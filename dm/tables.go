package dm

import "math"

// Tables are the global damage model values that the simulation reads.
type Tables struct {
	Slope    map[string]Table // values are 1/S
	Ricochet map[string]Table // values are probabilities

	MaxArmorScale   float64
	ArmorBreachK    float64
	ArmorResistance float64
	FillerPenMod    []Point // filler mass / shell mass -> penetration multiplier

	Globals
}

// Table is a slope effect or ricochet preset: curves blended by caliberToArmor.
// Each curve is stored with x = sin(angle from the surface), so it is read at cos(angle from the normal).
type Table []Row

type Row struct {
	Ratio float64
	Curve []Point
}

// At reads the table at cos(angle from the normal), blending rows linearly on ratio.
func (t Table) At(cos, ratio float64) float64 {
	if len(t) == 0 {
		return 0
	}
	if ratio <= t[0].Ratio {
		return Interp(t[0].Curve, cos)
	}
	for i := 1; i < len(t); i++ {
		if ratio <= t[i].Ratio {
			a, b := t[i-1], t[i]
			f := (ratio - a.Ratio) / (b.Ratio - a.Ratio)
			return Interp(a.Curve, cos)*(1-f) + Interp(b.Curve, cos)*f
		}
	}
	return Interp(t[len(t)-1].Curve, cos)
}

// Point is one (x, y) node of a curve.
type Point struct{ X, Y float64 }

// Interp reads a curve linearly in x and clamps at its ends.
func Interp(c []Point, x float64) float64 {
	if len(c) == 0 {
		return 0
	}
	if x <= c[0].X {
		return c[0].Y
	}
	for i := 1; i < len(c); i++ {
		if x <= c[i].X {
			a, b := c[i-1], c[i]
			if b.X == a.X {
				return b.Y
			}
			return a.Y + (b.Y-a.Y)*(x-a.X)/(b.X-a.X)
		}
	}
	return c[len(c)-1].Y
}

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
	SeatChange           float64 // s for a crew member to take the gunner's seat, with the best crew skill
	StockLoad            float64 // reload of a stock loader over the best one

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

// Blast gives radii (m), penetration (mm) and damage against the TNT mass in kg.
type Blast struct{ Inner, Outer, Pen, Damage []Point }

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
