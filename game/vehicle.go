package game

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"

	"shotdb/dm"
	"shotdb/geom"
	"shotdb/grp"
)

// Mechanisms the simulation charges parts for, each with its keys {mech}ArmorQuality and {mech}EffectiveThicknessMax.
var mechs = func() (out [][3]string) {
	for _, m := range []string{"generic", "cumulative", "explosion", "secondaryShatter", "shatter", "tandemPrecharge"} {
		out = append(out, [3]string{m, m + "ArmorQuality", m + "EffectiveThicknessMax"})
	}
	return out
}()

// keptVehicles bounds the memory: a queue loads every unit once, and a loaded
// unit takes about 0.2 MB.
const keptVehicles = 32

// Vehicle loads a unit. The last units loaded are kept.
func (d *Data) Vehicle(id string) (*dm.Vehicle, error) {
	id = strings.ToLower(id)
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, v := range d.vehicles {
		if v.ID == id {
			d.vehicles = append(append(d.vehicles[:i:i], d.vehicles[i+1:]...), v)
			return v, nil
		}
	}
	u, ok := d.Units[id]
	if !ok {
		return nil, fmt.Errorf("%s: not a ground unit of this game data", id)
	}
	t, err := d.blk("aces", "gamedata/units/tankmodels/"+id+".blk")
	if err != nil {
		return nil, err
	}
	v := &dm.Vehicle{Unit: u, Model: str(t, "model"), Parts: map[string]*dm.Part{}, Racks: map[string]dm.Rack{}, MinCrew: 2}
	if parts, ok := t["DamageParts"].(tree); ok {
		d.parts(parts, "", nil, nil, v.Parts)
	}
	d.effects(t, v)
	d.crewAndRacks(t, v)
	if err := d.shells(t, v); err != nil {
		return nil, err
	}

	if v.Model == "" {
		return nil, fmt.Errorf("%s: no model", id)
	}
	v.Hidden = map[string]bool{}
	if mods, ok := t["modifications"].(tree); ok {
		for _, m := range mods {
			mt, ok := m.(tree)
			if !ok {
				continue
			}
			// Nodes hidden while the modification is not fitted; units are taken stock.
			for _, off := range blocks(mt, "disableModEffects") {
				for _, h := range blocks(off, "hideNodes") {
					for _, n := range strs(h, "node") {
						v.Hidden[strings.TrimSuffix(n, "_dm")] = true
					}
				}
			}
		}
	}
	if len(d.vehicles) >= keptVehicles {
		d.vehicles = d.vehicles[1:]
	}
	d.vehicles = append(d.vehicles, v)
	return v, nil
}

// Nodes loads the vehicle's collision nodes that are damage parts, named
// without the _dm suffix. They are read each time and not kept: they are large.
func (d *Data) Nodes(v *dm.Vehicle) ([]geom.Node, error) {
	path, off, n, err := d.Collision(v)
	if err != nil {
		return nil, err
	}
	raw, err := d.files.ReadAt(path, off, n)
	if err != nil {
		return nil, err
	}
	nodes, err := grp.ParseCollision(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %s_collision: %w", v.ID, strings.ToLower(v.Model), err)
	}
	return v.DamageNodes(nodes)
}

var moduleGroups = map[string]bool{
	"crew": true, "crew_turret": true, "ammo": true, "fuel_tanks": true, "fuel_tanks_exterior": true,
	"power_block": true, "engine": true, "cannon_breech": true, "gun": true, "machine_gun": true,
	"optics": true, "commander_panoramic_sight": true, "equipment_body_turret": true,
}

var moduleClasses = map[string]bool{"steel_tankman": true, "armor_tank_engine": true, "optics_tank": true, "tank_barrel": true}

