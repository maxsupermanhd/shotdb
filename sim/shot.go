package sim

import (
	"math"
	"math/rand/v2"

	"shotdb/game"
	"shotdb/geom"
)

const (
	minCos      = 0.05
	ricochetGap = 0.01 // m: a part this close behind the last one rolls no ricochet
	maxBounces  = 2
	traceLength = 30.0 // m traced after a ricochet
	minBurst    = 0.01 // m
	jetSigma    = 0.3  // the jet's start draw is a normal of this sigma, cut at +-1
	burstKeep   = 0.7  // share of speed an APHE body keeps through its burst
	rayCap      = 8    // fragment rays traced per cone section or warhead segment
)

// shell is a round with its values at the range.
type shell struct {
	*game.Shell
	speed float64 // at the target
	pen   float64 // flat penetration of the body (or the jet)
	body  float64 // flat penetration of the kinetic body
}

// message is one damage message to a part.
type message struct {
	part   int32
	kind   string
	amount float64 // after the part's multiplier
	fire   float64 // chance multiplier for fire
}

// shot is one simulated shot.
type shot struct {
	d    *game.Data
	t    *target
	sh   *shell
	rng  *rand.Rand
	hits []geom.Hit // scratch of every trace
	leaf []span     // scratch of the traces that start no other: fragments and splash

	msgs     []message
	broke    bool // the core broke up
	ricochet bool
	// First armour plate met, and its charge, for the summary.
	first       int32
	firstCharge float64
}

// fragment is one fragment ray with its penetration along the ray and its damage.
type fragment struct {
	o, d       vec
	rng        float64
	pen0, pen1 float64 // at the origin and at the range
	dmg0, dmg1 float64 // damage with little and with much penetration left
	kind       string
	weight     float64 // how many fragments the ray stands for
	fire       float64
	group      bool  // the fragments hitting one part make one message, else one each
	from       int32 // the part that threw it, -1 for a warhead
}

func (s *shot) disperse(pen float64) float64 {
	return pen * (1 + (s.rng.Float64()*2-1)*s.d.PierceDispersion/2)
}

func (s *shot) chance(p float64) bool { return p > 0 && s.rng.Float64() < p }

// meshCharged tells whether a crossing is charged by its chord.
func (s *shot) meshCharged(i int32, sp span) bool {
	return s.t.solid[i] && !s.t.parts[i].IgnoreSolid && (sp.out-sp.in)*1000 > 0.05
}

// exit is where a body leaves a crossing. The game looks for an exit face only
// on solid nodes. A plain plate acts at its entry face: its node can be a closed
// shell (a wheel, a track, an add-on box) whose far face lies well behind it.
func (s *shot) exit(sp span) float64 {
	if s.meshCharged(sp.part, sp) {
		return sp.out
	}
	return sp.in
}

// charge is what a kinetic body pays for a crossing: the charge itself and the cost
// taken off the penetration.
func (s *shot) charge(i int32, sp span) (charge, cost float64) {
	p := s.t.parts[i]
	mech := s.sh.KineticType
	q := p.Q(mech)
	cos := math.Max(sp.cos, minCos)
	mesh := s.meshCharged(i, sp)
	thick := p.Thickness
	if mesh {
		thick = (sp.out - sp.in) * 1000 * cos
	}
	base := q * thick
	if base <= 0 {
		return 0, 0
	}
	if p.Module && !mesh {
		charge = base
	} else {
		charge = base * slope(s.d, s.sh.Shell, p, base, cos)
	}
	charge = math.Min(charge, p.Cap(mech))
	if mesh {
		return charge, charge
	}
	return charge, math.Max(charge, p.Through)
}

func (s *shot) ricochetChance(p *game.Part, sp span) float64 {
	if p.Module || !p.Ricochet {
		return 0
	}
	t, ok := s.d.Ricochet[s.sh.RicochetPreset]
	base := p.Q(s.sh.KineticType) * p.Thickness
	if !ok || base <= 0 {
		return 0
	}
	return t.At(sp.cos, 1000*s.sh.DmgCal/base)
}

// hit records a damage message; amount is before the part's multiplier.
func (s *shot) hit(i int32, kind string, amount, fire float64) {
	p := s.t.parts[i]
	m := p.Mult(kind)
	if kind == "pressure" {
		var ok bool
		if m, ok = p.DamageMult["pressure"]; !ok {
			return
		}
	}
	if a := amount * m; a > 0 {
		s.msgs = append(s.msgs, message{part: i, kind: kind, amount: a, fire: fire})
	}
}

