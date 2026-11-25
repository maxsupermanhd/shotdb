package game

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Card is one vehicle of a research tree.
type Card struct {
	ID    string `json:"id"`
	Prem  bool   `json:"prem,omitempty"`
	Squad bool   `json:"squad,omitempty"`
}

// Grid is one table of a rank. A cell is a stack of cards, or nil.
type Grid struct {
	Cols int        `json:"cols"`
	Rows [][][]Card `json:"rows"`
}

type Rank struct {
	N        int  `json:"n"`
	Research Grid `json:"research"`
	Premium  Grid `json:"premium"`
}

// Tree is the ground research tree of one nation.
type Tree struct {
	Country string `json:"country"`
	Name    string `json:"name"`
	Ranks   []Rank `json:"ranks"`
}

// entry is one slot of a shop.blk range: a unit, or a group of units that
// the page shows as one stack.
type entry struct {
	cards []Card
	rank  int
}

// loadTrees lays out the ground trees of shop.blk as the game does. Each
// "range" block of country_<nation>/army is a column, and the units of a
// rank fill its rows in file order. A range of mostly premium and squadron
// units goes to the premium side.
func (d *Data) loadTrees(wp tree) error {
	shop, err := d.blk("char", "config/shop.blk")
	if err != nil {
		return err
	}
	order, err := d.order("char", "config/shop.blk")
	if err != nil {
		return err
	}
	inOrder := func(m tree) []string {
		var keys []string
		for k, v := range m {
			if _, ok := v.(tree); ok {
				keys = append(keys, k)
			}
		}
		slices.SortFunc(keys, func(a, b string) int { return order[a] - order[b] })
		return keys
	}
	wpLower := map[string]tree{}
	for id, v := range wp {
		if u, ok := v.(tree); ok {
			wpLower[strings.ToLower(id)] = u
		}
	}

	d.Trees = nil
	for _, key := range inOrder(shop) {
		country, ok := strings.CutPrefix(key, "country_")
		if !ok {
			continue
		}
		armies := blocks(shop[key].(tree), "army")
		if len(armies) == 0 {
			continue
		}
		ranges := blocks(armies[0], "range")

		units := map[string]tree{}
		slaves := map[string]bool{}
		for _, r := range ranges {
			for _, k := range inOrder(r) {
				b := r[k].(tree)
				if _, ok := b["rank"]; !ok {
					for _, u := range inOrder(b) {
						units[strings.ToLower(u)] = b[u].(tree)
					}
					continue
				}
				units[strings.ToLower(k)] = b
				if s := str(b, "slaveUnit"); s != "" {
					slaves[strings.ToLower(s)] = true
				}
			}
		}
		// special: bought, a gift, an event prize, or a squadron vehicle.
		// Only a vehicle bought with gold is marked premium.
		card := func(id string) (c Card, special bool) {
			b := units[id]
			w := wpLower[id]
			c = Card{ID: id, Squad: b["isClanVehicle"] == true || str(w, "researchType") == "clanVehicle"}
			_, gold := w["costGold"]
			c.Prem = gold && !c.Squad
			_, gift := b["gift"]
			_, event := b["event"]
			_, market := b["marketplaceItemdefId"]
			return c, c.Prem || c.Squad || gift || event || market
		}

		var research, premium [][]entry
		maxRank := 0
		for _, r := range ranges {
			var col []entry
			special, all := 0, 0
			for _, k := range inOrder(r) {
				id := strings.ToLower(k)
				if slaves[id] {
					continue
				}
				b := r[k].(tree)
				var e entry
				add := func(id string) {
					c, sp := card(id)
					e.cards = append(e.cards, c)
					all++
					if sp {
						special++
					}
				}
				if rank, ok := num(b, "rank"); ok {
					e.rank = int(rank)
					add(id)
					if s := str(b, "slaveUnit"); s != "" {
						add(strings.ToLower(s))
					}
				} else {
					for _, u := range inOrder(b) {
						add(strings.ToLower(u))
						if e.rank == 0 {
							e.rank = int(numOr(b[u].(tree), "rank", 0))
						}
					}
				}
				if len(e.cards) == 0 || e.rank == 0 {
					continue
				}
				maxRank = max(maxRank, e.rank)
				col = append(col, e)
			}
			if 2*special > all {
				premium = append(premium, col)
			} else {
				research = append(research, col)
			}
		}

		t := Tree{Country: country, Name: d.names[key]}
		if t.Name == "" {
			t.Name = country
		}
		for rank := 1; rank <= maxRank; rank++ {
			r := Rank{N: rank, Research: grid(research, rank), Premium: grid(premium, rank)}
			if len(r.Research.Rows)+len(r.Premium.Rows) > 0 {
				t.Ranks = append(t.Ranks, r)
			}
		}
		d.Trees = append(d.Trees, t)
	}
	if len(d.Trees) == 0 {
		return errors.New("shop.blk has no ground trees")
	}
	return nil
}

