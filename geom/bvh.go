package geom

import (
	"math"
	"slices"
)

type Vec = [3]float64

// Tri is one triangle of a part.
type Tri struct {
	A, B, C Vec
	Part    int32
}

type tri struct {
	a, e1, e2, n Vec
	part         int32
}

type bvhNode struct {
	min, max Vec
	// Leaves: count > 0 triangles from first. Inner nodes: children first and right.
	first, count, right int32
}

// Mesh is a set of triangles with a bounding volume hierarchy.
type Mesh struct {
	tris     []tri
	nodes    []bvhNode
	Min, Max Vec
}

// Hit is one crossing of a ray with a triangle.
type Hit struct {
	T    float64
	Part int32
	// N is the unit normal of the triangle, turned against the ray.
	N Vec
	// Cos is |cos| of the angle between the ray and the normal.
	Cos float64
}

const leafSize = 4

// Build makes the hierarchy. Degenerate triangles are dropped.
func Build(in []Tri) *Mesh {
	m := &Mesh{}
	cent := make([]Vec, 0, len(in))
	for _, t := range in {
		e1, e2 := sub(t.B, t.A), sub(t.C, t.A)
		n := cross(e1, e2)
		l := math.Sqrt(dot(n, n))
		if l < 1e-12 {
			continue
		}
		m.tris = append(m.tris, tri{a: t.A, e1: e1, e2: e2, n: scale(n, 1/l), part: t.Part})
		cent = append(cent, Vec{(t.A[0] + t.B[0] + t.C[0]) / 3, (t.A[1] + t.B[1] + t.C[1]) / 3, (t.A[2] + t.B[2] + t.C[2]) / 3})
	}
	if len(m.tris) == 0 {
		return m
	}
	order := make([]int32, len(m.tris))
	for i := range order {
		order[i] = int32(i)
	}
	boxes := make([][2]Vec, len(m.tris))
	for i, t := range m.tris {
		boxes[i][0], boxes[i][1] = empty()
		for _, p := range [3]Vec{t.a, add(t.a, t.e1), add(t.a, t.e2)} {
			for k := range 3 {
				boxes[i][0][k] = min(boxes[i][0][k], p[k])
				boxes[i][1][k] = max(boxes[i][1][k], p[k])
			}
		}
	}
	m.split(order, 0, cent, boxes)
	tris := make([]tri, len(order))
	for i, o := range order {
		tris[i] = m.tris[o]
	}
	m.tris = tris
	m.Min, m.Max = m.nodes[0].min, m.nodes[0].max
	return m
}

func empty() (Vec, Vec) {
	inf := math.Inf(1)
	return Vec{inf, inf, inf}, Vec{-inf, -inf, -inf}
}

func area(min, max Vec) float64 {
	d := sub(max, min)
	return d[0]*d[1] + d[1]*d[2] + d[2]*d[0]
}

// split makes the node of order (a part of the global order, sorted in place)
// by a binned surface area heuristic. start is the index of order[0] in the global order.
func (m *Mesh) split(order []int32, start int32, cent []Vec, boxes [][2]Vec) int32 {
	idx := int32(len(m.nodes))
	m.nodes = append(m.nodes, bvhNode{})
	lo, hi := empty()
	cmin, cmax := empty()
	for _, o := range order {
		lo, hi = merge(lo, hi, boxes[o][0], boxes[o][1])
		cmin, cmax = merge(cmin, cmax, cent[o], cent[o])
	}
	m.nodes[idx].min, m.nodes[idx].max = lo, hi
	leaf := func() int32 {
		m.nodes[idx].first, m.nodes[idx].count = start, int32(len(order))
		return idx
	}
	if len(order) <= leafSize {
		return leaf()
	}
	const bins = 12
	bestCost, bestAxis, bestSplit := math.Inf(1), -1, 0
	for axis := range 3 {
		ext := cmax[axis] - cmin[axis]
		if ext <= 1e-12 {
			continue
		}
		var cnt [bins]int
		var bmin, bmax [bins]Vec
		for b := range bins {
			bmin[b], bmax[b] = empty()
		}
		for _, o := range order {
			b := min(int((cent[o][axis]-cmin[axis])/ext*bins), bins-1)
			cnt[b]++
			bmin[b], bmax[b] = merge(bmin[b], bmax[b], boxes[o][0], boxes[o][1])
		}
		var right [bins]float64
		rmin, rmax := empty()
		n := 0
		for b := bins - 1; b > 0; b-- {
			n += cnt[b]
			rmin, rmax = merge(rmin, rmax, bmin[b], bmax[b])
			right[b] = float64(n) * area(rmin, rmax)
		}
		lmin, lmax := empty()
		n = 0
		for b := 0; b < bins-1; b++ {
			n += cnt[b]
			lmin, lmax = merge(lmin, lmax, bmin[b], bmax[b])
			if n == 0 || n == len(order) {
				continue
			}
			if c := float64(n)*area(lmin, lmax) + right[b+1]; c < bestCost {
				bestCost, bestAxis, bestSplit = c, axis, b
			}
		}
	}
	if bestAxis < 0 || bestCost >= float64(len(order))*area(lo, hi) && len(order) <= 2*leafSize {
		return leaf()
	}
	ext := cmax[bestAxis] - cmin[bestAxis]
	inLeft := func(o int32) bool {
		return min(int((cent[o][bestAxis]-cmin[bestAxis])/ext*bins), bins-1) <= bestSplit
	}
	i, j := 0, len(order)-1
	for i <= j {
		if inLeft(order[i]) {
			i++
		} else {
			order[i], order[j] = order[j], order[i]
			j--
		}
	}
	if i == 0 || i == len(order) {
		return leaf()
	}
	l := m.split(order[:i], start, cent, boxes)
	r := m.split(order[i:], start+int32(i), cent, boxes)
	m.nodes[idx].first, m.nodes[idx].right = l, r
	return idx
}