func (s *shot) offset() float64 {
	if s.sh.ExplOffset >= 0 {
		return s.sh.ExplOffset
	}
	return s.d.ExplOffset
}

// face is the burst distance in front of a face at distance in.
func (s *shot) face(in, from float64) float64 {
	return math.Max(from+minBurst, in-s.offset())
}

// run simulates the shot along one line.
func (s *shot) run(l sample) {
	s.first = -1
	if s.sh.Cumulative > 0 && !s.sh.Rocket {
		s.heat(l.o, l.d, l.spans, 0)
	} else {
		s.kinetic(l.o, l.d, l.spans, s.sh.body, s.sh.speed, 0)
	}
}

// kinetic follows the shell body through the parts of a line.
func (s *shot) kinetic(o, d vec, spans []span, pen, speed float64, bounces int) {
	sh := s.sh
	pen = s.disperse(pen)
	prevExit := 0.0
	armed := math.Inf(1) // entry distance of the part that armed the fuse
	burst := false
	// The fuse of a rocket fires its warhead, and an APHE burst, at this distance.
	fuseAt := func() float64 { return armed + sh.FuseDelay }
	explode := func(at float64) {
		burst = true
		p := geom.Along(o, d, at)
		s.explode(p, d)
		if sh.Rocket && sh.Cumulative > 0 {
			s.jet(o, d, spans, at)
		}
	}
	for _, sp := range spans {
		i := sp.part
		p := s.t.parts[i]
		if sh.ExplMass > 0 && !burst && sp.out >= fuseAt() {
			// The delay ends in front of this part or inside it: the burst is in the gap, no deeper than the face.
			explode(math.Max(armed+minBurst, math.Min(fuseAt(), sp.in-s.offset())))
			// Only a body that keeps its integrity flies on, slower.
			if !sh.CheckIntegrity || sh.Rocket || s.chance(s.d.Integrity.At(sh.ExplMass*1000)) {
				return
			}
			kept := speedFor(s.d, sh.Shell, pen, sh.body, sh.speed) * burstKeep
			pen = kineticPen(s.d, sh.Shell, kept, Range)
		}
		gap := sp.in - prevExit
		prevExit = math.Max(prevExit, s.exit(sp))
		if s.broke && len(sh.BreakAir) > 0 {
			pen *= game.Interp(sh.BreakAir, gap)
		}
		charge, cost := s.charge(i, sp)
		if !p.Module && s.first < 0 && charge > 0 {
			s.first, s.firstCharge = i, charge
		}
		if gap > ricochetGap && s.chance(s.ricochetChance(p, sp)) {
			s.ricochet = true
			if bounces < maxBounces {
				s.bounce(o, d, sp, pen*game.Interp(s.d.RicochetPenKept, sp.cos), speed*s.d.RicochetSpeedMul, bounces)
			}
			return
		}
		if cost <= 0 {
			continue
		}
		if p.Module {
			v := speedFor(s.d, sh.Shell, pen, sh.body, sh.speed)
			amount := game.Interp(s.d.EnergyToDamage, 0.5*sh.Mass*v*v)
			if pen < charge {
				amount *= p.Restrain
			}
			s.hit(i, sh.KineticType, amount, 1)
		}
		if pen < cost {
			if sh.ExplMass > 0 && !burst {
				explode(s.face(sp.in, 0))
			}
			return
		}
		residual := pen - cost
		if sh.ExplMass > 0 && math.IsInf(armed, 1) && cost > sh.ExplThresh {
			armed = sp.in
		}
		if p.Spall && residual > 0 && cost >= s.d.FragThin && pen <= cost*s.d.FragPierce {
			s.cone(sh.Spall, o, d, sp, residual, cost)
		}
		if !p.Module && sh.BreakMax > 0 && !s.broke && speed > sh.BreakSpeed {
			thick := p.Thickness
			if sh.BreakLOS {
				thick /= math.Max(sp.cos, minCos)
			}
			if thick > sh.BreakMin && thick < sh.BreakMax {
				s.broke = true
				if len(sh.BreakScale) > 0 {
					residual *= game.Interp(sh.BreakScale, thick)
				}
			}
		}
		pen = s.disperse(residual)
	}
	if sh.ExplMass > 0 && !burst && !math.IsInf(armed, 1) {
		explode(fuseAt())
	}
}