// parts walks DamageParts. Keys ending in _dm are parts; other blocks are
// groups whose plain keys apply to the parts inside them. outerMults holds
// the damage multipliers of each block of outer, as a group block has a key
// for each of its parts and is too slow to walk for each part.
func (d *Data) parts(group tree, groupName string, outer []tree, outerMults []map[string]float64, out map[string]*dm.Part) {
	chain := append([]tree{group}, outer...)
	chainMults := append([]map[string]float64{damageMults(group)}, outerMults...)
	for name, v := range group {
		b, ok := v.(tree)
		if !ok {
			continue
		}
		if !strings.HasSuffix(name, "_dm") {
			d.parts(b, name, chain, chainMults, out)
			continue
		}
		scopes := append([]tree{b}, chain...)
		class := ""
		for _, s := range scopes {
			if class = str(s, "armorClass"); class != "" {
				break
			}
		}
		cls := d.Classes[class]
		lookup := func(key string) (any, bool) {
			for _, s := range scopes {
				if v, ok := s[key]; ok {
					return v, true
				}
			}
			v, ok := cls[key]
			return v, ok
		}
		number := func(key string, def float64) float64 {
			if v, ok := lookup(key); ok {
				if f, ok := v.(float64); ok {
					return f
				}
			}
			return def
		}
		flag := func(key string, def bool) bool {
			if v, ok := lookup(key); ok {
				if b, ok := v.(bool); ok {
					return b
				}
			}
			return def
		}
		p := &dm.Part{
			Name:        strings.TrimSuffix(name, "_dm"),
			Group:       groupName,
			Class:       class,
			Thickness:   number("armorThickness", 0),
			Quality:     number("armorQuality", 1),
			EffMax:      number("armorEffectiveThicknessMax", 0),
			Through:     number("armorThrough", 1),
			HP:          number("hp", 0),
			Spall:       flag("createSecondaryShatters", true),
			IgnoreSolid: flag("ignoreSolidDimension", false),
			Restrain:    number("restrainDamage", 1),
			SlopeMin:    number("slopeMinCaliber", 0),
			Mech:        map[string]float64{},
			MechMax:     map[string]float64{},
			DamageMult:  map[string]float64{},
			Module:      moduleGroups[groupName] || moduleClasses[class],
		}
		// The class's allowRicochet wins over the part's (a track group sets true on a class with false).
		p.Ricochet = true
		if r, ok := cls["allowRicochet"].(bool); ok {
			p.Ricochet = r
		} else {
			p.Ricochet = flag("allowRicochet", true)
		}
		for _, m := range mechs {
			if q, ok := lookup(m[1]); ok {
				if f, ok := q.(float64); ok {
					p.Mech[m[0]] = f
				}
			}
			if c := number(m[2], 0); c > 0 {
				p.MechMax[m[0]] = c
			}
		}
		for _, m := range append(append([]map[string]float64{damageMults(b)}, chainMults...), d.mults[class]) {
			for t, f := range m {
				if _, seen := p.DamageMult[t]; !seen {
					p.DamageMult[t] = f
				}
			}
		}
		out[p.Name] = p
	}
}

// damageMults gives the {type}DamageMult values of a block by type.
func damageMults(b tree) map[string]float64 {
	m := map[string]float64{}
	for k, v := range b {
		if t, ok := strings.CutSuffix(k, "DamageMult"); ok {
			if f, ok := v.(float64); ok {
				m[t] = f
			}
		}
	}
	return m
}

// effects reads DamageEffects into the parts' rules, onHit sorted by damage, highest first.
func (d *Data) effects(t tree, v *dm.Vehicle) {
	de, _ := t["DamageEffects"].(tree)
	for _, e := range blocks(de, "part") {
		names := strs(e, "name")
		onHit := rules(blocks(e, "onHit"))
		onKill := rules(blocks(e, "onKill"))
		sort.SliceStable(onHit, func(i, j int) bool { return onHit[i].Damage > onHit[j].Damage })
		for _, n := range names {
			if p := v.Parts[strings.TrimSuffix(n, "_dm")]; p != nil {
				p.OnHit, p.OnKill = onHit, onKill
			}
		}
	}
}

func rules(list []tree) []dm.Rule {
	var out []dm.Rule
	for _, b := range list {
		r := dm.Rule{Types: strs(b, "damageType"), Not: strs(b, "notDamageType"), Damage: numOr(b, "damage", 0)}
		for _, k := range sortedKeys(b) {
			if f, ok := b[k].(float64); ok && k != "damage" {
				r.Effects = append(r.Effects, dm.Effect{Name: strings.TrimSuffix(k, "_dm"), P: f})
			}
		}
		out = append(out, r)
	}
	return out
}

