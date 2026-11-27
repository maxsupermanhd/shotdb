// Package dm holds the damage model data that the shot simulation reads: the
// vehicles, their parts and shells, and the global tables. It has no loaders,
// so the compute build does not link the game file readers.
package dm

import (
	"fmt"
	"math"
	"strings"

	"shotdb/geom"
)

// Unit is one ground vehicle of the research trees.
type Unit struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Role    string  `json:"role"`
	Rank    int     `json:"rank"`
	BR      float64 `json:"br"`
}

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

	Hidden map[string]bool // collision nodes not on the stock vehicle

	Gun *Gun // the main gun, nil if none
}

// Gun is what the target needs to fire its main gun back: the parts that stop it.
type Gun struct {
	Barrels, Breeches  []string // by mount: barrelDP and breechDP
	BrokenBarrelShoots bool     // brokenBarrelAllowShoot
	HorDrive, VerDrive []string // horDriveDm and verDriveDm
	Gunner             string   // gunnerDm
	Override           string   // secondGunnerDm: a second crew part that can fire the gun
	Substitutes        []string // crew parts that can take the gunner's seat
	ChangeMult         float64  // tank_crew.changeTimeMult
}

// DamageNodes keeps the collision nodes that are damage parts of the stock
// vehicle, named without the _dm suffix.
func (v *Vehicle) DamageNodes(nodes []geom.Node) ([]geom.Node, error) {
	var out []geom.Node
	for _, n := range nodes {
		name, ok := strings.CutSuffix(n.Name, "_dm")
		if !ok || v.Hidden[name] || v.Parts[name] == nil || len(n.Tris) == 0 {
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
	Types   []string // damage types; none matches every type
	Not     []string // notDamageType
	Damage  float64
	Effects []Effect // sorted by name
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
	Guided  bool

	// Fire rate of the weapon that fires the round.
	ShotFreq   float64   // shots/s of one barrel, with the best loader
	Barrels    []float64 // timeOffset of each barrel, s
	Cartridge  int       // bulletsCartridge: shots of one barrel between magazine reloads
	MagReload  float64   // reloadTime of the magazine, s
	AutoLoader bool      // the loader skill does not change the reload

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