// bounce sends the shell on in the mirror direction from a ricochet point.
func (s *shot) bounce(o, d vec, sp span, pen, speed float64, bounces int) {
	n := sp.n
	nd := geom.Normalize(geom.Sub(d, geom.Scale(n, 2*geom.Dot(d, n))))
	at := geom.Add(geom.Along(o, d, sp.in), geom.Scale(nd, 1e-3))
	spans := s.t.spans(at, nd, traceLength, &s.hits, nil)
	// The plate it bounced off is not met again on the way out.
	kept := spans[:0]
	for _, x := range spans {
		if x.part != sp.part {
			kept = append(kept, x)
		}
	}
	s.kinetic(at, nd, kept, pen, speed, bounces+1)
}

// heat follows a HEAT or HESH round: it flies to the first part thick enough
// for its fuse, may ricochet there, and fires its jet from in front of it.
func (s *shot) heat(o, d vec, spans []span, bounces int) {
	sh := s.sh
	for k, sp := range spans {
		p := s.t.parts[sp.part]
		if p.Thickness < sh.ExplThresh {
			continue
		}
		if !p.Module && s.first < 0 {
			s.first = sp.part
		}
		if k == 0 || sp.in-spans[k-1].out > ricochetGap {
			if s.chance(s.ricochetChance(p, sp)) {
				s.ricochet = true
				if bounces < maxBounces {
					n := sp.n
					nd := geom.Normalize(geom.Sub(d, geom.Scale(n, 2*geom.Dot(d, n))))
					at := geom.Add(geom.Along(o, d, sp.in), geom.Scale(nd, 1e-3))
					var kept []span
					for _, x := range s.t.spans(at, nd, traceLength, &s.hits, nil) {
						if x.part != sp.part {
							kept = append(kept, x)
						}
					}
					s.heat(at, nd, kept, bounces+1)
				}
				return
			}
		}
		at := s.face(sp.in, 0)
		if sh.ExplMass > 0 {
			s.explode(geom.Along(o, d, at), d)
		}
		s.jet(o, d, spans, at)
		return
	}
}

// jet traces the cumulative jet from distance start along the line.
func (s *shot) jet(o, d vec, spans []span, start float64) {
	sh := s.sh
	u := math.Max(-1, math.Min(1, s.rng.NormFloat64()*jetSigma))
	pen := sh.Cumulative * (1 + s.d.JetDispersion*u)
	end := start + sh.CumDist
	prev := start
	for _, sp := range spans {
		if sp.in < start {
			continue
		}
		if sp.in > end {
			return
		}
		i := sp.part
		p := s.t.parts[i]
		pen -= s.d.AirArmor * math.Max(0, sp.in-prev)
		if pen <= 0 {
			return
		}
		mesh := s.meshCharged(i, sp)
		q := p.Q("cumulative")
		var charge float64
		cos := math.Max(sp.cos, minCos)
		switch {
		case mesh && sh.ByNormal:
			charge = q * (sp.out - sp.in) * 1000 * cos
		case mesh:
			charge = q * (sp.out - sp.in) * 1000
		case sh.ByNormal || p.Module:
			charge = q * p.Thickness
		default:
			charge = q * p.Thickness / cos
		}
		charge = math.Min(charge, p.Cap("cumulative"))
		if charge <= 0 {
			continue
		}
		if !p.Module && s.firstCharge == 0 {
			s.firstCharge = charge
		}
		cost := charge
		if !mesh {
			cost = math.Max(charge, p.Through)
		}
		prev = math.Max(prev, s.exit(sp))
		if p.Module {
			s.hit(i, "cumulative", sh.CumDamage*pen/sh.Cumulative, 1)
		}
		if pen < cost {
			return
		}
		pen -= cost
		if p.Spall && sh.JetSpall != "" && pen > 0 {
			s.cone(sh.JetSpall, o, d, sp, pen, cost)
		}
	}
}

// cone throws the secondary fragments of a perforated part from its exit point.
// The part does not charge its own fragments.
func (s *shot) cone(preset string, o, d vec, sp span, residual, cost float64) {
	at := geom.Along(o, d, s.exit(sp))
	pr := s.d.Spall[preset]
	if pr == nil {
		pr = s.d.Spall["default"]
	}
	if pr == nil {
		return
	}
	axis := d
	if pr.ByNormal {
		axis = geom.Scale(sp.n, -1)
	}
	cal := s.sh.DmgCal * 1000
	var shellTotal, armorTotal float64
	if pr.Mass {
		shellTotal = pr.ShellCount.At(s.sh.DmgMass)
		armorTotal = pr.ArmorCount.At(24.19 * s.sh.DmgCal * s.sh.DmgCal * cost)
	}
	for _, sec := range pr.Sections {
		var count, penMul, dmgMul float64
		switch {
		case !pr.Mass:
			count = sec.Count * pr.Count.At(residual) * pr.Cal.At(cal/cost)
			penMul, dmgMul = pr.Pen.At(residual), pr.Dmg.At(residual)
		case sec.Shell:
			count = shellTotal * sec.Count
			penMul, dmgMul = pr.ShellPen.At(residual), pr.ShellDmg.At(residual)
		default:
			count = armorTotal * sec.Count
			penMul, dmgMul = pr.ArmorPen.At(residual), pr.ArmorDmg.At(residual)
		}
		s.throw(math.Round(count), axis, at, sec.Inner, sec.Outer, fragment{
			rng: sec.Distance, pen0: sec.Pen[1] * penMul, pen1: sec.Pen[0] * penMul,
			dmg0: sec.Dmg[0] * dmgMul, dmg1: sec.Dmg[1] * dmgMul,
			kind: "secondaryShatter", fire: sec.FireMult, group: sec.Aggregate, from: sp.part,
		})
	}
}

