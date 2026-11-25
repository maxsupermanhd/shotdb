package geom

import (
	"math"
	"testing"
)

// A ray through a box of many slabs meets every face once, in order, with the right angle.
func TestTrace(t *testing.T) {
	var tris []Tri
	quad := func(x float64, part int32) {
		a, b, c, d := Vec{x, -1, -1}, Vec{x, 1, -1}, Vec{x, 1, 1}, Vec{x, -1, 1}
		tris = append(tris, Tri{A: a, B: b, C: c, Part: part}, Tri{A: a, B: c, C: d, Part: part})
	}
	for i := range 40 {
		quad(float64(i), int32(i))
	}
	m := Build(tris)
	n := math.Sqrt(1 + 0.002*0.002 + 0.105*0.105)
	d := Vec{1 / n, 0.002 / n, 0.105 / n}
	hits := m.Trace(Vec{-1, 0, 0}, d, 100, nil)
	// The ray leaves the box sideways (|z| > 1) after about 10 slabs.
	want := 0
	for i := range 40 {
		if z := (float64(i) + 1) / d[0] * d[2]; math.Abs(z) < 1 {
			want++
		}
	}
	if len(hits) != want {
		t.Fatalf("%d hits, want %d", len(hits), want)
	}
	for i, h := range hits {
		if h.Part != int32(i) || math.Abs(h.T-(float64(i)+1)/d[0]) > 1e-9 || math.Abs(h.Cos-d[0]) > 1e-9 || h.N[0] != -1 {
			t.Errorf("hit %d: %+v", i, h)
		}
	}
}
