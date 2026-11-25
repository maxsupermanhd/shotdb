package sim

import (
	"math"

	"shotdb/game"
)

const airDensity = 1.225

// impactSpeed applies the game's drag law over a distance.
func impactSpeed(sh *game.Shell, dist float64) float64 {
	if sh.Rocket || sh.Mass <= 0 || sh.Cx <= 0 {
		return sh.Speed
	}
	k := airDensity * sh.Cx * math.Pi * sh.BalCal * sh.BalCal / (8 * sh.Mass)
	return sh.Speed * math.Exp(-k*dist)
}

// kineticPen is the flat penetration of the shell body at an impact speed.
// Rounds with neither formula read their armorpower table at the range.
func kineticPen(d *game.Data, sh *game.Shell, speed, rangeM float64) float64 {
	switch {
	case sh.LanzOd != nil:
		a, b := lanzOdermatt(sh)
		v := speed / 1000
		return a * math.Exp(b/(v*v))
	case sh.DeMarre != nil:
		return deMarreC(d, sh) * math.Pow(speed/d.ArmorResistance, sh.DeMarre.SpeedPow)
	case len(sh.ArmorPower) > 0:
		return game.Interp(sh.ArmorPower, rangeM)
	}
	return 0
}

func lanzOdermatt(sh *game.Shell) (a, b float64) {
	lo := sh.LanzOd
	dmm := sh.DmgCal * 1000
	mul := 1.104
	b = math.Pow(lo.Hardness, -0.2342) * -73013.0625 / lo.Density
	switch lo.Material {
	case "depletedUranium":
		mul, b = 0.825, -17660.76/lo.Density
	case "tungsten":
		mul, b = 0.994, -24965.201171875/lo.Density
	}
	a = mul * lo.Length * math.Sqrt(lo.Density/7850) / math.Tanh(0.0656*(lo.Length/dmm)+0.283)
	return a, b
}

// deMarreC is the De Marre coefficient, with the filler modifier of APHE.
func deMarreC(d *game.Data, sh *game.Shell) float64 {
	dm := sh.DeMarre
	c := 100 * dm.K * math.Pow(sh.DmgMass, dm.MassPow) * math.Pow(sh.DmgCal*1000*0.01, -dm.CaliberPow)
	if sh.Mass > 0 && sh.ExplMass > 0 && len(d.FillerPenMod) > 0 {
		c *= game.Interp(d.FillerPenMod, sh.ExplMass/sh.Mass)
	}
	return c
}

// speedFor inverts the penetration curve: the speed at which the body has pen.
// Rounds without a speed curve scale the impact speed by the penetration share.
func speedFor(d *game.Data, sh *game.Shell, pen, flat, speed float64) float64 {
	if pen <= 0 {
		return 0
	}
	switch {
	case sh.LanzOd != nil:
		a, b := lanzOdermatt(sh)
		if pen >= a {
			return speed
		}
		return 1000 * math.Sqrt(b/math.Log(pen/a))
	case sh.DeMarre != nil:
		return d.ArmorResistance * math.Pow(pen/deMarreC(d, sh), 1/sh.DeMarre.SpeedPow)
	}
	if flat <= 0 {
		return 0
	}
	return speed * math.Min(1, pen/flat)
}

// slope is the S factor of the shell's slope preset, with the game's breach
// rule and scale cap. Without a preset it is 1/cos. base is quality x thickness.
func slope(d *game.Data, sh *game.Shell, p *game.Part, base, cos float64, mesh bool) float64 {
	cal := sh.DmgCal * 1000
	if base > 0 && d.ArmorBreachK*base <= cal {
		if mesh {
			return 1
		}
		return math.Min(1/cos, d.MaxArmorScale)
	}
	t, ok := d.Slope[sh.SlopePreset]
	if !ok || base <= 0 || cal <= p.SlopeMin {
		return math.Min(1/cos, d.MaxArmorScale)
	}
	inv := t.At(cos, cal/base)
	if inv <= 0 {
		return d.MaxArmorScale
	}
	return math.Min(1/inv, d.MaxArmorScale)
}
