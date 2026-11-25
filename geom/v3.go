package geom

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The 0xACE50003 payload: a 108 byte header (11 section counts at 64), node
// meta (8 bytes each), node records (48), node transforms (48), two more
// 48 byte tables, one mesh per node with indices, then small tables, the node
// name table and the material name table.
const (
	v3Header   = 108
	blasMagic  = 0x694C4264
	blasHeader = 44
	blasLeaf   = 28
	blasInner  = 16
)

func parseV3(p []byte) ([]Node, error) {
	if len(p) < v3Header {
		return nil, errors.New("collision: payload too short")
	}
	le := binary.LittleEndian
	var counts [11]int
	for i := range counts {
		counts[i] = int(le.Uint32(p[64+4*i:]))
	}
	n := counts[0]
	if n <= 0 || n > 10000 || counts[1] != n {
		return nil, fmt.Errorf("collision: %d nodes, %d transforms", n, counts[1])
	}
	records := v3Header + 8*n
	tms := records + 48*n
	pos := tms + 48*(counts[1]+counts[2]+counts[3])
	if pos > len(p) {
		return nil, errors.New("collision: node tables past the end")
	}
	nodes := make([]Node, n)
	for i := range nodes {
		rec := records + 48*i
		indices := int(le.Uint32(p[rec+4:]))
		nodes[i].Flags = uint32(le.Uint16(p[rec+8:]))
		if indices == 0 {
			continue
		}
		if pos+36 > len(p) {
			return nil, errors.New("collision: mesh header past the end")
		}
		var inv, origin [3]float64
		for k := range 3 {
			inv[k] = float64(math.Float32frombits(le.Uint32(p[pos+12+4*k:])))
			origin[k] = float64(math.Float32frombits(le.Uint32(p[pos+24+4*k:])))
		}
		pos += 36
		tris, raw, next, err := readBLAS(p, pos)
		if err != nil {
			return nil, fmt.Errorf("collision: node %d: %w", i, err)
		}
		if len(tris)*3 != indices {
			return nil, fmt.Errorf("collision: node %d: %d triangles, record says %d", i, len(tris), indices/3)
		}
		var tm [12]float32
		for k := range tm {
			tm[k] = math.Float32frombits(le.Uint32(p[tms+48*i+4*k:]))
		}
		verts := make([][3]float32, len(raw))
		for k, r := range raw {
			v := [3]float32{
				float32(origin[0] + float64(r&0x1FFFFF)/32*inv[0]),
				float32(origin[1] + float64((r>>21)&0x1FFFFF)/32*inv[1]),
				float32(origin[2] + float64(r>>42)/32*inv[2]),
			}
			verts[k] = transform(tm, v)
		}
		nodes[i].Verts, nodes[i].Tris = verts, tris
		pos = next
	}
	pos += 2*counts[6] + 28*counts[7] + 16*counts[8]
	if pos+counts[9] > len(p) {
		return nil, errors.New("collision: name table past the end")
	}
	names := p[pos : pos+counts[9]]
	for i := range nodes {
		at := int(le.Uint32(p[records+48*i:]))
		if at >= len(names) {
			return nil, fmt.Errorf("collision: node %d name out of range", i)
		}
		end := bytes.IndexByte(names[at:], 0)
		if end < 0 {
			return nil, fmt.Errorf("collision: node %d name not terminated", i)
		}
		nodes[i].Name = string(names[at : at+end])
	}
	return nodes, checkIndices(nodes)
}

