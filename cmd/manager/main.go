//go:build js && wasm

// Command manager runs in the manager worker as globalThis.manager. It owns
// the game files and the shot queue, and hands one target packet at a time
// to the compute workers over their MessagePorts.
//
//	manager.start(readFile, readRange, listDir, useSource, blob)  once, before any message
//	manager.message(msg)                                         each message of the page
//
// readFile(path) and readRange(path, offset, length) must return a Promise of
// a Uint8Array, listDir(path) a Promise of an array of file names, and
// blob(path, offset, length) a Promise of a Blob of that range that reads
// nothing yet; each gives null when the file or directory does not exist.
// Paths are relative to the directory of the source that useSource(source) set last.
//
// Messages of the page:
//
//	{op: "open", id, source}   open the game files of a source; the answer "opened" has the same id
//	{op: "run", shooter, round, lines, options}
//	                           start the queue of the shooter ("" for none), the round (-1 for the
//	                           best of all), the shot lines a round (a count of sim.Samples; others
//	                           keep the count) and the model (the JSON of sim.Options). With no game
//	                           data, the manager keeps the choice for the next open.
//	{op: "go", gen, done}      start queue gen; done lists the targets that the page has results for
//	{op: "pause"}              stop or continue the queue
//	{op: "hold", n}            targets that a compute worker holds at once
//	{op: "worker", id, port}   a compute worker joined; port is a MessagePort to it
//	{op: "drop", id}           the page stops a compute worker; its targets go back in the queue
//	{op: "failed", id, error, gen, target}
//	                           a compute worker failed in the shot at target of queue gen (both
//	                           absent when it ran none): that target fails, the others go back
//
// Messages to the page are JSON strings, see web/src/types.ts. The first one,
// "samples", gives the line counts that "run" takes and the default model. Each new queue ("queue")
// waits for the "go" of the page, so that the page can give the results that it keeps.
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"
	"sync"
	"syscall/js"

	"shotdb/dm"
	"shotdb/game"
	"shotdb/packet"
	"shotdb/sim"
)

type job struct {
	gen    int
	target string
}

type worker struct {
	id     int
	port   js.Value
	listen js.Func
	flight []job // targets sent and not answered, the oldest first
}

type manager struct {
	files     jsFiles
	useSource js.Value
	source    js.Value // of the data; undefined before the first good open
	data      *game.Data
	targets   []string // the units of the trees that the data has
	tables    []byte   // packet of the tables, for workers that join later

	shooter    string
	round      int // -1: the best of all rounds
	sample     int // index in sim.Samples
	options    sim.Options
	optionsRaw string // the JSON of options, for the compute workers
	rounds     []sim.ShellInfo
	shooterPkt []byte
	gen        int // bumps on every queue restart; answers of older queues are dropped
	queue      []string
	waiting    bool // for the "go" of the queue
	total      int
	done       int
	paused     bool
	hold       int // targets per worker: one runs, the others wait in the worker

	workers []*worker
}

// Events come from JS callbacks, which must not block. The loop runs them in order.
var (
	mu      sync.Mutex
	pending []func(*manager)
	wake    = make(chan struct{}, 1)
)

