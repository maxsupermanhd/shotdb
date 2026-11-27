// Command shotsim prints shot results from a game directory, for checks
// without a browser.
//
//	shotsim -data DIR [-lines N] [model flags] SHOOTER TARGET...
//
// The model flags are the model settings of the page; see -h.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"shotdb/game"
	"shotdb/sim"
)

func main() {
	var counts []string
	for _, s := range sim.Samples {
		counts = append(counts, strconv.Itoa(s.Lines()))
	}
	dir := flag.String("data", ".", "War Thunder install or unpacked datamine")
	lines := flag.Int("lines", sim.Samples[sim.DefaultSample].Lines(), "shot lines a round on each target: "+strings.Join(counts, ", "))
	o := sim.DefaultOptions
	flag.BoolVar(&o.FollowUps, "follow", o.FollowUps, "fire the follow-up shots")
	flag.BoolVar(&o.Pairs, "pairs", o.Pairs, "fire the two-shot pairs")
	flag.BoolVar(&o.ReturnFire, "return", o.ReturnFire, "check if the target can fire back")
	flag.Float64Var(&o.Window, "window", o.Window, "s after the first shot for the follow-ups")
	flag.BoolVar(&o.Stock, "stock", o.Stock, "stock loader: a manual loader is slower")
	flag.BoolVar(&o.LastRound, "lastround", o.LastRound, "the first shot fires the last round of the magazine")
	flag.Float64Var(&o.SeatChange, "seat", o.SeatChange, "s to take the gunner's seat, 0 to 60; -1 for the game's value")
	flag.BoolVar(&o.Override, "override", o.Override, "a second gunner (secondGunnerDm) fires for a dead gunner")
	flag.BoolVar(&o.Drives, "drives", o.Drives, "a dead turret drive stops the gun")
	flag.BoolVar(&o.Outside, "outside", o.Outside, "a module hit before the shell goes through a plate makes a pen")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: shotsim -data DIR [-lines N] [model flags] SHOOTER TARGET...")
		os.Exit(2)
	}
	i := slices.IndexFunc(sim.Samples, func(s sim.Sample) bool { return s.Lines() == *lines })
	if i < 0 {
		fmt.Fprintf(os.Stderr, "shotsim: -lines must be one of %s\n", strings.Join(counts, ", "))
		os.Exit(2)
	}
	if err := o.Check(); err != nil {
		fmt.Fprintln(os.Stderr, "shotsim:", err)
		os.Exit(2)
	}
	if err := run(*dir, sim.Samples[i], o, flag.Arg(0), flag.Args()[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string, sample sim.Sample, o sim.Options, shooter string, targets []string) error {
	data, err := game.Open(game.Dir(dir))
	if err != nil {
		return err
	}
	a, err := data.Vehicle(shooter)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	for _, t := range targets {
		v, err := data.Vehicle(t)
		if err != nil {
			return err
		}
		nodes, err := data.Nodes(v)
		if err != nil {
			return err
		}
		r, err := sim.Simulate(&data.Tables, a, v, nodes, -1, sample, o)
		if err != nil {
			return err
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
