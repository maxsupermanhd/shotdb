// Package sim decides shot results of one vehicle against another, with a
// 3D shot simulation on the target's collision model. See README.md, "What
// the simulation does", for what is taken from the game and what is not.
package sim

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"time"

	"shotdb/dm"
	"shotdb/geom"
)

const Range = 500.0 // m

// Views in result order, with their weight in the score. Sample gives the
// bearings of each view.
var Views = []struct {
	Name   string
	Weight float64
}{
	{"front", 0.5},
	{"side", 0.3},
	{"rear", 0.2},
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
	Ms      float64      `json:"ms"`              // time spent, all shells
	Shots   int          `json:"shots"`           // shots simulated, all shells
	Extra   int          `json:"extra,omitempty"` // follow-up and pair shots simulated, all shells
	// With round -1: the result of each round, by its index. The page keeps
	// them to show a single round later without a new simulation.
	Each []RoundResult `json:"each,omitempty"`
}

// RoundResult is the result of one round when Simulate fires all rounds.
// The same round fired alone gives the same views, score and shots.
type RoundResult struct {
	Views []ViewResult `json:"views"`
	Score float64      `json:"score"`
	Ms    float64      `json:"ms"`
	Shots int          `json:"shots"`
	Extra int          `json:"extra,omitempty"`
}

// ShellInfo is a round of the shooter. Index is its place in Rounds.
type ShellInfo struct {
	Index     int     `json:"index"`
	Name      string  `json:"name"`
	Gun       string  `json:"gun"`
	Type      string  `json:"type"`
	CaliberMm float64 `json:"caliber"`
	MassKg    float64 `json:"mass"`
	Speed     float64 `json:"speed"` // m/s at the muzzle
	PenMm     float64 `json:"pen"`   // flat penetration at Range
	FillerKg  float64 `json:"filler"`
	Explosive string  `json:"explosive"`
	// Follow-up shots after the first on the same line: their count and the
	// time of the last one, and the time of the third shot. Zero for a guided round.
	FollowUps int     `json:"followUps,omitempty"`
	FollowSec float64 `json:"followSec,omitempty"`
	ThirdSec  float64 `json:"thirdSec,omitempty"`
}

type ViewResult struct {
	Name   string          `json:"name"`
	Counts map[Outcome]int `json:"counts"`
	Score  float64         `json:"score"`
	Plates []PlateInfo     `json:"plates"` // the armour parts met first most often
	Follow *Follow         `json:"follow,omitempty"`
}

// Follow counts the shots after the first in a view. See README.md, "Follow-up shots".
type Follow struct {
	Lines   int     `json:"lines,omitempty"`   // first shots that penetrate, damage crew and do not kill
	Kills   int     `json:"kills,omitempty"`   // of those, lines that a follow-up kills
	Shots   int     `json:"shots,omitempty"`   // sum over the kills of the follow-ups fired
	Seconds float64 `json:"seconds,omitempty"` // sum over the kills of the time to the kill

	Pairs     int `json:"pairs,omitempty"`     // two-shot combinations in one direction
	PairKills int `json:"pairKills,omitempty"` // of those, combinations that kill

	Silenced   int `json:"silenced,omitempty"`   // first shots after which the target cannot fire back
	Third      int `json:"third,omitempty"`      // of those, lines with a third shot before the target fires
	ThirdKills int `json:"thirdKills,omitempty"` // of those, lines killed by the second or third shot
}

// PlateInfo is an armour part the shell met first: in how many of the view's
// shots, and its mean charge (effective thickness) against the shell.
type PlateInfo struct {
	Name  string  `json:"name"`
	Mm    float64 `json:"mm"`
	Shots int     `json:"shots"`
}

// Options are the model choices that the user can change. DefaultOptions is the
// model that README.md gives. See README.md, "Model settings".
type Options struct {
	FollowUps  bool    `json:"followUps"`
	Pairs      bool    `json:"pairs"`
	ReturnFire bool    `json:"returnFire"`
	Window     float64 `json:"window"`     // s after the first shot in which the follow-ups fall
	Stock      bool    `json:"stock"`      // a manual loader takes Tables.StockLoad times the best reload
	LastRound  bool    `json:"lastRound"`  // the first shot fires the last round of the magazine
	SeatChange float64 `json:"seatChange"` // s to take the gunner's seat; less than 0 for the game's value
	Override   bool    `json:"override"`   // a live secondGunnerDm crew part fires the gun for a dead gunner
	Drives     bool    `json:"drives"`     // a dead turret drive stops the gun
	Outside    bool    `json:"outside"`    // a module that the shell hits before it goes through a plate makes a pen
}

var DefaultOptions = Options{FollowUps: true, Pairs: true, ReturnFire: true, Window: 1.5, SeatChange: -1, Override: true, Drives: true, Outside: true}

