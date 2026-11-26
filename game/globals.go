package game

import (
	"math"
	"sort"
	"strings"

	"shotdb/dm"
)

func map4(m tree, key string) dm.Map4 {
	l, ok := m[key].([]any)
	if !ok || len(l) != 4 {
		return dm.Map4{}
	}
	var out dm.Map4
	for i, v := range l {
		out[i], _ = v.(float64)
	}
	return out
}

func blast(m tree) dm.Blast {
	c := func(k string) []dm.Point {
		t, _ := m[k].(tree)
		return points(t, "p")
	}
	return dm.Blast{Inner: c("explosiveMassToInnerRadius"), Outer: c("explosiveMassToOuterRadius"), Pen: c("explosiveMassToPenetration"), Damage: c("explosiveMassToDamage")}
}

func (d *Data) loadGlobals(dmb, expl tree) error {
	g := &d.Globals
	g.PierceDispersion = numOr(dmb, "pierceDispersion", 0.05)
	g.JetDispersion = numOr(dmb, "cumulativePierceDispersion", 0.05)
	g.AirArmor = numOr(dmb, "cumulativeAirArmor", 100)
	g.FragThin = numOr(dmb, "armorFragmentsThicknessThreshold", 3.3)
	g.FragPierce = numOr(dmb, "armorFragmentsPierceThreshold", 90)
	g.RicochetSpeedMul = numOr(dmb, "ricochetSpeedMul", 0.3)
	g.EffectThreshold = numOr(dmb, "partDamageEffectThreshold", 0.01)
	g.ExplOffset = numOr(dmb, "defaultExplosionOffset", 0.05)
	g.SplashThreshold = numOr(dmb, "splashPenetrationToArmorThreshold", 0.33)
	g.Integrity = map4(dmb, "shellIntegrityAfterExplosionByExplosiveMass")
	if e, ok := dmb["kineticEnergyToDamage"].(tree); ok {
		g.EnergyToDamage = points(e, "p")
	}
	if r, ok := dmb["ricochetPenetrationModifier"].(tree); ok {
		for _, p := range points(r, "mod") {
			g.RicochetPenKept = append(g.RicochetPenKept, dm.Point{X: math.Sin(p.X * math.Pi / 180), Y: p.Y})
		}
	}

	g.Strength, g.Brisance = map[string]float64{}, map[string]float64{}
	if types, ok := expl["explosiveTypes"].(tree); ok {
		for name, v := range types {
			if t, ok := v.(tree); ok {
				g.Strength[name] = numOr(t, "strengthEquivalent", 1)
				g.Brisance[name] = numOr(t, "brisanceEquivalent", 1)
			}
		}
	}
	if b, ok := expl["explosiveTypeToSplashParams"].(tree); ok {
		g.Splash = blast(b)
	}
	if b, ok := expl["explosiveTypeToPressureParams"].(tree); ok {
		g.Closed = blast(b)
	}
	if b, ok := expl["explosiveTypeToPressureOpenParams"].(tree); ok {
		g.Opened = blast(b)
	}
	if sc, ok := expl["explosiveTypeToShattersParams"].(tree); ok {
		for name, v := range sc {
			c, ok := v.(tree)
			if !ok {
				continue
			}
			curve := func(k string) []dm.Point {
				t, _ := c[k].(tree)
				return points(t, "p")
			}
			g.Shatters = append(g.Shatters, dm.ShatterClass{
				Name: name, Fill: numOr(c, "fillingRatio", 0),
				Radius: curve("explosiveMassToRadius"), Pen: curve("explosiveMassToPenetration"),
				Dmg: curve("explosiveMassToDamage"), Cnt: curve("bodyMassToShattersCount"),
			})
		}
		sort.Slice(g.Shatters, func(i, j int) bool { return g.Shatters[i].Fill < g.Shatters[j].Fill })
	}

	presets, err := d.blk("aces", "gamedata/damage_model/secondary_shatter_presets.blk")
	if err != nil {
		return err
	}
	g.Spall = map[string]*dm.SpallPreset{}
	for name, v := range presets {
		if p, ok := v.(tree); ok {
			g.Spall[name] = spallPreset(p)
		}
	}
	return nil
}

func spallPreset(p tree) *dm.SpallPreset {
	s := &dm.SpallPreset{
		ByNormal: p["byNormal"] == true,
		Count:    map4(p, "residualArmorPenetrationToShatterCountMult"),
		Pen:      map4(p, "residualArmorPenetrationToShatterPenetrationMult"),
		Dmg:      map4(p, "residualArmorPenetrationToShatterDamageMult"),
		Cal:      map4(p, "caliberToArmorToShatterCountMult"),

		ArmorCount: map4(p, "armorMassToShatterCount"),
		ShellCount: map4(p, "shellMassToShatterCount"),
		ArmorPen:   map4(p, "residualPenetrationToArmorShatterPenetrationMult"),
		ArmorDmg:   map4(p, "residualPenetrationToArmorShatterDamageMult"),
		ShellPen:   map4(p, "residualPenetrationToShellShatterPenetrationMult"),
		ShellDmg:   map4(p, "residualPenetrationToShellShatterDamageMult"),
	}
	s.Mass = s.ArmorCount != (dm.Map4{}) || s.ShellCount != (dm.Map4{})
	if !s.Mass && s.Cal == (dm.Map4{}) {
		s.Cal = dm.Map4{0.5, 1, 0.5, 1}
	}
	for _, k := range sortedKeys(p) {
		sec, ok := p[k].(tree)
		if !ok || !strings.HasPrefix(k, "section") {
			continue
		}
		sh, _ := sec["shatter"].(tree)
		a, _ := sec["angles"].([]any)
		ang, _ := vec2(a)
		pair := func(key string) [2]float64 {
			l, _ := sh[key].([]any)
			v, _ := vec2(l)
			return [2]float64{v.X, v.Y}
		}
		count := numOr(sh, "count", 0)
		if s.Mass {
			count = numOr(sh, "countPortion", 0)
		}
		agg := true
		if b, ok := sh["aggregateDamage"].(bool); ok {
			agg = b
		}
		s.Sections = append(s.Sections, dm.SpallSection{
			Inner: ang.X, Outer: ang.Y, Distance: numOr(sh, "distance", 1), Count: count,
			Shell: sh["shellShatter"] == true, Pen: pair("penetration"), Dmg: pair("damage"),
			Aggregate: agg, FireMult: numOr(sh, "onHitChanceMultFire", 1),
		})
	}
	return s
}