func grid(cols [][]entry, rank int) Grid {
	g := Grid{Cols: len(cols)}
	for c, col := range cols {
		row := 0
		for _, e := range col {
			if e.rank != rank {
				continue
			}
			if row == len(g.Rows) {
				g.Rows = append(g.Rows, make([][]Card, len(cols)))
			}
			g.Rows[row][c] = e.cards
			row++
		}
	}
	return g
}

// order gives the position of each block name in a .blk file, by first
// appearance. The decoded tree is a map, so it lost the file order.
func (d *Data) order(container, path string) (map[string]int, error) {
	if d.vromfs == nil {
		raw, err := d.read(container, path+"x")
		if err != nil {
			return nil, err
		}
		return jsonOrder(raw)
	}
	raw, err := d.read(container, path)
	if err != nil {
		return nil, err
	}
	order, err := blkOrder(raw, d.slims[container])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return order, nil
}

func jsonOrder(raw []byte) (map[string]int, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	order := map[string]int{}
	var objects []bool // per open container: is it an object
	key := false       // the next string is a key
	for {
		t, err := dec.Token()
		if err == io.EOF {
			return order, nil
		}
		if err != nil {
			return nil, err
		}
		switch t {
		case json.Delim('{'):
			objects = append(objects, true)
			key = true
			continue
		case json.Delim('['):
			objects = append(objects, false)
			key = false
			continue
		case json.Delim('}'), json.Delim(']'):
			objects = objects[:len(objects)-1]
		default:
			if s, ok := t.(string); ok && key {
				if _, seen := order[s]; !seen {
					order[s] = len(order)
				}
				key = false
				continue
			}
		}
		key = len(objects) > 0 && objects[len(objects)-1]
	}
}

// blkOrder reads the block table of a binary BLK. The table lists the blocks
// with the children of each block next to each other, in file order.
func blkOrder(raw []byte, s slim) (map[string]int, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty BLK")
	}
	names := s.names
	var body []byte
	switch raw[0] {
	case 0x01:
		body, names = raw[1:], nil
	case 0x03:
		body = raw[1:]
	case 0x04, 0x05:
		var err error
		if body, err = s.zstd.DecodeAll(raw[1:], nil); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("BLK type %#x: no block order", raw[0])
	}
	p := 0
	next := func() (int, error) {
		v, n := binary.Uvarint(body[min(p, len(body)):])
		if n <= 0 {
			return 0, errors.New("BLK block table truncated")
		}
		p += n
		return int(v), nil
	}
	skip := func(n int) error {
		if n < 0 || p+n > len(body) {
			return errors.New("BLK block table truncated")
		}
		p += n
		return nil
	}
	if _, err := next(); err != nil { // name count
		return nil, err
	}
	if names == nil {
		size, err := next()
		if err != nil {
			return nil, err
		}
		if err := skip(size); err != nil {
			return nil, err
		}
		names = strings.Split(strings.TrimSuffix(string(body[p-size:p]), "\x00"), "\x00")
	}
	var head [3]int // blocks, params, params data size
	for i := range head {
		v, err := next()
		if err != nil {
			return nil, err
		}
		head[i] = v
	}
	if err := skip(head[2] + 8*head[1]); err != nil {
		return nil, err
	}
	order := map[string]int{}
	for i := range head[0] {
		var f [3]int // name id, params, children
		for j := range f {
			v, err := next()
			if err != nil {
				return nil, err
			}
			f[j] = v
		}
		if f[2] > 0 {
			if _, err := next(); err != nil { // first child
				return nil, err
			}
		}
		if f[0] == 0 {
			continue
		}
		if f[0] > len(names) {
			return nil, fmt.Errorf("block %d: name %d out of range", i, f[0])
		}
		if _, seen := order[names[f[0]-1]]; !seen {
			order[names[f[0]-1]] = len(order)
		}
	}
	return order, nil
}