func (d *Data) crewAndRacks(t tree, v *dm.Vehicle) {
	if crew, ok := t["tank_crew"].(tree); ok {
		v.MinCrew = int(numOr(crew, "minCrewMemberAliveCount", 2))
		keys := sortedKeys(crew)
		for _, k := range keys {
			if m, ok := crew[k].(tree); ok && str(m, "dmPart") != "" {
				v.Crew = append(v.Crew, dm.Crew{Part: strings.TrimSuffix(str(m, "dmPart"), "_dm"), Count: int(numOr(m, "crew", 1))})
			}
		}
	}
	if st, ok := t["ammoStowages"].(tree); ok {
		for _, s := range st {
			stow, ok := s.(tree)
			if !ok {
				continue
			}
			for _, kind := range []string{"shells", "charges"} {
				for _, b := range blocks(stow, kind) {
					flag := func(key string) bool {
						if f, ok := b[key].(bool); ok {
							return f
						}
						f, _ := stow[key].(bool)
						return f
					}
					r := dm.Rack{FatalFire: flag("fatalFire"), FatalExplosion: flag("fatalExplosion"), Charges: kind == "charges"}
					for k, val := range b {
						if _, ok := val.(tree); ok && strings.HasSuffix(k, "_dm") {
							v.Racks[strings.TrimSuffix(k, "_dm")] = r
						}
					}
				}
			}
		}
	}
	if mp, ok := t["MetaParts"].(tree); ok {
		for _, m := range mp {
			b, ok := m.(tree)
			if !ok {
				continue
			}
			for _, e := range blocks(b, "effect") {
				var list *[]string
				switch {
				case e["pressure"] == true:
					list = &v.Pressure
				case e["pressureOpen"] == true:
					list = &v.Open
				default:
					continue
				}
				for _, n := range strs(b, "part") {
					*list = append(*list, strings.TrimSuffix(n, "_dm"))
				}
				break
			}
		}
	}
}

func (d *Data) shells(t tree, v *dm.Vehicle) error {
	var list []tree
	for _, cw := range blocks(t, "commonWeapons") {
		list = append(list, blocks(cw, "Weapon")...)
	}
	seen := map[string]bool{}
	first := true
	var main tree
	var gunMounts []tree
	for _, w := range list {
		path := str(w, "blk")
		if path == "" || strings.HasSuffix(path, "dummy_weapon.blk") {
			continue
		}
		wp, err := d.weapon(path)
		if err != nil {
			return fmt.Errorf("%s: %w", v.ID, err)
		}
		// The barrels of the weapon: its entries with the same file and trigger.
		var mounts []tree
		for _, o := range list {
			if str(o, "blk") == path && str(o, "trigger") == str(w, "trigger") {
				mounts = append(mounts, o)
			}
		}
		for _, s := range wp.shells {
			if first {
				v.RackExplosive = s.ExplMass > 0
				first = false
			}
			key := s.Name + "/" + s.Type
			// Rounds of these types do not attack armour.
			noArmour := slices.ContainsFunc([]string{"smoke", "sam", "aam", "football", "napalm", "sonic", "snowball", "practice"}, func(p string) bool { return strings.HasPrefix(s.Type, p) })
			// 0.0199: a 20 mm calibre reads as 0.0199999 from the float32 files.
			if s.Caliber < 0.0199 || seen[key] || s.Type == "" || noArmour {
				continue
			}
			seen[key] = true
			s.Gun = wp.gun
			s.ShotFreq = numOr(w, "shotFreq", wp.shotFreq)
			s.Cartridge = wp.cartridge
			s.MagReload = numOr(w, "reloadTime", wp.reload)
			s.AutoLoader = slices.ContainsFunc(mounts, func(m tree) bool { return m["autoLoader"] == true })
			for _, m := range mounts {
				s.Barrels = append(s.Barrels, numOr(m, "timeOffset", 0))
			}
			if main == nil {
				main, gunMounts = w, mounts
			}
			v.Shells = append(v.Shells, s)
		}
	}
	if main == nil {
		return nil
	}
	w := main
	turret, _ := w["turret"].(tree)
	g := &dm.Gun{
		BrokenBarrelShoots: w["brokenBarrelAllowShoot"] == true,
		HorDrive:           trimDm(strs(turret, "horDriveDm")),
		VerDrive:           trimDm(strs(turret, "verDriveDm")),
		Gunner:             strings.TrimSuffix(str(turret, "gunnerDm"), "_dm"),
		Override:           strings.TrimSuffix(str(turret, "secondGunnerDm"), "_dm"),
		ChangeMult:         1,
	}
	for _, m := range gunMounts {
		g.Barrels = append(g.Barrels, strings.TrimSuffix(str(m, "barrelDP"), "_dm"))
		g.Breeches = append(g.Breeches, strings.TrimSuffix(str(m, "breechDP"), "_dm"))
	}
	if crew, ok := t["tank_crew"].(tree); ok {
		g.ChangeMult = numOr(crew, "changeTimeMult", 1)
		for _, k := range sortedKeys(crew) {
			m, ok := crew[k].(tree)
			if !ok || strings.TrimSuffix(str(m, "dmPart"), "_dm") != g.Gunner {
				continue
			}
			for _, sub := range strs(m, "substitute") {
				if sm, ok := crew[sub].(tree); ok {
					g.Substitutes = append(g.Substitutes, strings.TrimSuffix(str(sm, "dmPart"), "_dm"))
				}
			}
		}
	}
	v.Gun = g
	return nil
}

