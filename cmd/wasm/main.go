//go:build js && wasm

// Command wasm exposes the shot simulation to JavaScript as globalThis.shotdb:
//
//	shotdb.open(readFile, readRange, listDir) -> Promise<string>  JSON {source, version, units, trees}
//	shotdb.simulate(shooter, target)          -> Promise<string>  JSON sim.Result
//
// readFile(path) and readRange(path, offset, length) must return a Promise of
// a Uint8Array, listDir(path) a Promise of an array of file names; each gives
// null when the file or directory does not exist. Paths are relative to the
// chosen game directory.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"syscall/js"

	"shotdb/game"
	"shotdb/sim"
)

var (
	opened    *game.Data
	simulator sim.Simulator
)

func main() {
	api := js.Global().Get("Object").New()
	api.Set("open", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 3 || args[0].Type() != js.TypeFunction || args[1].Type() != js.TypeFunction || args[2].Type() != js.TypeFunction {
			return reject(errors.New("open needs readFile, readRange and listDir functions"))
		}
		files := jsFiles{read: args[0], readRange: args[1], list: args[2]}
		return promise(func() (any, error) {
			data, err := game.Open(files)
			if err != nil {
				return "", err
			}
			opened = data
			simulator = &sim.Sim{Data: data}
			units := make([]game.Unit, 0, len(data.Units))
			for _, u := range data.Units {
				units = append(units, u)
			}
			out, err := json.Marshal(map[string]any{"source": data.Source, "version": data.Version, "units": units, "trees": data.Trees})
			return string(out), err
		})
	}))
	api.Set("simulate", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 2 {
			return reject(errors.New("simulate needs a shooter and a target"))
		}
		shooter, target := args[0].String(), args[1].String()
		return promise(func() (any, error) {
			if simulator == nil {
				return "", errors.New("no game data open")
			}
			r, err := simulator.Simulate(shooter, target)
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(r)
			return string(out), err
		})
	}))
	js.Global().Set("shotdb", api)
	select {}
}

// jsFiles is game.Files over the page's functions.
type jsFiles struct{ read, readRange, list js.Value }

func call(name string, fn js.Value, args ...any) (js.Value, error) {
	v, err := await(fn.Invoke(args...))
	if err != nil {
		return v, fmt.Errorf("%s: %w", name, err)
	}
	if v.IsNull() || v.IsUndefined() {
		return v, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
	}
	return v, nil
}

func bytesOf(v js.Value) []byte {
	b := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(b, v)
	return b
}

func (f jsFiles) Read(name string) ([]byte, error) {
	v, err := call(name, f.read, name)
	if err != nil {
		return nil, err
	}
	return bytesOf(v), nil
}

func (f jsFiles) ReadAt(name string, off int64, n int) ([]byte, error) {
	v, err := call(name, f.readRange, name, float64(off), n)
	if err != nil {
		return nil, err
	}
	return bytesOf(v), nil
}

func (f jsFiles) List(dir string) ([]string, error) {
	v, err := call(dir, f.list, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, v.Length())
	for i := range names {
		names[i] = v.Index(i).String()
	}
	return names, nil
}

// promise runs work off the JS event loop, so it may wait on JS promises itself.
func promise(work func() (any, error)) js.Value {
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve, rejectFn := args[0], args[1]
		go func() {
			v, err := work()
			if err != nil {
				rejectFn.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(v)
		}()
		return nil
	})
	defer executor.Release()
	return js.Global().Get("Promise").New(executor)
}

func reject(err error) js.Value {
	return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New(err.Error()))
}

func await(p js.Value) (js.Value, error) {
	done := make(chan js.Value, 1)
	failed := make(chan error, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		done <- args[0]
		return nil
	})
	defer then.Release()
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		failed <- errors.New(args[0].Call("toString").String())
		return nil
	})
	defer catch.Release()
	js.Global().Get("Promise").Call("resolve", p).Call("then", then, catch)
	select {
	case v := <-done:
		return v, nil
	case err := <-failed:
		return js.Value{}, err
	}
}
