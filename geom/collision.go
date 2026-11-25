package geom

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/klauspost/compress/zstd"
)

// Collision resource class ids.
const (
	ClassCollision     = 0xACE50000 // raw node list
	ClassCollisionZstd = 0xACE50001 // the same nodes, zstd packed, 4-float vertices and u16 indices
	ClassCollisionV3   = 0xACE50003 // zstd packed column tables, one quantised BVH per node mesh
)

// CollisionNode behaviour flags.
const (
	FlagTraceable = 0x1
	FlagPhys      = 0x2
	FlagSolid     = 0x4
)

// Node is one collision node: for a vehicle, one damage part. Vertices are in
// the unit's native frame (+x forward, +y up, +z right), in metres.
type Node struct {
	Name  string
	Flags uint32 // behaviour flags; FlagSolid marks a part charged by its chord
	Verts [][3]float32
	Tris  [][3]uint32
}

// ParseCollision decodes a collision resource.
func ParseCollision(raw []byte) ([]Node, error) {
	if len(raw) < 12 {
		return nil, errors.New("collision: too short")
	}
	switch id := binary.LittleEndian.Uint32(raw); id {
	case ClassCollision:
		return parseRaw(raw)
	case ClassCollisionZstd, ClassCollisionV3:
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1<<28))
		if err != nil {
			return nil, err
		}
		defer dec.Close()
		// The u32 at 4 is a Dagor compressed block head: the size in the low 24
		// bits and the method in the top byte (0x40 zstd, 0 stored).
		head := binary.LittleEndian.Uint32(raw[4:])
		size := int(head & 0xFFFFFF)
		if 8+size > len(raw) || (head>>24 != 0x40 && head>>24 != 0) {
			return nil, fmt.Errorf("collision: block head 0x%08x does not fit", head)
		}
		payload := raw[8 : 8+size]
		if head>>24 == 0x40 {
			var err error
			if payload, err = dec.DecodeAll(payload, nil); err != nil {
				return nil, fmt.Errorf("collision: %w", err)
			}
		}
		if id == ClassCollisionV3 {
			return parseV3(payload)
		}
		return parseZstd(payload)
	default:
		return nil, fmt.Errorf("collision: class 0x%08x is not supported", id)
	}
}

type cursor struct {
	b   []byte
	pos int
	err error
}

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || c.pos+n > len(c.b) {
		c.err = fmt.Errorf("collision: read of %d bytes at 0x%x past the end (0x%x)", n, c.pos, len(c.b))
		return nil
	}
	s := c.b[c.pos : c.pos+n]
	c.pos += n
	return s
}

func (c *cursor) u32() uint32 {
	s := c.take(4)
	if s == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(s)
}

func (c *cursor) f32() float32 { return math.Float32frombits(c.u32()) }

// str reads a u32 length, the bytes, and pads to 4.
func (c *cursor) str() string {
	n := int(c.u32())
	s := c.take(n)
	c.take((4 - n&3) & 3)
	return string(s)
}

func transform(tm [12]float32, v [3]float32) [3]float32 {
	return [3]float32{
		v[0]*tm[0] + v[1]*tm[3] + v[2]*tm[6] + tm[9],
		v[0]*tm[1] + v[1]*tm[4] + v[2]*tm[7] + tm[10],
		v[0]*tm[2] + v[1]*tm[5] + v[2]*tm[8] + tm[11],
	}
}

// Versions of the raw format that carry a physical material name, and collision flags.
const (
	rawVersionPhysMat = 538247445
	rawVersionFlags   = 538445072
)