// readBLAS reads one serialised mesh BVH: its 21-bit-per-axis vertices and the
// triangles of its leaves. It returns the position after the per-leaf tags.
func readBLAS(p []byte, pos int) ([][3]uint32, []uint64, int, error) {
	le := binary.LittleEndian
	if pos+blasHeader > len(p) {
		return nil, nil, 0, errors.New("BVH header past the end")
	}
	if le.Uint32(p[pos:]) != blasMagic || p[pos+6] != 8 || p[pos+7] != blasLeaf {
		return nil, nil, 0, fmt.Errorf("bad BVH header at 0x%x", pos)
	}
	treeBytes := int(le.Uint32(p[pos+8:]))
	nv := int(le.Uint32(p[pos+12:]))
	at := pos + blasHeader
	if nv == 0 || at+8*nv > len(p) {
		return nil, nil, 0, errors.New("BVH vertices past the end")
	}
	raw := make([]uint64, nv)
	for i := range raw {
		raw[i] = le.Uint64(p[at+8*i:])
	}
	cur := at + 8*nv
	consumed, leaves := 0, 0
	var tris [][3]uint32
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > 256 || cur >= len(p) {
			return errors.New("BVH tree malformed")
		}
		m := p[cur]
		cur++
		switch m {
		case 0, 0xFD, 0xFE, 0xFF:
			var w [4]uint32
			if m == 0xFD || m == 0xFE {
				if cur+8 > len(p) {
					return errors.New("BVH leaf past the end")
				}
				w[0], w[1] = le.Uint32(p[cur:]), le.Uint32(p[cur+4:])
				cur += 8
				if m == 0xFD {
					w[2] = 1 << 29
				}
			} else {
				if cur+16 > len(p) {
					return errors.New("BVH leaf past the end")
				}
				for k := range w {
					w[k] = le.Uint32(p[cur+4*k:])
				}
				cur += 16
			}
			consumed += blasLeaf
			leaves++
			if m != 0xFF {
				tris = appendLeaf(tris, w)
			}
			return nil
		}
		consumed += blasInner
		for range int(m) {
			if err := walk(depth + 1); err != nil {
				return err
			}
		}
		return nil
	}
	for consumed < treeBytes {
		if err := walk(0); err != nil {
			return nil, nil, 0, err
		}
	}
	if consumed != treeBytes {
		return nil, nil, 0, fmt.Errorf("BVH tree is %d bytes, header says %d", consumed, treeBytes)
	}
	// One u16 per leaf: the edge mask of its triangles.
	end := cur + 2*leaves
	if end > len(p) {
		return nil, nil, 0, errors.New("BVH leaf tags past the end")
	}
	for _, t := range tris {
		for _, v := range t {
			if int(v) >= nv {
				return nil, nil, 0, errors.New("BVH triangle past the vertices")
			}
		}
	}
	return tris, raw, end, nil
}

func signExtend(v uint32, bits uint) int64 {
	v &= 1<<bits - 1
	if v>>(bits-1) != 0 {
		return int64(v) - 1<<bits
	}
	return int64(v)
}

// appendLeaf expands a leaf of up to four triangles: two strips of two, each
// with a base vertex and 13-bit signed offsets.
func appendLeaf(tris [][3]uint32, w [4]uint32) [][3]uint32 {
	base := int64(w[1] & 0xFFFFFF)
	o1 := signExtend(w[0], 13)
	o2 := signExtend(w[0]>>13, 13)
	o3 := signExtend((w[0]>>26)&31|(w[1]>>24)<<5, 13)
	deltaB := int64(w[2] & 0xFFFF)
	o1b := signExtend(w[2]>>16, 13)
	o2b := signExtend(w[3], 13)
	o3b := signExtend(w[3]>>13, 13)
	flipA, flipB := w[2]&(1<<29) != 0, w[2]&(1<<30) != 0
	tri := func(a, b, c int64) [3]uint32 { return [3]uint32{uint32(a), uint32(b), uint32(c)} }
	tris = append(tris, tri(base, base+o1, base+o2))
	if o3 != o2 {
		if flipA {
			tris = append(tris, tri(base+o2, base+o1, base+o3))
		} else {
			tris = append(tris, tri(base+o1, base+o2, base+o3))
		}
	}
	if o1b != o2b {
		b := base + deltaB
		tris = append(tris, tri(b, b+o1b, b+o2b))
		if o3b != o2b {
			if flipB {
				tris = append(tris, tri(b+o2b, b+o1b, b+o3b))
			} else {
				tris = append(tris, tri(b+o1b, b+o2b, b+o3b))
			}
		}
	}
	return tris
}