// throw sends count fragments in a cone band about axis, as at most rayCap
// rays. inner and outer of 0 and 180 make a sphere.
func (s *shot) throw(count float64, axis, at vec, inner, outer float64, f fragment) {
	if count <= 0 {
		return
	}
	rays := min(int(count), rayCap)
	f.weight = count / float64(rays)
	f.o = geom.Add(at, geom.Scale(axis, 1e-4))
	u := geom.Normalize(geom.Cross(axis, vec{0.3, 1, 0.1}))
	w := geom.Cross(axis, u)
	ci, co := math.Cos(inner*math.Pi/180), math.Cos(outer*math.Pi/180)
	var agg []partDamage
	for range rays {
		// Uniform in solid angle between the two cone angles.
		c := ci + (co-ci)*s.rng.Float64()
		sn := math.Sqrt(math.Max(0, 1-c*c))
		phi := 2 * math.Pi * s.rng.Float64()
		f.d = geom.Add(geom.Scale(axis, c), geom.Add(geom.Scale(u, sn*math.Cos(phi)), geom.Scale(w, sn*math.Sin(phi))))
		agg = s.fly(f, agg)
	}
	for _, a := range agg {
		if f.group {
			s.hit(a.part, f.kind, a.amount, f.fire)
			continue
		}
		// One message per fragment.
		n := min(64, max(1, int(math.Round(a.amount/f.dmg1))))
		for range n {
			s.hit(a.part, f.kind, a.amount/float64(n), f.fire)
		}
	}
}

// fly traces one fragment ray: each part costs its charge (at least its
// armorThrough), and the fragment stops where the cost is more than it has
// left. The damage to modules adds up in agg, in the order the parts are first hit.
func (s *shot) fly(f fragment, agg []partDamage) []partDamage {
	used := 0.0
	s.leaf = s.t.spans(f.o, f.d, f.rng, &s.hits, s.leaf[:0])
	for _, sp := range s.leaf {
		i := sp.part
		if i == f.from {
			continue
		}
		p := s.t.parts[i]
		mech := f.kind
		q := p.Q(mech)
		var charge float64
		switch {
		case s.meshCharged(i, sp):
			charge = q * (sp.out - sp.in) * 1000
		case p.Module:
			charge = q * p.Thickness
		default:
			charge = q * p.Thickness / math.Max(sp.cos, minCos)
		}
		charge = math.Min(charge, p.Cap(mech))
		cost := math.Max(charge, p.Through)
		pen := f.pen0 + (f.pen1-f.pen0)*math.Min(1, sp.in/f.rng)
		rem := pen - used
		if rem <= 0 {
			break
		}
		if p.Module {
			dmg := f.dmg1
			if lo, hi := f.pen0, f.pen1; hi > lo {
				x := math.Max(0, math.Min(1, (rem-lo)/(hi-lo)))
				dmg = f.dmg0 + (f.dmg1-f.dmg0)*x
			}
			if rem < charge {
				dmg *= p.Restrain
			}
			k := 0
			for k < len(agg) && agg[k].part != i {
				k++
			}
			if k == len(agg) {
				agg = append(agg, partDamage{part: i})
			}
			agg[k].amount += dmg * f.weight
		}
		used += cost
		if cost > rem {
			break
		}
	}
	return agg
}

type partDamage struct {
	part   int32
	amount float64
}