func push(f func(*manager)) {
	mu.Lock()
	pending = append(pending, f)
	mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func main() {
	m := &manager{round: -1, sample: sim.DefaultSample, options: sim.DefaultOptions, optionsRaw: "{}", hold: 2}
	var counts []int
	for _, s := range sim.Samples {
		counts = append(counts, s.Lines())
	}
	post(map[string]any{"op": "samples", "lines": counts, "default": counts[sim.DefaultSample], "options": sim.DefaultOptions})
	started := make(chan struct{})
	api := js.Global().Get("Object").New()
	api.Set("start", js.FuncOf(func(_ js.Value, args []js.Value) any {
		m.files = jsFiles{read: args[0], readRange: args[1], list: args[2], blob: args[4]}
		m.useSource = args[3]
		close(started)
		return nil
	}))
	api.Set("message", js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer func() {
			if r := recover(); r != nil {
				post(map[string]any{"op": "error", "error": fmt.Sprint("manager: bad message: ", r)})
			}
		}()
		msg := args[0]
		switch op := msg.Get("op").String(); op {
		case "open":
			id, src := msg.Get("id").Int(), msg.Get("source")
			push(func(m *manager) { m.open(id, src) })
		case "run":
			id, round, n := msg.Get("shooter").String(), msg.Get("round").Int(), msg.Get("lines").Float()
			raw := msg.Get("options").String()
			o, err := sim.ParseOptions([]byte(raw))
			push(func(m *manager) {
				m.shooter, m.round = id, round
				if err != nil {
					post(map[string]any{"op": "error", "error": "manager: " + err.Error()})
				} else {
					m.options, m.optionsRaw = o, raw
				}
				if i := slices.IndexFunc(sim.Samples, func(s sim.Sample) bool { return float64(s.Lines()) == n }); i >= 0 {
					m.sample = i
				}
				if m.data != nil {
					m.restart()
				}
			})
		case "go":
			gen := msg.Get("gen").Int()
			list := msg.Get("done")
			done := make(map[string]bool, list.Length())
			for i := range list.Length() {
				done[list.Index(i).String()] = true
			}
			push(func(m *manager) {
				if gen != m.gen || !m.waiting {
					return
				}
				m.waiting = false
				n := len(m.queue)
				m.queue = slices.DeleteFunc(m.queue, func(t string) bool { return done[t] })
				m.done += n - len(m.queue)
				m.progress()
			})
		case "pause":
			push(func(m *manager) {
				m.paused = !m.paused
				m.progress()
			})
		case "hold":
			n := msg.Get("n").Float()
			push(func(m *manager) {
				// NaN and the values out of range keep the default
				if n >= 1 && n <= 8 {
					m.hold = int(n)
				}
			})
		case "worker":
			id, port := msg.Get("id").Int(), msg.Get("port")
			push(func(m *manager) { m.join(id, port) })
		case "drop":
			id := msg.Get("id").Int()
			push(func(m *manager) { m.leave(id, nil, nil) })
		case "failed":
			id, text := msg.Get("id").Int(), msg.Get("error").String()
			var shot *job
			if t := msg.Get("target"); t.Type() == js.TypeString {
				shot = &job{msg.Get("gen").Int(), t.String()}
			}
			push(func(m *manager) { m.leave(id, shot, fmt.Errorf("compute worker failed: %s", text)) })
		default:
			post(map[string]any{"op": "error", "error": "manager: unknown message " + op})
		}
		return nil
	}))
	js.Global().Set("manager", api)
	<-started
	m.run()
}

func (m *manager) run() {
	defer func() {
		if r := recover(); r != nil {
			post(map[string]any{"op": "fatal", "error": fmt.Sprint("manager: ", r)})
		}
	}()
	for {
		mu.Lock()
		events := pending
		pending = nil
		mu.Unlock()
		for _, f := range events {
			f(m)
		}
		if len(events) == 0 && !m.dispatch() {
			<-wake
		}
	}
}

func post(msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		b = []byte(fmt.Sprintf(`{"op":"fatal","error":%q}`, "manager: "+err.Error()))
	}
	js.Global().Call("postMessage", string(b))
}

func send(w *worker, msg map[string]any, pkt []byte) {
	u8 := js.Global().Get("Uint8Array").New(len(pkt))
	js.CopyBytesToJS(u8, pkt)
	msg["buf"] = u8
	w.port.Call("postMessage", msg, []any{u8.Get("buffer")})
}

// stop ends the running queue. The workers skip the targets of it that they hold and have not started.
func (m *manager) stop() {
	m.gen++
	for _, w := range m.workers {
		w.port.Call("postMessage", map[string]any{"op": "gen", "gen": m.gen})
	}
}

