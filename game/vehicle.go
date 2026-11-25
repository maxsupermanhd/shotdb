package game

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"shotdb/geom"
)

// Vehicle is a unit with its damage parts, collision model, crew, ammo racks
// and the shells of its guns.
type Vehicle struct {
	Unit
	Model  string
	Parts  map[string]*Part // by name, without the _dm suffix
	Shells []Shell

	Crew    []Crew
	MinCrew int // fewer crew alive ends the vehicle
	// Racks maps each ammo part to its rack. The racks are taken as full.
	Racks map[string]Rack
	// RackExplosive tells whether the stowed round (the main gun's first round) has a filler.
	RackExplosive bool
	// Pressure lists the parts of the closed compartments that overpressure acts on, and Open the open ones.
	Pressure, Open []string

	hidden map[string]bool // collision nodes not on the stock vehicle
}

type Crew struct {
	Part  string
	Count int
}

type Rack struct {
	FatalFire, FatalExplosion bool
	Charges                   bool // a charges block of two-piece ammo
}

// Part is one damage part. Values come from the part's own keys, then its
// groups' keys (inner first), then its armour class.
type Part struct {
	Name, Group, Class string
	Thickness          float64
	Quality            float64            // armorQuality
	Mech               map[string]float64 // {mech}ArmorQuality
	EffMax             float64            // armorEffectiveThicknessMax, 0 if none
	MechMax            map[string]float64 // {mech}EffectiveThicknessMax
	Through            float64            // armorThrough
	HP                 float64
	DamageMult         map[string]float64 // {type}DamageMult
	Spall              bool               // createSecondaryShatters
	IgnoreSolid        bool               // ignoreSolidDimension
	Ricochet           bool               // allowRicochet, the class's value first
	Restrain           float64            // restrainDamage, 1 if none
	SlopeMin           float64            // slopeMinCaliber in mm, 0 if none
	Module             bool               // crew, ammo, fuel, engine, breech, optics and other equipment
	OnHit, OnKill      []Rule
}

// Rule is one onHit or onKill rule of DamageEffects.
type Rule struct {
	Types    []string // damage types; none matches every type
	Not      []string // notDamageType
	Damage   float64
	Effects  []Effect // sorted by name
	Critical bool
}

// Effect is a chance of expl, full_expl, fire, nothing, cut, or of killing a named part.
type Effect struct {
	Name string
	P    float64
}

// Matches tells whether the rule takes a damage type.
func (r Rule) Matches(kind string) bool {
	for _, n := range r.Not {
		if n == kind {
			return false
		}
	}
	if len(r.Types) == 0 {
		return true
	}
	for _, t := range r.Types {
		if t == kind {
			return true
		}
	}
	return false
}

// Mechanisms the simulation charges parts for.
var mechs = []string{"generic", "cumulative", "explosion", "secondaryShatter", "shatter", "tandemPrecharge"}

// Q is the armour quality against a damage mechanism.
func (p *Part) Q(mech string) float64 {
	m, ok := p.Mech[mech]
	if !ok {
		m = 1
	}
	return m * p.Quality
}

// Cap is the effective thickness cap against a mechanism, +Inf if none.
func (p *Part) Cap(mech string) float64 {
	c := math.Inf(1)
	if p.EffMax > 0 {
		c = p.EffMax
	}
	if m := p.MechMax[mech]; m > 0 && m < c {
		c = m
	}
	return c
}

// Mult is the part's damage multiplier for a damage type.
func (p *Part) Mult(kind string) float64 {
	if m, ok := p.DamageMult[kind]; ok {
		return m
	}
	return 1
}

// Shell is one round of a vehicle's guns, with what the simulation reads.
type Shell struct {
	Name    string  // bulletName, for example 75mm_m61
	Label   string  // display name from the language files
	Gun     string  // display name of the weapon that fires it
	Type    string  // bulletType, for example apcbc_tank
	Caliber float64 // m
	DmgCal  float64 // damageCaliber in m, else caliber
	BalCal  float64 // ballisticCaliber in m, else caliber
	Mass    float64 // kg
	DmgMass float64 // damageMass, else mass
	Speed   float64 // m/s at the muzzle
	Cx      float64
	Rocket  bool

	DeMarre     *DeMarre
	LanzOd      *LanzOdermatt
	ArmorPower  []Point // range m -> penetration mm, for rounds without a formula
	KineticType string  // damage type of the body: generic, or tandemPrecharge for a tandem rocket

	Cumulative float64 // jet armorPower in mm, 0 if none
	ByNormal   bool    // cumulativeByNormal: the jet ignores the angle (HESH)
	CumDist    float64 // jet length, m
	CumDamage  float64 // jet damage
	JetSpall   string  // cumulativeSecondaryShattersPreset

	ExplType       string
	ExplMass       float64 // kg
	ExplThresh     float64 // explodeTreshold, mm
	ExplOffset     float64 // burst distance in front of a face, m; -1 for the game default
	FuseDelay      float64 // fuseDelayDist, m
	CheckIntegrity bool    // checkIntegrityAfterExplosion: the body flies on after its burst
	Pressure       bool    // the round has overpressure (pressureDamage)
	Shatter        *Shatter

	Spall          string // secondaryShattersPreset
	SlopePreset    string
	RicochetPreset string

	BreakMin, BreakMax, BreakSpeed float64 // core breakup: armour range in mm and speed in m/s
	BreakLOS                       bool    // breakingArmorThicknessEffective
	BreakScale                     []Point // breakingScaleByArmor
	BreakAir                       []Point // breakingScaleByAir: air gap m -> factor
}

