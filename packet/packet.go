// Package packet encodes what the manager sends to the compute workers: the
// global tables once, the shooter once per queue, and one packet per target.
// The encoding is encoding/gob, which keeps every float64 exact (also +Inf
// and NaN). Gob drops empty maps and slices, so they come back as nil: the
// simulation only reads them. Each map value of Vehicle.Parts is its own
// *Part. The parts of one DamageEffects block share their rule lists, which
// gob would copy for each part, so a target packet sends each list once.
package packet

import (
	"bytes"
	"encoding/gob"

	"shotdb/dm"
	"shotdb/geom"
)

func encode(values ...any) ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	for _, v := range values {
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

func decode(b []byte, values ...any) error {
	dec := gob.NewDecoder(bytes.NewReader(b))
	for _, v := range values {
		if err := dec.Decode(v); err != nil {
			return err
		}
	}
	return nil
}

func Tables(t *dm.Tables) ([]byte, error) { return encode(t) }

func ReadTables(b []byte) (*dm.Tables, error) {
	t := &dm.Tables{}
	return t, decode(b, t)
}

// Shooter keeps only what the simulation reads of the shooter: its unit and shells.
func Shooter(v *dm.Vehicle) ([]byte, error) {
	return encode(&dm.Vehicle{Unit: v.Unit, Shells: v.Shells})
}

func ReadShooter(b []byte) (*dm.Vehicle, error) {
	v := &dm.Vehicle{}
	return v, decode(b, v)
}

// target is a target packet. Rules holds the distinct rule lists, and OnHit
// and OnKill give the index in Rules of each part's lists, plus 1.
type target struct {
	Vehicle       *dm.Vehicle
	Rules         [][]dm.Rule
	OnHit, OnKill map[string]int
	Nodes         []geom.Node
}

// Target keeps what the simulation reads of a target, and its collision nodes.
func Target(v *dm.Vehicle, nodes []geom.Node) ([]byte, error) {
	t := target{
		Vehicle: &dm.Vehicle{
			Unit: v.Unit, Parts: map[string]*dm.Part{}, Crew: v.Crew, MinCrew: v.MinCrew,
			Racks: v.Racks, RackExplosive: v.RackExplosive, Pressure: v.Pressure, Open: v.Open,
		},
		OnHit: map[string]int{}, OnKill: map[string]int{}, Nodes: nodes,
	}
	type list struct {
		first *dm.Rule
		n     int
	}
	seen := map[list]int{}
	index := func(rules []dm.Rule) int {
		if len(rules) == 0 {
			return 0
		}
		k := list{&rules[0], len(rules)}
		if _, ok := seen[k]; !ok {
			t.Rules = append(t.Rules, rules)
			seen[k] = len(t.Rules)
		}
		return seen[k]
	}
	for name, p := range v.Parts {
		q := *p
		t.OnHit[name], t.OnKill[name] = index(p.OnHit), index(p.OnKill)
		q.OnHit, q.OnKill = nil, nil
		t.Vehicle.Parts[name] = &q
	}
	return encode(&t)
}

func ReadTarget(b []byte) (*dm.Vehicle, []geom.Node, error) {
	var t target
	if err := decode(b, &t); err != nil {
		return nil, nil, err
	}
	for name, p := range t.Vehicle.Parts {
		if i := t.OnHit[name]; i > 0 {
			p.OnHit = t.Rules[i-1]
		}
		if i := t.OnKill[name]; i > 0 {
			p.OnKill = t.Rules[i-1]
		}
	}
	return t.Vehicle, t.Nodes, nil
}