// explode bursts the filler at a point: splash, body fragments (real or
// statistical) and overpressure.
func (s *shot) explode(at, d vec) {
	sh := s.sh
	if sh.ExplMass <= 0 {
		return
	}
	before := len(s.msgs)
	tntB := sh.ExplMass * strength(s.d.Brisance, sh.ExplType)
	var class *game.ShatterClass
	for k := range s.d.Shatters {
		class = &s.d.Shatters[k]
		if class.Fill >= tntB/sh.Mass {
			break
		}
	}
	if class != nil {
		r, p, dm := game.Interp(class.Radius, tntB), game.Interp(class.Pen, tntB), game.Interp(class.Dmg, tntB)
		n := game.Interp(class.Cnt, sh.Mass-tntB)
		if sh.Shatter != nil {
			for _, g := range sh.Shatter.Segments {
				cnt := math.Round(n * sh.Shatter.CountPortion * g.Portion)
				s.throw(cnt, d, at, g.Inner, g.Outer, fragment{
					rng: r * g.RadiusScale, pen0: p * g.PenScale, pen1: p * g.PenScale,
					dmg0: dm * g.DmgScale, dmg1: dm * g.DmgScale, kind: "shatter", fire: 1, from: -1,
				})
			}
		} else {
			// The statistical shatters of APHE: fragments in every direction from the burst.
			s.throw(math.Round(n), d, at, 0, 180, fragment{rng: r, pen0: p, pen1: p, dmg0: dm, dmg1: dm, kind: "shatter", fire: 1, from: -1})
		}
	}

	tnt := sh.ExplMass * strength(s.d.Strength, sh.ExplType)
	sp := s.d.Splash
	inner, outer := game.Interp(sp.Inner, tnt), game.Interp(sp.Outer, tnt)
	pen, dmg := game.Interp(sp.Pen, tnt), game.Interp(sp.Damage, tnt)
	for i, p := range s.t.parts {
		if !p.Module {
			continue
		}
		if a := s.reach(at, int32(i), outer, inner, dmg, pen, "explosion"); a > 0 {
			s.hit(int32(i), "explosion", a, 1)
		}
	}

	if !sh.Pressure {
		return
	}
	reached := map[int32]bool{}
	for _, m := range s.msgs[before:] {
		reached[m.part] = true
	}
	for _, c := range []struct {
		parts []string
		blast game.Blast
		open  bool
	}{{s.t.v.Pressure, s.d.Closed, false}, {s.t.v.Open, s.d.Opened, true}} {
		var idx []int32
		hit := c.open
		for _, name := range c.parts {
			if i := s.t.index(name); i >= 0 {
				idx = append(idx, i)
				hit = hit || reached[i]
			}
		}
		if !hit {
			continue
		}
		in, out := game.Interp(c.blast.Inner, tnt), game.Interp(c.blast.Outer, tnt)
		pd, pp := game.Interp(c.blast.Damage, tnt), game.Interp(c.blast.Pen, tnt)
		for _, i := range idx {
			p := s.t.parts[i]
			if pp < s.d.SplashThreshold*p.Q("pressure")*p.Thickness {
				continue
			}
			if a := falloff(geom.Norm(geom.Sub(s.t.center[i], at)), in, out) * pd; a > 0 {
				s.hit(i, "pressure", a, 1)
			}
		}
	}
}

// reach is the splash damage a part takes from a burst: full up to the inner
// radius, then less to none at the outer one, when the armour on the way from
// the burst to the part's centre is less than the splash penetration. Plates
// on the way cost their line-of-sight thickness, solid parts their chord.
func (s *shot) reach(at vec, i int32, outer, inner, dmg, pen float64, mech string) float64 {
	c := s.t.center[i]
	dist := geom.Norm(geom.Sub(c, at))
	f := falloff(dist, inner, outer)
	if f <= 0 {
		return 0
	}
	p := s.t.parts[i]
	if pen < s.d.SplashThreshold*p.Q(mech)*p.Thickness {
		return 0
	}
	if dist > 1e-6 {
		armour := 0.0
		s.leaf = s.t.spans(at, geom.Scale(geom.Sub(c, at), 1/dist), dist, &s.hits, s.leaf[:0])
		for _, sp := range s.leaf {
			q := s.t.parts[sp.part]
			if sp.part == i || q.Module {
				continue
			}
			thick := q.Thickness / math.Max(sp.cos, minCos)
			if s.meshCharged(sp.part, sp) {
				thick = (sp.out - sp.in) * 1000
			}
			if armour += math.Min(q.Q(mech)*thick, q.Cap(mech)); armour >= pen {
				return 0
			}
		}
	}
	return f * dmg
}

func falloff(dist, inner, outer float64) float64 {
	switch {
	case dist <= inner:
		return 1
	case dist >= outer:
		return 0
	}
	return (outer - dist) / (outer - inner)
}

func strength(m map[string]float64, kind string) float64 {
	if s, ok := m[kind]; ok {
		return s
	}
	return 1
}
