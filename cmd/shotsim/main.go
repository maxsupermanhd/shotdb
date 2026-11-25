// Command shotsim prints shot results from a game directory, for checks
// without a browser.
//
//	shotsim -data DIR SHOOTER TARGET...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"shotdb/game"
	"shotdb/sim"
)

func main() {
	dir := flag.String("data", ".", "War Thunder install or unpacked datamine")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: shotsim -data DIR SHOOTER TARGET...")
		os.Exit(2)
	}
	if err := run(*dir, flag.Arg(0), flag.Args()[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dir, shooter string, targets []string) error {
	data, err := game.Open(game.Dir(dir))
	if err != nil {
		return err
	}
	s := &sim.Sim{Data: data}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	for _, t := range targets {
		r, err := s.Simulate(shooter, t, -1)
		if err != nil {
			return err
		}
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
