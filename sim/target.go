package sim

import (
	"math"
	"slices"

	"shotdb/game"
	"shotdb/geom"
)

type vec = geom.Vec

// span is one part crossed by a ray: entry and exit distance, the entry face normal (against the ray) and |cos|.
type span struct {
	part    int32
	in, out float64
	n       vec
	cos     float64
}

// sample is one shot line: from o along d, with the parts it crosses.
type sample struct {
	o, d  vec
	spans []span
}

// target is what the simulation keeps of a vehicle: its mesh, parts and shot lines.
type target struct {
	v      *game.Vehicle
	mesh   *geom.Mesh
	parts  []*game.Part // by mesh part index
	solid  []bool       // the node's SOLID flag: charged by the chord
	center []vec        // part centres
	views  [][]sample   // one list per view, in Views order
	crew   []int32      // crew parts by mesh index, with counts
	count  []int
	byName map[string]int32
}

func (t *target) index(name string) int32 {
	if i, ok := t.byName[name]; ok {
		return i
	}
	return -1
}

func newTarget(v *game.Vehicle, nodes []geom.Node) *target {
	t := &target{v: v, byName: map[string]int32{}}
	var tris []geom.Tri
	for i, n := range nodes {
		t.byName[n.Name] = int32(i)
		t.parts = append(t.parts, v.Parts[n.Name])
		t.solid = append(t.solid, n.Flags&geom.FlagSolid != 0)
		var c vec
		for _, p := range n.Verts {
			for k := range 3 {
				c[k] += float64(p[k]) / float64(len(n.Verts))
			}
		}
		t.center = append(t.center, c)
		at := func(k uint32) vec { p := n.Verts[k]; return vec{float64(p[0]), float64(p[1]), float64(p[2])} }
		for _, tr := range n.Tris {
			tris = append(tris, geom.Tri{A: at(tr[0]), B: at(tr[1]), C: at(tr[2]), Part: int32(i)})
		}
	}
	t.mesh = geom.Build(tris)
	for _, c := range v.Crew {
		if i := t.index(c.Part); i >= 0 {
			t.crew = append(t.crew, i)
			t.count = append(t.count, c.Count)
		}
	}
	for _, view := range Views {
		t.views = append(t.views, t.lines(view.Bearings))
	}
	return t
}

// spans appends to out the parts a ray crosses, by entry. A part's crossings
// pair up in order (in, out); a lone last crossing is a zero length span.
// hits is scratch space, kept between calls.
func (t *target) spans(o, d vec, maxT float64, hits *[]geom.Hit, out []span) []span {
	*hits = t.mesh.Trace(o, d, maxT, (*hits)[:0])
	hs := *hits
	start := len(out)
	for i, h := range hs {
		if h.Part < 0 {
			continue
		}
		next := -1
		for j := i + 1; j < len(hs); j++ {
			if hs[j].Part != h.Part {
				continue
			}
			// A ray through a shared edge or vertex hits both triangles at the same distance.
			if hs[j].T-h.T < 1e-6 {
				hs[j].Part = -1
				continue
			}
			next = j
			break
		}
		s := span{part: h.Part, in: h.T, out: h.T, n: h.N, cos: h.Cos}
		if next >= 0 {
			s.out = hs[next].T
			hs[next].Part = -1
		}
		out = append(out, s)
	}
	slices.SortStableFunc(out[start:], func(a, b span) int {
		switch {
		case a.in < b.in:
			return -1
		case a.in > b.in:
			return 1
		}
		return 0
	})
	return out
}

// Grid of the silhouette for each shot direction, and the lines kept of it.
const (
	gridCells   = 28 // cells across the larger side of the silhouette
	linesPerDir = 8
)

// Elevations of the shot line, degrees down from the horizontal.
var elevations = []float64{0, 6}

// lines picks shot lines for a view: for each bearing and elevation, a grid
// over the silhouette, of which the cells that hit the vehicle are taken at an
// even stride.
func (t *target) lines(bearings []float64) []sample {
	var out []sample
	var buf []geom.Hit
	mid := scale(add(t.mesh.Min, t.mesh.Max), 0.5)
	radius := 0.5*norm(sub(t.mesh.Max, t.mesh.Min)) + 1
	for _, b := range bearings {
		for _, e := range elevations {
			br, er := b*math.Pi/180, e*math.Pi/180
			// The shooter sits at this bearing from the nose (+x), toward +z for a positive one.
			from := vec{math.Cos(br) * math.Cos(er), math.Sin(er), math.Sin(br) * math.Cos(er)}
			d := scale(from, -1)
			u := normalize(cross(vec{0, 1, 0}, d))
			w := cross(d, u)
			umin, umax, wmin, wmax := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
			for _, c := range corners(t.mesh.Min, t.mesh.Max) {
				r := sub(c, mid)
				umin, umax = math.Min(umin, dot(r, u)), math.Max(umax, dot(r, u))
				wmin, wmax = math.Min(wmin, dot(r, w)), math.Max(wmax, dot(r, w))
			}
			step := math.Max(umax-umin, wmax-wmin) / gridCells
			var hit []sample
			for a := umin + step/2; a < umax; a += step {
				for c := wmin + step/2; c < wmax; c += step {
					o := add(add(mid, scale(from, radius)), add(scale(u, a), scale(w, c)))
					sp := t.spans(o, d, 2*radius, &buf, nil)
					if len(sp) > 0 {
						hit = append(hit, sample{o: o, d: d, spans: sp})
					}
				}
			}
			n := min(linesPerDir, len(hit))
			for i := range n {
				// Centre of each of n equal runs of the hit cells.
				out = append(out, hit[(2*i+1)*len(hit)/(2*n)])
			}
		}
	}
	return out
}

func corners(lo, hi vec) []vec {
	var out []vec
	for _, x := range []float64{lo[0], hi[0]} {
		for _, y := range []float64{lo[1], hi[1]} {
			for _, z := range []float64{lo[2], hi[2]} {
				out = append(out, vec{x, y, z})
			}
		}
	}
	return out
}

func add(a, b vec) vec              { return vec{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func sub(a, b vec) vec              { return vec{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func scale(a vec, s float64) vec    { return vec{a[0] * s, a[1] * s, a[2] * s} }
func dot(a, b vec) float64          { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func norm(a vec) float64            { return math.Sqrt(dot(a, a)) }
func normalize(a vec) vec           { return scale(a, 1/norm(a)) }
func along(o, d vec, t float64) vec { return add(o, scale(d, t)) }
func cross(a, b vec) vec {
	return vec{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
