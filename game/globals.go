package game

import (
	"math"
	"sort"
	"strings"
)

// Globals are the damage model values past the armour tables.
type Globals struct {
	PierceDispersion     float64 // spread of each arrival penetration
	JetDispersion        float64 // cumulativePierceDispersion
	AirArmor             float64 // cumulativeAirArmor, mm of jet lost per metre of air
	FragThin, FragPierce float64 // armorFragmentsThicknessThreshold, armorFragmentsPierceThreshold
	RicochetSpeedMul     float64
	EffectThreshold      float64 // partDamageEffectThreshold
	ExplOffset           float64 // defaultExplosionOffset
	SplashThreshold      float64 // splashPenetrationToArmorThreshold
	Integrity            Map4    // explosive grams -> chance the body breaks at its burst
	EnergyToDamage       []Point // kinetic energy J -> damage
	RicochetPenKept      []Point // cos -> share of penetration kept after a ricochet

	Spall    map[string]*SpallPreset
	Strength map[string]float64 // explosive type -> strengthEquivalent (TNT for splash and pressure)
	Brisance map[string]float64 // explosive type -> brisanceEquivalent (TNT for fragments)
	Splash   Blast
	Closed   Blast // overpressure in a closed compartment
	Opened   Blast // and in an open one
	Shatters []ShatterClass
}

// Map4 is a clamped linear map [x0, x1, y0, y1]. The zero value is 1 everywhere.
type Map4 [4]float64

func (m Map4) At(x float64) float64 {
	if m == (Map4{}) {
		return 1
	}
	if m[1] == m[0] {
		return m[3]
	}
	f := math.Max(0, math.Min(1, (x-m[0])/(m[1]-m[0])))
	return m[2] + (m[3]-m[2])*f
}

func map4(m tree, key string) Map4 {
	l, ok := m[key].([]any)
	if !ok || len(l) != 4 {
		return Map4{}
	}
	var out Map4
	for i, v := range l {
		out[i], _ = v.(float64)
	}
	return out
}

// Blast gives radii (m), penetration (mm) and damage against the TNT mass in kg.
type Blast struct{ Inner, Outer, Pen, Damage []Point }

func blast(m tree) Blast {
	c := func(k string) []Point {
		t, _ := m[k].(tree)
		return points(t, "p")
	}
	return Blast{c("explosiveMassToInnerRadius"), c("explosiveMassToOuterRadius"), c("explosiveMassToPenetration"), c("explosiveMassToDamage")}
}

// ShatterClass is a fragment class of explosiveTypeToShattersParams.
type ShatterClass struct {
	Name                  string
	Fill                  float64 // largest TNT to shell mass ratio of the class
	Radius, Pen, Dmg, Cnt []Point // against the TNT mass, and Cnt against the body mass
}

// SpallPreset is a secondary shatter preset: the cone behind a perforated part.
type SpallPreset struct {
	ByNormal bool // the cone axis is the plate normal (HESH), not the flight line
	// Residual mode: the counts, penetrations and damages scale on the residual
	// penetration, and the counts on the caliber to armour ratio.
	Count, Pen, Dmg, Cal Map4
	// Mass mode: the counts come from the shell and the plate mass.
	Mass                                   bool
	ArmorCount, ShellCount                 Map4
	ArmorPen, ArmorDmg, ShellPen, ShellDmg Map4
	Sections                               []SpallSection
}

type SpallSection struct {
	Inner, Outer float64 // cone angles, degrees
	Distance     float64 // range, m
	Count        float64 // fragments, or the share of the total in mass mode
	Shell        bool    // shellShatter: counted on the shell in mass mode
	Pen, Dmg     [2]float64
	Aggregate    bool // the fragments of the section hitting one part make one damage message
	FireMult     float64
}

func (d *Data) loadGlobals(dm, expl tree) error {
	g := &d.Globals
	g.PierceDispersion = numOr(dm, "pierceDispersion", 0.05)
	g.JetDispersion = numOr(dm, "cumulativePierceDispersion", 0.05)
	g.AirArmor = numOr(dm, "cumulativeAirArmor", 100)
	g.FragThin = numOr(dm, "armorFragmentsThicknessThreshold", 3.3)
	g.FragPierce = numOr(dm, "armorFragmentsPierceThreshold", 90)
	g.RicochetSpeedMul = numOr(dm, "ricochetSpeedMul", 0.3)
	g.EffectThreshold = numOr(dm, "partDamageEffectThreshold", 0.01)
	g.ExplOffset = numOr(dm, "defaultExplosionOffset", 0.05)
	g.SplashThreshold = numOr(dm, "splashPenetrationToArmorThreshold", 0.33)
	g.Integrity = map4(dm, "shellIntegrityAfterExplosionByExplosiveMass")
	if e, ok := dm["kineticEnergyToDamage"].(tree); ok {
		g.EnergyToDamage = points(e, "p")
	}
	if r, ok := dm["ricochetPenetrationModifier"].(tree); ok {
		for _, p := range points(r, "mod") {
			g.RicochetPenKept = append(g.RicochetPenKept, Point{math.Sin(p.X * math.Pi / 180), p.Y})
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
			curve := func(k string) []Point {
				t, _ := c[k].(tree)
				return points(t, "p")
			}
			g.Shatters = append(g.Shatters, ShatterClass{
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
	g.Spall = map[string]*SpallPreset{}
	for name, v := range presets {
		if p, ok := v.(tree); ok {
			g.Spall[name] = spallPreset(p)
		}
	}
	return nil
}

func spallPreset(p tree) *SpallPreset {
	s := &SpallPreset{
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
	s.Mass = s.ArmorCount != (Map4{}) || s.ShellCount != (Map4{})
	if !s.Mass && s.Cal == (Map4{}) {
		s.Cal = Map4{0.5, 1, 0.5, 1}
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
		s.Sections = append(s.Sections, SpallSection{
			Inner: ang.X, Outer: ang.Y, Distance: numOr(sh, "distance", 1), Count: count,
			Shell: sh["shellShatter"] == true, Pen: pair("penetration"), Dmg: pair("damage"),
			Aggregate: agg, FireMult: numOr(sh, "onHitChanceMultFire", 1),
		})
	}
	return s
}