// Shatter is a warhead's body fragments (damage.shatter with useRealShatters).
type Shatter struct {
	CountPortion float64
	Segments     []Segment
}

type Segment struct {
	RadiusScale, PenScale, DmgScale float64
	Inner, Outer                    float64 // degrees from the flight direction
	Portion                         float64
}

type DeMarre struct{ K, SpeedPow, MassPow, CaliberPow float64 }

type LanzOdermatt struct {
	Material                  string
	Length, Density, Hardness float64
}

// Vehicle loads a unit, once.
func (d *Data) Vehicle(id string) (*Vehicle, error) {
	id = strings.ToLower(id)
	d.mu.Lock()
	defer d.mu.Unlock()
	if v, ok := d.vehicles[id]; ok {
		return v, nil
	}
	u, ok := d.Units[id]
	if !ok {
		return nil, fmt.Errorf("%s: not a ground unit of this game data", id)
	}
	t, err := d.blk("aces", "gamedata/units/tankmodels/"+id+".blk")
	if err != nil {
		return nil, err
	}
	v := &Vehicle{Unit: u, Model: str(t, "model"), Parts: map[string]*Part{}, Racks: map[string]Rack{}, MinCrew: 2}
	if parts, ok := t["DamageParts"].(tree); ok {
		d.parts(parts, "", nil, v.Parts)
	}
	d.effects(t, v)
	d.crewAndRacks(t, v)
	if err := d.shells(t, v); err != nil {
		return nil, err
	}

	if v.Model == "" {
		return nil, fmt.Errorf("%s: no model", id)
	}
	v.hidden = map[string]bool{}
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
						v.hidden[strings.TrimSuffix(n, "_dm")] = true
					}
				}
			}
		}
	}
	d.vehicles[id] = v
	return v, nil
}

// Nodes loads the vehicle's collision nodes that are damage parts, named
// without the _dm suffix. They are read each time and not kept: they are large.
func (d *Data) Nodes(v *Vehicle) ([]geom.Node, error) {
	nodes, err := d.collision(v.ID, v.Model)
	if err != nil {
		return nil, err
	}
	var out []geom.Node
	for _, n := range nodes {
		name, ok := strings.CutSuffix(n.Name, "_dm")
		if !ok || v.hidden[name] || v.Parts[name] == nil || len(n.Tris) == 0 {
			continue
		}
		n.Name = name
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no collision node names a damage part", v.ID)
	}
	return out, nil
}

var moduleGroups = map[string]bool{
	"crew": true, "crew_turret": true, "ammo": true, "fuel_tanks": true, "fuel_tanks_exterior": true,
	"power_block": true, "engine": true, "cannon_breech": true, "gun": true, "machine_gun": true,
	"optics": true, "commander_panoramic_sight": true, "equipment_body_turret": true,
}

var moduleClasses = map[string]bool{"steel_tankman": true, "armor_tank_engine": true, "optics_tank": true, "tank_barrel": true}