// open loads the game files of src. The old data goes first, as wasm memory never
// shrinks. When the new data fails, the old source loads again and its queue continues.
func (m *manager) open(id int, src js.Value) {
	m.data = nil
	m.useSource.Invoke(src)
	d, err := guard(func() (*game.Data, error) { return game.Open(m.files) })
	var tables []byte
	if err == nil {
		tables, err = packet.Tables(&d.Tables)
	}
	if err != nil {
		post(map[string]any{"op": "opened", "id": id, "error": err.Error()})
		m.useSource.Invoke(m.source)
		if m.source.IsUndefined() {
			return
		}
		if m.data, err = guard(func() (*game.Data, error) { return game.Open(m.files) }); err != nil {
			post(map[string]any{"op": "error", "error": "cannot load the old folder again: " + err.Error()})
			m.source, m.targets, m.tables = js.Undefined(), nil, nil
			m.restart() // with no data: no shooter, no queue
		}
		return
	}
	m.stop()
	m.data, m.source, m.tables, m.targets = d, src, tables, nil
	seen := map[string]bool{}
	for _, t := range d.Trees {
		for _, r := range t.Ranks {
			for _, g := range []game.Grid{r.Research, r.Premium} {
				for _, row := range g.Rows {
					for _, stack := range row {
						for _, c := range stack {
							if _, ok := d.Units[c.ID]; ok && !seen[c.ID] {
								seen[c.ID] = true
								m.targets = append(m.targets, c.ID)
							}
						}
					}
				}
			}
		}
	}
	for _, w := range m.workers {
		send(w, map[string]any{"op": "tables"}, m.tables)
	}
	units := make([]dm.Unit, 0, len(d.Units))
	for _, u := range d.Units {
		units = append(units, u)
	}
	post(map[string]any{"op": "opened", "id": id, "info": map[string]any{"source": d.Source, "version": d.Version, "units": units, "trees": d.Trees, "seatChange": d.Tables.SeatChange}})
	m.restart() // a shooter that the user picked before stays, if the new data has it
}

// restart makes the queue of the shooter, the round and the sample, outwards from the
// shooter's BR: by distance, the higher BR first on ties. The queue starts on the "go" of the page.
func (m *manager) restart() {
	m.stop()
	m.paused = false // a new shooter, round or line count starts its queue at once
	m.queue, m.rounds, m.shooterPkt = nil, nil, nil
	m.total, m.done, m.waiting = 0, 0, false
	msg := map[string]any{"op": "queue", "gen": m.gen, "shooter": m.shooter, "lines": sim.Samples[m.sample].Lines(), "options": m.options}
	defer func() {
		post(msg)
		m.progress()
	}()
	if m.data == nil {
		m.shooter = ""
		return
	}
	msg["source"], msg["version"] = m.data.Source, m.data.Version
	if _, ok := m.data.Units[m.shooter]; !ok {
		m.shooter = ""
		return
	}
	a, err := guard(func() (*dm.Vehicle, error) { return m.data.Vehicle(m.shooter) })
	var pkt []byte
	if err == nil {
		pkt, err = packet.Shooter(a)
	}
	if err != nil {
		msg["error"] = err.Error()
		return
	}
	m.rounds = sim.Rounds(&m.data.Tables, a, m.options)
	if m.round >= len(m.rounds) || m.round < 0 {
		m.round = -1
	}
	m.shooterPkt = pkt
	for _, w := range m.workers {
		m.sendShooter(w)
	}
	br := func(id string) float64 { return m.data.Units[id].BR }
	sbr := br(m.shooter)
	m.queue = slices.Clone(m.targets)
	slices.SortFunc(m.queue, func(x, y string) int {
		dx, dy := br(x)-sbr, br(y)-sbr
		return cmp.Or(cmp.Compare(math.Abs(dx), math.Abs(dy)), cmp.Compare(dy, dx), strings.Compare(x, y))
	})
	m.total = len(m.queue)
	m.waiting = true
	msg["round"], msg["range"], msg["rounds"], msg["targets"] = m.round, sim.Range, m.rounds, m.queue
}

// dispatch gives the next target to the worker that holds the fewest, if one has room.
func (m *manager) dispatch() bool {
	if m.shooterPkt == nil || m.paused || m.waiting || len(m.queue) == 0 {
		return false
	}
	var w *worker
	for _, x := range m.workers {
		if len(x.flight) < m.hold && (w == nil || len(x.flight) < len(w.flight)) {
			w = x
		}
	}
	if w == nil {
		return false
	}
	id := m.queue[0]
	m.queue = m.queue[1:]
	// The compute worker reads and parses the collision resource from the blob, so that the manager does not.
	var blob js.Value
	pkt, err := guard(func() ([]byte, error) {
		v, err := m.data.Vehicle(id)
		if err != nil {
			return nil, err
		}
		path, off, n, err := m.data.Collision(v)
		if err != nil {
			return nil, err
		}
		if blob, err = call(path, m.files.blob, path, float64(off), n); err != nil {
			return nil, err
		}
		return packet.Target(v)
	})
	if err != nil {
		m.finish(id, "", err)
		return true
	}
	w.flight = append(w.flight, job{m.gen, id})
	post(map[string]any{"op": "start", "gen": m.gen, "target": id})
	send(w, map[string]any{"op": "target", "gen": m.gen, "target": id, "blob": blob}, pkt)
	m.progress()
	return true
}

