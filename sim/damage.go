package sim

import (
	"maps"
	"math"
	"slices"
	"strings"

	"shotdb/dm"
)

// fate is what the damage of one shot does to the vehicle.
type fate struct {
	destroyed bool
	interior  bool // a module inside the vehicle took damage
}

// damage is the state of the target's parts between shots. A part not in hp has its full hp.
type damage struct {
	hp   map[int32]float64
	dead map[int32]bool
}

func newDamage() damage { return damage{hp: map[int32]float64{}, dead: map[int32]bool{}} }

func (d damage) clone() damage { return damage{hp: maps.Clone(d.hp), dead: maps.Clone(d.dead)} }

// crewHit tells whether a crew part lost hp or died.
func (t *target) crewHit(d damage) bool {
	for _, i := range t.crew {
		if h, ok := d.hp[i]; d.dead[i] || ok && h < t.parts[i].HP {
			return true
		}
	}
	return false
}

// silenced tells whether the target cannot fire its main gun back, and for how
// long in seconds: +Inf when no crew member can fix it. A broken barrel or
// breech on every mount, or a dead turret drive, stops the gun. A dead gunner
// stops it when no live crew part has the override, until a substitute takes the seat.
func (t *target) silenced(d damage, tables *dm.Tables) (bool, float64) {
	g := t.v.Gun
	if g == nil {
		return false, 0
	}
	dead := func(name string) bool {
		i := t.index(name)
		return i >= 0 && d.dead[i]
	}
	// A drive with many parts stops when each of its parts on the model is dead.
	drive := func(names []string) bool {
		on := 0
		for _, n := range names {
			if t.index(n) < 0 {
				continue
			}
			if !dead(n) {
				return false
			}
			on++
		}
		return on > 0
	}
	out := len(g.Barrels) > 0
	for k := range g.Barrels {
		if !(dead(g.Barrels[k]) && !g.BrokenBarrelShoots || dead(g.Breeches[k])) {
			out = false
		}
	}
	if out || drive(g.HorDrive) || drive(g.VerDrive) {
		return true, math.Inf(1)
	}
	if g.Gunner == "" || !dead(g.Gunner) || g.Override != "" && t.index(g.Override) >= 0 && !dead(g.Override) {
		return false, 0
	}
	for _, sub := range g.Substitutes {
		if t.index(sub) >= 0 && !dead(sub) {
			return true, tables.SeatChange * g.ChangeMult
		}
	}
	return true, math.Inf(1)
}

// resolve applies the shot's damage messages in order: hp, the onHit rule
// (the highest damage value below the hit, of a matching type), the onKill
// rule when a part dies, then the kill conditions: an ammo explosion or fatal
// ammo fire, a fuel explosion, too few crew alive.
func (s *shot) resolve(d damage) fate {
	var f fate
	t := s.t
	hp, dead := d.hp, d.dead
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
		if kind == "shatter" && !slices.ContainsFunc(p.OnHit, func(r dm.Rule) bool { return slices.Contains(r.Types, "shatter") }) {
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