// MaxWindow and MaxSeatChange are the largest Window and SeatChange, s.
const MaxWindow, MaxSeatChange = 10.0, 60.0

// ParseOptions reads options JSON over DefaultOptions.
func ParseOptions(b []byte) (Options, error) {
	o := DefaultOptions
	if err := json.Unmarshal(b, &o); err != nil {
		return o, fmt.Errorf("options: %w", err)
	}
	return o, o.Check()
}

// Check tells whether the times are in range.
func (o Options) Check() error {
	if o.Window < 0 || o.Window > MaxWindow {
		return fmt.Errorf("options: window %g s is not in 0 to %g s", o.Window, MaxWindow)
	}
	if o.SeatChange > MaxSeatChange {
		return fmt.Errorf("options: seat change %g s is more than %g s", o.SeatChange, MaxSeatChange)
	}
	return nil
}

// Rounds lists the shooter's rounds at the range.
func Rounds(d *dm.Tables, a *dm.Vehicle, o Options) []ShellInfo {
	out := make([]ShellInfo, len(a.Shells))
	for k := range a.Shells {
		out[k] = newShell(d, &a.Shells[k], o).info(k)
	}
	return out
}

func newShell(d *dm.Tables, g *dm.Shell, o Options) *shell {
	sh := &shell{Shell: g, speed: impactSpeed(g, Range)}
	sh.times, sh.follows = followTimes(d, g, o)
	sh.body = kineticPen(d, g, sh.speed, Range)
	sh.pen = sh.body
	if g.Cumulative > 0 {
		sh.pen = g.Cumulative
	}
	return sh
}

func (sh *shell) info(index int) ShellInfo {
	g := sh.Shell
	in := ShellInfo{
		Index: index, Name: g.Label, Gun: g.Gun, Type: g.Type,
		CaliberMm: math.Round(g.Caliber*10000) / 10, MassKg: math.Round(g.Mass*1000) / 1000, Speed: math.Round(g.Speed),
		PenMm: math.Round(sh.pen*10) / 10, FillerKg: math.Round(g.ExplMass*10000) / 10000, Explosive: g.ExplType,
	}
	if n := sh.follows; n > 0 {
		in.FollowUps, in.FollowSec, in.ThirdSec = n, math.Round(sh.times[n-1]*1000)/1000, math.Round(sh.times[1]*1000)/1000
	}
	return in
}

// followTimes gives the times after the first shot of the next shots on the
// same line, and how many of them are follow-ups: the shots in o.Window, at
// least 1. It gives at least two times. Each barrel fires at its offset, then
// each 1/ShotFreq; after Cartridge shots it waits for the magazine. The
// magazine is full at the first shot, or holds only that round with o.LastRound.
func followTimes(d *dm.Tables, g *dm.Shell, o Options) ([]float64, int) {
	if g.ShotFreq <= 0 || g.Guided {
		return nil, 0
	}
	gap, reload := 1/g.ShotFreq, g.MagReload
	// A manual loader loads each round of a single-shot gun, and each magazine of a magazine gun.
	if o.Stock && !g.AutoLoader && d.StockLoad > 0 {
		if g.Cartridge > 1 {
			reload *= d.StockLoad
		} else {
			gap *= d.StockLoad
		}
	}
	fired := 0 // rounds of each magazine fired before the first shot
	if o.LastRound && g.Cartridge > 1 {
		fired = g.Cartridge - 1
	}
	offsets := g.Barrels
	if len(offsets) == 0 {
		offsets = []float64{0}
	}
	base := slices.Min(offsets)
	var times []float64
	for _, off := range offsets {
		at := off - base
		for k := 0; k < 3 || at <= o.Window; k++ {
			if k > 0 {
				at += gap
				if g.Cartridge > 1 && (k+fired)%g.Cartridge == 0 {
					at += math.Max(gap, reload) - gap
				}
			}
			times = append(times, at)
		}
	}
	slices.Sort(times)
	times = times[1:]
	n := 0
	for n < len(times) && times[n] <= o.Window {
		n++
	}
	n = max(n, 1)
	return times[:max(n, 2)], n
}

