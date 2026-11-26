package sim

import (
	"math"
	"slices"
	"strings"

	"shotdb/game"
)

// fate is what the damage of one shot does to the vehicle.
type fate struct {
	destroyed bool
	interior  bool // a module inside the vehicle took damage
}

// resolve applies the shot's damage messages in order: hp, the onHit rule
// (the highest damage value below the hit, of a matching type), the onKill
// rule when a part dies, then the kill conditions: an ammo explosion or fatal
// ammo fire, a fuel explosion, too few crew alive.
func (s *shot) resolve() fate {
	var f fate
	t := s.t
	hp := map[int32]float64{}
	dead := map[int32]bool{}
	state := func(i int32) float64 {
		if h, ok := hp[i]; ok {
			return h
		}
		return t.parts[i].HP
	}
	effect := func(i int32, name string) {
		p := t.parts[i]
		switch name {
		case "expl", "full_expl":
			if rack, ok := t.v.Racks[p.Name]; ok {
				switch {
				case t.v.RackExplosive && !rack.Charges && rack.FatalExplosion:
					f.destroyed = true
				case !t.v.RackExplosive || rack.Charges:
					f.destroyed = f.destroyed || rack.FatalFire
				}
			} else if strings.HasPrefix(p.Name, "fuel_tank") && p.Group != "fuel_tanks_exterior" {
				// An exterior tank burns and bursts outside the hull: the game gives it its own splash table.
				f.destroyed = true
			}
		case "fire":
			if rack, ok := t.v.Racks[p.Name]; ok && rack.FatalFire {
				f.destroyed = true
			}
		default:
			if j := t.index(name); j >= 0 {
				dead[j] = true
			}
		}
	}
	for _, m := range s.msgs {
		p := t.parts[m.part]
		if dead[m.part] || p.HP <= 0 {
			continue
		}
		// The gun, its barrel and the sights are hit from outside, with no hole in the armour.
		if g := p.Group; !(g == "gun" || g == "optics" || g == "commander_panoramic_sight" || strings.HasPrefix(p.Name, "gun_barrel")) {
			f.interior = true
		}
		left := math.Max(0, state(m.part)-m.amount)
		hp[m.part] = left
		gate := math.Min(1, (1-left/p.HP)/math.Max(s.d.EffectThreshold, 1e-9))
		// Warhead fragments take the explosion rules of a part that has none for shatter.
		kind := m.kind
		if kind == "shatter" && !slices.ContainsFunc(p.OnHit, func(r game.Rule) bool { return slices.Contains(r.Types, "shatter") }) {
			kind = "explosion"
		}
		for _, r := range p.OnHit {
			if !r.Matches(kind) || r.Damage >= m.amount {
				continue
			}
			for _, e := range r.Effects {
				chance := e.P
				if e.Name == "fire" {
					chance *= m.fire
				}
				if s.chance(chance * gate) {
					effect(m.part, e.Name)
				}
			}
			break
		}
		if left > 0 {
			continue
		}
		dead[m.part] = true
		for _, r := range p.OnKill {
			if !r.Matches(kind) {
				continue
			}
			sum := 0.0
			for _, e := range r.Effects {
				sum += e.P
			}
			x := s.rng.Float64() * sum
			for _, e := range r.Effects {
				if x -= e.P; x < 0 {
					effect(m.part, e.Name)
					break
				}
			}
			break
		}
	}
	alive, total := 0, 0
	for k, i := range t.crew {
		total += t.count[k]
		if !dead[i] {
			alive += t.count[k]
		}
	}
	if total > 0 && alive < min(s.t.v.MinCrew, total) {
		f.destroyed = true
	}
	if i := t.index("body"); i >= 0 && dead[i] {
		f.destroyed = true
	}
	return f
}
