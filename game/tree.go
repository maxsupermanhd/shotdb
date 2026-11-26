package game

import (
	"slices"
	"strings"
)

// A tree is one decoded BLK file: the .blkx JSON shape, which the binary decoder also gives.
type tree = map[string]any

func num(m tree, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func numOr(m tree, key string, def float64) float64 {
	if v, ok := num(m, key); ok {
		return v
	}
	return def
}

func str(m tree, key string) string {
	s, _ := m[key].(string)
	return s
}

// blocks gives the child blocks of a name, whether it holds one block or a list of them.
func blocks(m tree, key string) []tree {
	switch v := m[key].(type) {
	case tree:
		return []tree{v}
	case []any:
		var out []tree
		for _, e := range v {
			if b, ok := e.(tree); ok {
				out = append(out, b)
			}
		}
		return out
	}
	return nil
}

// Point is one (x, y) node of a curve.
type Point struct{ X, Y float64 }

// points collects the 2D vector params of a block whose names start with prefix,
// sorted by x. A repeated name gives a list of vectors; every one counts.
func points(m tree, prefix string) []Point {
	var out []Point
	for k, v := range m {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		l, ok := v.([]any)
		if !ok {
			continue
		}
		if p, ok := vec2(l); ok {
			out = append(out, p)
			continue
		}
		for _, e := range l {
			if el, ok := e.([]any); ok {
				if p, ok := vec2(el); ok {
					out = append(out, p)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b Point) int {
		switch {
		case a.X < b.X:
			return -1
		case a.X > b.X:
			return 1
		}
		return 0
	})
	return out
}

func vec2(l []any) (Point, bool) {
	if len(l) != 2 {
		return Point{}, false
	}
	x, ok1 := l[0].(float64)
	y, ok2 := l[1].(float64)
	return Point{x, y}, ok1 && ok2
}

// Interp reads a curve linearly in x and clamps at its ends.
func Interp(c []Point, x float64) float64 {
	if len(c) == 0 {
		return 0
	}
	if x <= c[0].X {
		return c[0].Y
	}
	for i := 1; i < len(c); i++ {
		if x <= c[i].X {
			a, b := c[i-1], c[i]
			if b.X == a.X {
				return b.Y
			}
			return a.Y + (b.Y-a.Y)*(x-a.X)/(b.X-a.X)
		}
	}
	return c[len(c)-1].Y
}

// strs gives a string param, or a list of them.
func strs(m tree, key string) []string {
	switch v := m[key].(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func sortedKeys(m tree) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
