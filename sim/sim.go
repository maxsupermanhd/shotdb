// Package sim decides shot results of one vehicle against another, with a
// 3D shot simulation on the target's collision model. See README.md, "What
// the simulation does", for what is taken from the game and what is not.
package sim

import (
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"shotdb/game"
	"shotdb/geom"
)

// Simulator gives the result of a shooter against a target.
type Simulator interface {
	Simulate(shooter, target string) (Result, error)
}

const Range = 500.0 // m

// Views in result order: their weight in the score and the shooter's bearings
// from the target's nose, in degrees.
var Views = []struct {
	Name     string
	Weight   float64
	Bearings []float64
}{
	{"front", 0.5, []float64{-20, 0, 20}},
	{"side", 0.3, []float64{-100, -80, 80, 100}},
	{"rear", 0.2, []float64{160, 180, 200}},
}

type Outcome string

const (
	Kill     Outcome = "kill"
	Pen      Outcome = "pen"
	Shatter  Outcome = "shatter"
	Ricochet Outcome = "ricochet"
	NonPen   Outcome = "nonpen"
)

type Result struct {
	Shooter string       `json:"shooter"`
	Target  string       `json:"target"`
	Shell   *ShellInfo   `json:"shell"` // nil when the shooter has no usable gun
	Views   []ViewResult `json:"views"`
	Score   float64      `json:"score"`
	Ms      float64      `json:"ms"`    // time spent, all shells
	Shots   int          `json:"shots"` // shots simulated, all shells
}

type ShellInfo struct {
	Name  string  `json:"name"`
	Type  string  `json:"type"`
	PenMm float64 `json:"pen"` // flat penetration at Range
}

type ViewResult struct {
	Name   string          `json:"name"`
	Counts map[Outcome]int `json:"counts"`
	Score  float64         `json:"score"`
	Plates []PlateInfo     `json:"plates"` // the armour parts met first most often
}

// PlateInfo is an armour part the shell met first: in how many of the view's
// shots, and its mean charge (effective thickness) against the shell.
type PlateInfo struct {
	Name  string  `json:"name"`
	Mm    float64 `json:"mm"`
	Shots int     `json:"shots"`
}

// Sim is the Simulator over loaded game data. The last targets prepared are kept.
type Sim struct {
	Data *game.Data

	mu      sync.Mutex
	targets []*target // the most recent last
}

// keptTargets bounds the memory: a target's mesh takes a few MB.
const keptTargets = 16

func (s *Sim) target(v *game.Vehicle) (*target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.targets {
		if t.v == v {
			s.targets = append(append(s.targets[:i:i], s.targets[i+1:]...), t)
			return t, nil
		}
	}
	nodes, err := s.Data.Nodes(v)
	if err != nil {
		return nil, err
	}
	t := newTarget(v, nodes)
	if len(s.targets) >= keptTargets {
		s.targets = s.targets[1:]
	}
	s.targets = append(s.targets, t)
	return t, nil
}

func (s *Sim) Simulate(shooter, target string) (Result, error) {
	start := time.Now()
	a, err := s.Data.Vehicle(shooter)
	if err != nil {
		return Result{}, err
	}
	v, err := s.Data.Vehicle(target)
	if err != nil {
		return Result{}, err
	}
	t, err := s.target(v)
	if err != nil {
		return Result{}, err
	}
	best := Result{}
	bestPen := -1.0
	shots := 0
	for k := range a.Shells {
		r := s.shoot(&a.Shells[k], t)
		shots += r.Shots
		if best.Shell == nil || r.Score > best.Score || (r.Score == best.Score && r.Shell.PenMm > bestPen) {
			best, bestPen = r, r.Shell.PenMm
		}
	}
	best.Shooter, best.Target, best.Shots = a.ID, v.ID, shots
	if best.Shell == nil {
		for _, view := range Views {
			best.Views = append(best.Views, ViewResult{Name: view.Name, Counts: map[Outcome]int{}})
		}
	}
	best.Ms = math.Round(float64(time.Since(start).Microseconds())/100) / 10
	return best, nil
}

// shoot fires one shell along every shot line of every view.
func (s *Sim) shoot(g *game.Shell, t *target) Result {
	d := s.Data
	sh := &shell{Shell: g, speed: impactSpeed(g, Range)}
	sh.body = kineticPen(d, g, sh.speed, Range)
	sh.pen = sh.body
	if g.Cumulative > 0 {
		sh.pen = g.Cumulative
	}
	h := fnv.New64a()
	h.Write([]byte(g.Name + "/" + g.Type + "/" + t.v.ID))
	seed := h.Sum64()

	var hits []geom.Hit
	var leaf []span
	r := Result{Shell: &ShellInfo{Name: g.Label, Type: g.Type, PenMm: math.Round(sh.pen*10) / 10}}
	for vi, view := range Views {
		vr := ViewResult{Name: view.Name, Counts: map[Outcome]int{}}
		first := map[int32]*PlateInfo{}
		lines := t.views[vi]
		for li, l := range lines {
			x := &shot{d: d, t: t, sh: sh, rng: rand.New(rand.NewPCG(seed, uint64(vi<<16|li))), hits: hits, leaf: leaf}
			x.run(l)
			hits, leaf = x.hits, x.leaf
			f := x.resolve()
			var o Outcome
			switch {
			case f.destroyed:
				o = Kill
			case f.interior && x.broke:
				o = Shatter
			case f.interior:
				o = Pen
			case x.ricochet:
				o = Ricochet
			default:
				o = NonPen
			}
			vr.Counts[o]++
			r.Shots++
			if x.first >= 0 {
				p := first[x.first]
				if p == nil {
					p = &PlateInfo{Name: t.parts[x.first].Name}
					first[x.first] = p
				}
				p.Shots++
				p.Mm += x.firstCharge
			}
		}
		for _, p := range first {
			p.Mm = math.Round(p.Mm / float64(p.Shots))
			vr.Plates = append(vr.Plates, *p)
		}
		sort.Slice(vr.Plates, func(i, j int) bool {
			a, b := vr.Plates[i], vr.Plates[j]
			return a.Shots > b.Shots || a.Shots == b.Shots && a.Name < b.Name
		})
		vr.Plates = vr.Plates[:min(3, len(vr.Plates))]
		if n := len(lines); n > 0 {
			vr.Score = (float64(vr.Counts[Kill]) + 0.4*float64(vr.Counts[Pen]+vr.Counts[Shatter])) / float64(n)
		}
		r.Views = append(r.Views, vr)
		r.Score += view.Weight * vr.Score
	}
	return r
}
