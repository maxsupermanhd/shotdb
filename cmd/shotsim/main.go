// Command shotsim prints shot results from a game directory, for checks
// without a browser.
//
//	shotsim -data DIR [-lines N] SHOOTER TARGET...
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
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: shotsim -data DIR [-lines N] SHOOTER TARGET...")
		os.Exit(2)
	}
	i := slices.IndexFunc(sim.Samples, func(s sim.Sample) bool { return s.Lines() == *lines })
	if i < 0 {
		fmt.Fprintf(os.Stderr, "shotsim: -lines must be one of %s\n", strings.Join(counts, ", "))
		os.Exit(2)
	}
	if err := run(*dir, sim.Samples[i], flag.Arg(0), flag.Args()[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir string, sample sim.Sample, shooter string, targets []string) error {
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
		r, err := sim.Simulate(&data.Tables, a, v, nodes, -1, sample)
		if err != nil {
			return err
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