func merge(amin, amax, bmin, bmax Vec) (Vec, Vec) {
	for k := range 3 {
		amin[k] = min(amin[k], bmin[k])
		amax[k] = max(amax[k], bmax[k])
	}
	return amin, amax
}

// Trace appends every crossing of the ray o + t d (d a unit vector, 0 < t < maxT)
// to out, sorted by t.
func (m *Mesh) Trace(o, d Vec, maxT float64, out []Hit) []Hit {
	if len(m.nodes) == 0 {
		return out
	}
	start := len(out)
	var inv Vec
	for k := range 3 {
		inv[k] = 1 / d[k]
	}
	var buf [64]int32
	stack := append(buf[:0], 0)
	for len(stack) > 0 {
		n := &m.nodes[stack[len(stack)-1]]
		stack = stack[:len(stack)-1]
		if !slab(n.min, n.max, o, inv, maxT) {
			continue
		}
		if n.count > 0 {
			for i := n.first; i < n.first+n.count; i++ {
				t := &m.tris[i]
				if h, ok := t.hit(o, d, maxT); ok {
					out = append(out, h)
				}
			}
			continue
		}
		stack = append(stack, n.first, n.right)
	}
	hits := out[start:]
	slices.SortFunc(hits, func(a, b Hit) int {
		switch {
		case a.T < b.T:
			return -1
		case a.T > b.T:
			return 1
		}
		return 0
	})
	return out
}

func slab(lo, hi, o, inv Vec, maxT float64) bool {
	t0, t1 := 0.0, maxT
	for k := range 3 {
		a := (lo[k] - o[k]) * inv[k]
		b := (hi[k] - o[k]) * inv[k]
		if a > b {
			a, b = b, a
		}
		t0, t1 = max(t0, a), min(t1, b)
		if t0 > t1 {
			return false
		}
	}
	return true
}

// Möller-Trumbore, both faces.
func (t *tri) hit(o, d Vec, maxT float64) (Hit, bool) {
	p := cross(d, t.e2)
	det := dot(t.e1, p)
	if math.Abs(det) < 1e-14 {
		return Hit{}, false
	}
	inv := 1 / det
	s := sub(o, t.a)
	u := dot(s, p) * inv
	if u < 0 || u > 1 {
		return Hit{}, false
	}
	q := cross(s, t.e1)
	v := dot(d, q) * inv
	if v < 0 || u+v > 1 {
		return Hit{}, false
	}
	tt := dot(t.e2, q) * inv
	if tt <= 1e-9 || tt >= maxT {
		return Hit{}, false
	}
	n := t.n
	c := dot(n, d)
	if c > 0 {
		n = scale(n, -1)
	} else {
		c = -c
	}
	return Hit{T: tt, Part: t.part, N: n, Cos: min(c, 1)}, true
}

func add(a, b Vec) Vec           { return Vec{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func sub(a, b Vec) Vec           { return Vec{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func scale(a Vec, s float64) Vec { return Vec{a[0] * s, a[1] * s, a[2] * s} }
func dot(a, b Vec) float64       { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross(a, b Vec) Vec {
	return Vec{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