// Simulate fires the shooter a at the target v, on the target's collision
// nodes, along the shot lines of s. With round -1 it fires every round of the
// shooter and keeps the best, else only that round.
// With round -1 the result also holds the result of each round in Each.
func Simulate(d *dm.Tables, a, v *dm.Vehicle, nodes []geom.Node, round int, s Sample, o Options) (Result, error) {
	start := time.Now()
	if round >= len(a.Shells) {
		return Result{}, fmt.Errorf("%s has no round %d", a.ID, round)
	}
	if len(s.Bearings) != len(Views) {
		return Result{}, fmt.Errorf("the sample has %d views, not %d", len(s.Bearings), len(Views))
	}
	t := newTarget(v, nodes, s)
	best := Result{}
	bestPen := -1.0
	shots, extra := 0, 0
	var each []RoundResult
	for k := range a.Shells {
		if round >= 0 && k != round {
			continue
		}
		at := time.Now()
		r := shoot(d, k, &a.Shells[k], t, o)
		shots += r.Shots
		extra += r.Extra
		if round < 0 {
			each = append(each, RoundResult{Views: r.Views, Score: r.Score, Shots: r.Shots, Extra: r.Extra, Ms: math.Round(float64(time.Since(at).Microseconds())/100) / 10})
		}
		if best.Shell == nil || r.Score > best.Score || (r.Score == best.Score && r.Shell.PenMm > bestPen) {
			best, bestPen = r, r.Shell.PenMm
		}
	}
	best.Shooter, best.Target, best.Shots, best.Extra, best.Each = a.ID, v.ID, shots, extra, each
	if best.Shell == nil {
		for _, view := range Views {
			best.Views = append(best.Views, ViewResult{Name: view.Name, Counts: map[Outcome]int{}})
		}
	}
	best.Ms = math.Round(float64(time.Since(start).Microseconds())/100) / 10
	return best, nil
}

// shoot fires one shell along every shot line of every view.
func shoot(d *dm.Tables, index int, g *dm.Shell, t *target, o Options) Result {
	sh := newShell(d, g, o)
	// FNV-1a of the round and the target.
	seed := uint64(14695981039346656037)
	for _, c := range []byte(g.Name + "/" + g.Type + "/" + t.v.ID) {
		seed = (seed ^ uint64(c)) * 1099511628211
	}

	var hits []geom.Hit
	var leaf []span
	info := sh.info(index)
	r := Result{Shell: &info}
	for vi, view := range Views {
		vr := ViewResult{Name: view.Name, Counts: map[Outcome]int{}}
		var fl Follow
		first := map[int32]*PlateInfo{}
		lines := t.views[vi]
		alone := make([]bool, len(lines))   // the first shot kills
		after := make([]damage, len(lines)) // the state after a first shot that passes the gate
		// next fires one more shot along line l on the state dmg and tells whether it kills.
		next := func(l sample, stream uint64, dmg damage) bool {
			x := &shot{d: d, t: t, sh: sh, outside: o.Outside, rng: rand.New(rand.NewPCG(seed, stream)), hits: hits, leaf: leaf}
			x.run(l)
			hits, leaf = x.hits, x.leaf
			r.Extra++
			return x.resolve(dmg).destroyed
		}
		for li, l := range lines {
			x := &shot{d: d, t: t, sh: sh, outside: o.Outside, rng: rand.New(rand.NewPCG(seed, uint64(vi<<16|li))), hits: hits, leaf: leaf}
			x.run(l)
			hits, leaf = x.hits, x.leaf
			dmg := newDamage()
			f := x.resolve(dmg)
			alone[li] = f.destroyed
			if !f.destroyed {
				gated := (o.FollowUps || o.Pairs) && sh.follows > 0 && f.interior && t.crewHit(dmg)
				silenced, window := false, 0.0
				if o.ReturnFire {
					silenced, window = t.silenced(dmg, d, o)
				}
				third := silenced && sh.follows > 0 && sh.times[1] < window
				need := 0
				if gated && o.FollowUps {
					need = sh.follows
				}
				if gated && o.Pairs {
					after[li] = dmg.clone()
				}
				if third {
					need = max(need, 2)
				}
				kill := 0
				for k := 1; k <= need; k++ {
					if next(l, uint64(k)<<32|uint64(vi<<16|li), dmg) {
						kill = k
						break
					}
				}
				if gated {
					fl.Lines++
					if o.FollowUps && kill > 0 && kill <= sh.follows {
						fl.Kills++
						fl.Shots += kill
						fl.Seconds += sh.times[kill-1]
					}
				}
				if silenced {
					fl.Silenced++
				}
				if third {
					fl.Third++
					if kill > 0 && kill <= 2 {
						fl.ThirdKills++
					}
				}
			}
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
		// Pairs: the first shot of line i, then a shot along another line j of the same direction that alone does not kill.
		for i := range lines {
			if after[i].hp == nil {
				continue
			}
			for j := range lines {
				if j == i || lines[j].dir != lines[i].dir || alone[j] {
					continue
				}
				fl.Pairs++
				if next(lines[j], 1<<63|uint64(j)<<32|uint64(vi<<16|i), after[i].clone()) {
					fl.PairKills++
				}
			}
		}
		if fl != (Follow{}) {
			fl.Seconds = math.Round(fl.Seconds*1000) / 1000
			vr.Follow = &fl
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