func parseRaw(raw []byte) ([]Node, error) {
	c := &cursor{b: raw, pos: 4}
	version := c.u32()
	c.take(4 + 20) // the size of the bounding sphere, then the sphere
	size := int(c.u32())
	if c.err != nil {
		return nil, c.err
	}
	if c.pos+size > len(raw) {
		return nil, errors.New("collision: node block past the end")
	}
	c = &cursor{b: raw[c.pos : c.pos+size]}
	var flags uint32
	if version >= rawVersionFlags {
		flags = c.u32()
	}
	count := int(c.u32())
	if count <= 0 || count > 10000 {
		return nil, fmt.Errorf("collision: %d nodes", count)
	}
	nodes := make([]Node, 0, count)
	for range count {
		n := Node{Name: c.str()}
		if version >= rawVersionPhysMat {
			c.str()
		}
		c.u32() // collision type
		if flags&2 != 0 {
			if b := c.take(1); b != nil {
				n.Flags = uint32(b[0])
			}
		}
		var tm [12]float32
		for i := range tm {
			tm[i] = c.f32()
		}
		if flags&4 != 0 {
			c.take(48)
		}
		c.take(20 + 24) // bounding sphere and box
		nv := int(c.u32())
		if c.err != nil {
			return nil, c.err
		}
		if nv > 1<<20 {
			return nil, fmt.Errorf("collision: %s has %d vertices", n.Name, nv)
		}
		n.Verts = make([][3]float32, nv)
		for i := range n.Verts {
			n.Verts[i] = transform(tm, [3]float32{c.f32(), c.f32(), c.f32()})
		}
		ni := int(c.u32())
		if ni%3 != 0 || ni > 3<<20 {
			return nil, fmt.Errorf("collision: %s has %d indices", n.Name, ni)
		}
		n.Tris = make([][3]uint32, ni/3)
		for i := range n.Tris {
			n.Tris[i] = [3]uint32{c.u32(), c.u32(), c.u32()}
		}
		if c.err != nil {
			return nil, c.err
		}
		nodes = append(nodes, n)
	}
	if c.pos != len(c.b) {
		return nil, fmt.Errorf("collision: %d bytes left after the nodes", len(c.b)-c.pos)
	}
	return nodes, checkIndices(nodes)
}

func parseZstd(p []byte) ([]Node, error) {
	if len(p) < 84 {
		return nil, errors.New("collision: payload too short")
	}
	c := &cursor{b: p, pos: 76}
	c.u32() // flags
	count := int(c.u32())
	if count <= 0 || count > 10000 {
		return nil, fmt.Errorf("collision: %d nodes", count)
	}
	nodes := make([]Node, 0, count)
	for range count {
		n := Node{Name: c.str()}
		c.str() // physical material
		head := c.take(104)
		if head == nil {
			return nil, c.err
		}
		var tm [12]float32
		for i := range tm {
			tm[i] = math.Float32frombits(binary.LittleEndian.Uint32(head[52+4*i:]))
		}
		nv := int(c.u32())
		if c.err != nil {
			return nil, c.err
		}
		if nv > 1<<20 {
			return nil, fmt.Errorf("collision: %s has %d vertices", n.Name, nv)
		}
		n.Verts = make([][3]float32, nv)
		for i := range n.Verts {
			v := [3]float32{c.f32(), c.f32(), c.f32()}
			c.f32()
			n.Verts[i] = transform(tm, v)
		}
		ni := int(c.u32())
		if ni%3 != 0 || ni > 3<<20 {
			return nil, fmt.Errorf("collision: %s has %d indices", n.Name, ni)
		}
		idx := c.take(2 * ni)
		if idx == nil {
			return nil, c.err
		}
		n.Tris = make([][3]uint32, ni/3)
		for i := range n.Tris {
			for k := range 3 {
				n.Tris[i][k] = uint32(binary.LittleEndian.Uint16(idx[2*(3*i+k):]))
			}
		}
		nodes = append(nodes, n)
	}
	if c.pos != len(p) {
		return nil, fmt.Errorf("collision: %d bytes left after the nodes", len(p)-c.pos)
	}
	return nodes, checkIndices(nodes)
}

func checkIndices(nodes []Node) error {
	for _, n := range nodes {
		for _, t := range n.Tris {
			for _, i := range t {
				if int(i) >= len(n.Verts) {
					return fmt.Errorf("collision: %s: index %d of %d vertices", n.Name, i, len(n.Verts))
				}
			}
		}
	}
	return nil
}