// answer takes a worker's answer to a target: the result JSON, or "" when the worker skipped a target of an older queue.
func (m *manager) answer(w *worker, j job, result string) {
	i := slices.Index(w.flight, j)
	if i < 0 {
		return
	}
	w.flight = slices.Delete(w.flight, i, i+1)
	if j.gen != m.gen {
		return
	}
	// a worker skips only the targets of older queues
	if result == "" {
		m.finish(j.target, "", errors.New("the compute worker gave no result"))
		return
	}
	var r struct{ Error string }
	if err := json.Unmarshal([]byte(result), &r); err != nil {
		m.finish(j.target, "", fmt.Errorf("result: %w", err))
		return
	}
	if r.Error != "" {
		m.finish(j.target, "", errors.New(r.Error))
		return
	}
	m.finish(j.target, result, nil)
}

func (m *manager) finish(target, result string, err error) {
	msg := map[string]any{"op": "result", "gen": m.gen, "target": target}
	if err != nil {
		msg["error"] = err.Error()
	} else {
		msg["result"] = json.RawMessage(result)
	}
	m.done++
	post(msg)
	m.progress()
}

func (m *manager) progress() {
	running := 0
	for _, w := range m.workers {
		for _, j := range w.flight {
			if j.gen == m.gen {
				running++
			}
		}
	}
	msg := map[string]any{"op": "progress", "gen": m.gen, "done": m.done, "total": m.total, "running": running, "paused": m.paused}
	if len(m.queue) > 0 {
		msg["at"] = m.data.Units[m.queue[0]].BR
	}
	post(msg)
}

func (m *manager) join(id int, port js.Value) {
	w := &worker{id: id, port: port}
	w.listen = js.FuncOf(func(_ js.Value, args []js.Value) any {
		d := args[0].Get("data")
		j := job{d.Get("gen").Int(), d.Get("target").String()}
		result := ""
		if r := d.Get("result"); r.Type() == js.TypeString {
			result = r.String()
		}
		push(func(m *manager) { m.answer(w, j, result) })
		return nil
	})
	port.Set("onmessage", w.listen)
	m.workers = append(m.workers, w)
	if m.tables != nil {
		send(w, map[string]any{"op": "tables"}, m.tables)
	}
	if m.shooterPkt != nil {
		m.sendShooter(w)
	}
}

func (m *manager) sendShooter(w *worker) {
	send(w, map[string]any{"op": "shooter", "gen": m.gen, "round": m.round, "sample": m.sample, "options": m.optionsRaw}, m.shooterPkt)
}

// leave removes a worker. The shot that it ran when it failed, if one, fails
// with err. Its other targets of this queue go back to the front of the queue.
func (m *manager) leave(id int, shot *job, err error) {
	i := slices.IndexFunc(m.workers, func(w *worker) bool { return w.id == id })
	if i < 0 {
		return
	}
	w := m.workers[i]
	m.workers = slices.Delete(m.workers, i, i+1)
	w.port.Set("onmessage", js.Null())
	w.port.Call("close")
	w.listen.Release()
	var back []string
	for _, j := range w.flight {
		if j.gen != m.gen {
			continue
		}
		if shot != nil && j == *shot {
			m.finish(j.target, "", err)
		} else {
			back = append(back, j.target)
		}
	}
	w.flight = nil // answers that come after this are dropped
	if len(back) > 0 {
		post(map[string]any{"op": "back", "gen": m.gen, "targets": back})
	}
	m.queue = append(back, m.queue...)
	m.progress()
}

func guard[T any](work func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return work()
}

// jsFiles is game.Files over the worker's functions.
type jsFiles struct{ read, readRange, list, blob js.Value }

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