// parts walks DamageParts. Keys ending in _dm are parts; other blocks are
// groups whose plain keys apply to the parts inside them.
func (d *Data) parts(group tree, groupName string, outer []tree, out map[string]*Part) {
	chain := append([]tree{group}, outer...)
	for name, v := range group {
		b, ok := v.(tree)
		if !ok {
			continue
		}
		if !strings.HasSuffix(name, "_dm") {
			d.parts(b, name, chain, out)
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
		p := &Part{
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
			if q, ok := lookup(m + "ArmorQuality"); ok {
				if f, ok := q.(float64); ok {
					p.Mech[m] = f
				}
			}
			if c := number(m+"EffectiveThicknessMax", 0); c > 0 {
				p.MechMax[m] = c
			}
		}
		for _, s := range append(scopes, cls) {
			for k, v := range s {
				if t, ok := strings.CutSuffix(k, "DamageMult"); ok {
					if f, ok := v.(float64); ok {
						if _, seen := p.DamageMult[t]; !seen {
							p.DamageMult[t] = f
						}
					}
				}
			}
		}
		out[p.Name] = p
	}
}

// effects reads DamageEffects into the parts' rules, onHit sorted by damage, highest first.
func (d *Data) effects(t tree, v *Vehicle) {
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

func rules(list []tree) []Rule {
	var out []Rule
	for _, b := range list {
		r := Rule{Types: strs(b, "damageType"), Not: strs(b, "notDamageType"), Damage: numOr(b, "damage", 0)}
		r.Critical = b["fHitCritical"] == true
		for _, k := range sortedKeys(b) {
			if f, ok := b[k].(float64); ok && k != "damage" {
				r.Effects = append(r.Effects, Effect{strings.TrimSuffix(k, "_dm"), f})
			}
		}
		out = append(out, r)
	}
	return out
}

func (d *Data) crewAndRacks(t tree, v *Vehicle) {
	if crew, ok := t["tank_crew"].(tree); ok {
		v.MinCrew = int(numOr(crew, "minCrewMemberAliveCount", 2))
		keys := sortedKeys(crew)
		for _, k := range keys {
			if m, ok := crew[k].(tree); ok && str(m, "dmPart") != "" {
				v.Crew = append(v.Crew, Crew{Part: strings.TrimSuffix(str(m, "dmPart"), "_dm"), Count: int(numOr(m, "crew", 1))})
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
					r := Rack{FatalFire: flag("fatalFire"), FatalExplosion: flag("fatalExplosion"), Charges: kind == "charges"}
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

func (d *Data) shells(t tree, v *Vehicle) error {
	seen := map[string]bool{}
	first := true
	for _, cw := range blocks(t, "commonWeapons") {
		for _, w := range blocks(cw, "Weapon") {
			path := str(w, "blk")
			if path == "" || strings.HasSuffix(path, "dummy_weapon.blk") {
				continue
			}
			wt, ok := d.weapons[path]
			if !ok {
				var err error
				if wt, err = d.blk("aces", path); err != nil {
					return fmt.Errorf("%s: %w", v.ID, err)
				}
				d.weapons[path] = wt
			}
			base := strings.TrimSuffix(path[strings.LastIndex(path, "/")+1:], ".blk")
			gun := d.names["weapons/"+base]
			if gun == "" {
				gun = base
			}
			for _, b := range bullets(wt) {
				s, err := d.shell(b)
				if err != nil {
					return fmt.Errorf("%s: %w", v.ID, err)
				}
				if first {
					v.RackExplosive = s.ExplMass > 0
					first = false
				}
				key := s.Name + "/" + s.Type
				// 0.0199: a 20 mm calibre reads as 0.0199999 from the float32 files.
				if s.Caliber < 0.0199 || seen[key] || !usable(s) {
					continue
				}
				seen[key] = true
				s.Gun = gun
				v.Shells = append(v.Shells, s)
			}
		}
	}
	return nil
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

// usable drops rounds that do not attack armour.
func usable(s Shell) bool {
	for _, p := range []string{"smoke", "sam", "aam", "football", "napalm", "sonic", "snowball", "practice"} {
		if strings.HasPrefix(s.Type, p) {
			return false
		}
	}
	return s.Type != ""
}

// projectile gives the shared defaults of a bullet type, once.
func (d *Data) projectile(kind string) (tree, error) {
	if t, ok := d.ptypes[kind]; ok {
		return t, nil
	}
	t, err := d.blk("aces", "gamedata/damage_model/projectile_types/"+kind+".blk")
	if err != nil && !isNotExist(err) {
		return nil, err
	}
	d.ptypes[kind] = t
	return t, nil
}

func (d *Data) shell(b tree) (Shell, error) {
	// The bullet's own keys win over its projectile type's.
	def, err := d.projectile(str(b, "bulletType"))
	if err != nil {
		return Shell{}, err
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
	s := Shell{
		Name:           str(b, "bulletName"),
		Type:           str(b, "bulletType"),
		Caliber:        numOr(b, "caliber", 0),
		Mass:           numOr(b, "mass", 0),
		Cx:             numOr(b, "Cx", 0),
		Rocket:         b["rocket"] == true,
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
		s.DeMarre = &DeMarre{K: k, SpeedPow: numOr(kin, "demarreSpeedPow", 1.43), MassPow: numOr(kin, "demarreMassPow", 0.71), CaliberPow: numOr(kin, "demarreCaliberPow", 1.07)}
	} else if l, ok := num(kin, "lanzOdermattWorkingLength"); ok {
		s.LanzOd = &LanzOdermatt{Material: str(kin, "lanzOdermattMaterial"), Length: l, Density: numOr(kin, "lanzOdermattDensity", 17500), Hardness: numOr(kin, "lanzOdermattBrinellHardnessNumber", 470)}
	}
	if ap, ok := b["armorpower"].(tree); ok {
		for _, p := range points(ap, "ArmorPower") {
			s.ArmorPower = append(s.ArmorPower, Point{p.Y, p.X})
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
		s.Shatter = &Shatter{CountPortion: numOr(sh, "countPortion", 1)}
		for _, g := range blocks(sh, "segment") {
			a, _ := g["angles"].([]any)
			ang, _ := vec2(a)
			s.Shatter.Segments = append(s.Shatter.Segments, Segment{
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