func trimDm(names []string) []string {
	for i, n := range names {
		names[i] = strings.TrimSuffix(n, "_dm")
	}
	return names
}

type weapon struct {
	gun       string
	shells    []dm.Shell // all rounds of the weapon file, in file order
	shotFreq  float64
	cartridge int
	reload    float64
}

// weapon gives the gun name and the rounds of a weapon file, once. Most
// weapon files are used by many units.
func (d *Data) weapon(path string) (weapon, error) {
	if wp, ok := d.weapons[path]; ok {
		return wp, nil
	}
	wt, err := d.blk("aces", path)
	if err != nil {
		return weapon{}, err
	}
	base := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".blk")
	wp := weapon{gun: d.names["weapons/"+base], shotFreq: numOr(wt, "shotFreq", 0), cartridge: int(numOr(wt, "bulletsCartridge", 1)), reload: numOr(wt, "reloadTime", 0)}
	if wp.gun == "" {
		wp.gun = base
	}
	for _, b := range bullets(wt) {
		s, err := d.shell(b)
		if err != nil {
			return weapon{}, err
		}
		wp.shells = append(wp.shells, s)
	}
	d.weapons[path] = wp
	return wp, nil
}

// bullets lists the rounds of a weapon file: its default bullet blocks and
// those of each named ammo block. A rocket's own block fills what the bullet lacks.
func bullets(w tree) []tree {
	list := blocks(w, "bullet")
	for _, k := range sortedKeys(w) {
		if b, ok := w[k].(tree); ok {
			list = append(list, blocks(b, "bullet")...)
		}
	}
	var out []tree
	for _, b := range list {
		r, ok := b["rocket"].(tree)
		if !ok {
			out = append(out, b)
			continue
		}
		merged := tree{"rocket": true}
		for k, v := range r {
			merged[k] = v
		}
		for k, v := range b {
			if k != "rocket" {
				merged[k] = v
			}
		}
		out = append(out, merged)
	}
	return out
}

// projectile gives the shared defaults of a bullet type, once.
func (d *Data) projectile(kind string) (tree, error) {
	if t, ok := d.ptypes[kind]; ok {
		return t, nil
	}
	t, err := d.blk("aces", "gamedata/damage_model/projectile_types/"+kind+".blk")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	d.ptypes[kind] = t
	return t, nil
}

