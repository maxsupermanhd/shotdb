//go:build js && wasm

// Command compute runs the shot simulation in a compute worker, as
// globalThis.compute. It reads no game files: the manager sends it the
// packets of package packet.
//
//	compute.tables(bytes)          -> error message, "" if none
//	compute.shooter(bytes, round)  -> error message, "" if none; round -1 for the best
//	compute.simulate(bytes)        -> JSON of a sim.Result, or {target, error}
//
// Each call is synchronous. A panic comes back as an error.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"syscall/js"
	"time"

	"shotdb/dm"
	"shotdb/packet"
	"shotdb/sim"
)

var (
	tables  *dm.Tables
	shooter *dm.Vehicle
	round   = -1
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("tables", js.FuncOf(func(_ js.Value, args []js.Value) any {
		err := guard(func() (err error) {
			tables, err = packet.ReadTables(bytesOf(args[0]))
			return err
		})
		if err != nil {
			return err.Error()
		}
		return ""
	}))
	api.Set("shooter", js.FuncOf(func(_ js.Value, args []js.Value) any {
		err := guard(func() (err error) {
			round = args[1].Int()
			shooter, err = packet.ReadShooter(bytesOf(args[0]))
			return err
		})
		if err != nil {
			return err.Error()
		}
		return ""
	}))
	api.Set("simulate", js.FuncOf(func(_ js.Value, args []js.Value) any {
		var out []byte
		var target string
		err := guard(func() error {
			start := time.Now()
			v, nodes, err := packet.ReadTarget(bytesOf(args[0]))
			if err != nil {
				return err
			}
			target = v.ID
			if tables == nil || shooter == nil {
				return errors.New("no tables or shooter yet")
			}
			r, err := sim.Simulate(tables, shooter, v, nodes, round)
			if err != nil {
				return err
			}
			r.Ms = math.Round(float64(time.Since(start).Microseconds())/100) / 10
			out, err = json.Marshal(r)
			return err
		})
		if err != nil {
			out, _ = json.Marshal(map[string]string{"target": target, "error": err.Error()}) // a map of strings always encodes
		}
		return string(out)
	}))
	js.Global().Set("compute", api)
	select {}
}

func guard(work func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return work()
}

func bytesOf(v js.Value) []byte {
	b := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(b, v)
	return b
}