func (d *Data) shell(b tree) (dm.Shell, error) {
	// The bullet's own keys win over its projectile type's.
	def, err := d.projectile(str(b, "bulletType"))
	if err != nil {
		return dm.Shell{}, err
	}
	if def != nil {
		merged := tree{}
		for k, v := range def {
			merged[k] = v
		}
		for k, v := range b {
			merged[k] = v
		}
		b = merged
	}
	s := dm.Shell{
		Name:           str(b, "bulletName"),
		Type:           str(b, "bulletType"),
		Caliber:        numOr(b, "caliber", 0),
		Mass:           numOr(b, "mass", 0),
		Cx:             numOr(b, "Cx", 0),
		Rocket:         b["rocket"] == true,
		Guided:         slices.ContainsFunc([]string{"guidance", "guidanceType", "operated", "operatedDist"}, func(k string) bool { _, ok := b[k]; return ok }),
		ExplType:       str(b, "explosiveType"),
		ExplMass:       numOr(b, "explosiveMass", 0),
		ExplThresh:     numOr(b, "explodeTreshold", 0),
		FuseDelay:      numOr(b, "fuseDelayDist", 0),
		CheckIntegrity: b["checkIntegrityAfterExplosion"] == true,
		Spall:          str(b, "secondaryShattersPreset"),
		JetSpall:       str(b, "cumulativeSecondaryShattersPreset"),
		SlopePreset:    str(b, "slopeEffectPreset"),
		RicochetPreset: str(b, "ricochetPreset"),
		BreakSpeed:     numOr(b, "breakingCriticalSpeed", 0),
		BreakLOS:       b["breakingArmorThicknessEffective"] == true,
		KineticType:    "generic",
		ExplOffset:     -1,
	}
	_, s.Pressure = b["pressureDamage"].(tree)
	s.DmgCal = numOr(b, "damageCaliber", s.Caliber)
	s.BalCal = numOr(b, "ballisticCaliber", s.Caliber)
	s.DmgMass = numOr(b, "damageMass", s.Mass)
	s.Speed = numOr(b, "speed", numOr(b, "endSpeed", numOr(b, "startSpeed", 0)))
	if s.Label = d.names[s.Name]; s.Label == "" {
		s.Label = s.Name
	}
	if r, ok := b["breakingArmorThickness"].([]any); ok {
		if p, ok := vec2(r); ok {
			s.BreakMin, s.BreakMax = p.X, p.Y
		}
	}

	dmg, _ := b["damage"].(tree)
	kin, _ := dmg["kinetic"].(tree)
	if kin == nil {
		kin, _ = b["kineticDamage"].(tree)
	}
	if t := str(kin, "damageType"); t != "" {
		s.KineticType = t
	}
	if k, ok := num(kin, "demarrePenetrationK"); ok {
		s.DeMarre = &dm.DeMarre{K: k, SpeedPow: numOr(kin, "demarreSpeedPow", 1.43), MassPow: numOr(kin, "demarreMassPow", 0.71), CaliberPow: numOr(kin, "demarreCaliberPow", 1.07)}
	} else if l, ok := num(kin, "lanzOdermattWorkingLength"); ok {
		s.LanzOd = &dm.LanzOdermatt{Material: str(kin, "lanzOdermattMaterial"), Length: l, Density: numOr(kin, "lanzOdermattDensity", 17500), Hardness: numOr(kin, "lanzOdermattBrinellHardnessNumber", 470)}
	}
	if ap, ok := b["armorpower"].(tree); ok {
		for _, p := range points(ap, "ArmorPower") {
			s.ArmorPower = append(s.ArmorPower, dm.Point{X: p.Y, Y: p.X})
		}
		sort.Slice(s.ArmorPower, func(i, j int) bool { return s.ArmorPower[i].X < s.ArmorPower[j].X })
	}
	if sc, ok := dmg["breakingScaleByArmor"].(tree); ok {
		s.BreakScale = points(sc, "mod")
	}
	if sc, ok := dmg["breakingScaleByAir"].(tree); ok {
		s.BreakAir = points(sc, "mod")
	}
	if e, ok := dmg["explosive"].(tree); ok {
		s.ExplOffset = numOr(e, "offset", -1)
	}
	if sh, ok := dmg["shatter"].(tree); ok && sh["useRealShatters"] == true {
		s.Shatter = &dm.Shatter{CountPortion: numOr(sh, "countPortion", 1)}
		for _, g := range blocks(sh, "segment") {
			a, _ := g["angles"].([]any)
			ang, _ := vec2(a)
			s.Shatter.Segments = append(s.Shatter.Segments, dm.Segment{
				RadiusScale: numOr(g, "radiusScale", 1), PenScale: numOr(g, "penetrationScale", 1), DmgScale: numOr(g, "damageScale", 1),
				Inner: ang.X, Outer: ang.Y, Portion: numOr(g, "countPortion", 0),
			})
		}
	}
	if c, ok := b["cumulativeDamage"].(tree); ok {
		s.Cumulative = numOr(c, "armorPower", 0)
		s.CumDist = numOr(c, "distance", 0)
		s.CumDamage = numOr(c, "damage", 0)
		s.ByNormal = b["cumulativeByNormal"] == true
	}
	return s, nil
}
