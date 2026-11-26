// Package grp reads the game's GRP2 resource packs and the collision
// resources in them.
package grp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Pack is the table of a GRP2 resource pack: one entry per resource, in file order.
type Pack struct {
	Entries []Entry
}

type Entry struct {
	Name    string
	ClassID uint32
	Offset  uint32
}

// HeadSize gives how many leading bytes of a pack hold its tables, from at least its first 8 bytes.
func HeadSize(start []byte) (int, error) {
	if len(start) < 8 || string(start[:4]) != "GRP2" {
		return 0, errors.New("grp: not a GRP2 pack")
	}
	// The u32 at 4 is the size of the table area after the 16 byte header.
	return int(binary.LittleEndian.Uint32(start[4:])) + 16, nil
}

// ReadPack parses the tables of a pack from its first HeadSize bytes (or more).
func ReadPack(head []byte) (*Pack, error) {
	if len(head) < 64 || string(head[:4]) != "GRP2" {
		return nil, errors.New("grp: not a GRP2 pack")
	}
	le := binary.LittleEndian
	tab := func(at int) (int, int) {
		return int(le.Uint32(head[at:])), int(le.Uint32(head[at+4:]))
	}
	namesAt, nameCount := tab(16)
	entriesAt, entryCount := tab(32)
	if namesAt+4*nameCount > len(head) || entriesAt+12*entryCount > len(head) {
		return nil, errors.New("grp: tables out of range")
	}
	names := make([]string, nameCount)
	for i := range names {
		at := int(le.Uint32(head[namesAt+4*i:]))
		if at >= len(head) {
			return nil, errors.New("grp: name out of range")
		}
		end := bytes.IndexByte(head[at:], 0)
		if end < 0 {
			return nil, errors.New("grp: name not terminated")
		}
		names[i] = string(head[at : at+end])
	}
	p := &Pack{}
	for i := range entryCount {
		at := entriesAt + 12*i
		id := int(le.Uint16(head[at+8:]))
		if id >= len(names) {
			return nil, fmt.Errorf("grp: entry %d names %d of %d", i, id, len(names))
		}
		p.Entries = append(p.Entries, Entry{Name: names[id], ClassID: le.Uint32(head[at:]), Offset: le.Uint32(head[at+4:])})
	}
	return p, nil
}

// Span gives where a resource sits in the pack file: from off to the next
// entry's offset. end is -1 for the last entry (it runs to the end of the file).
func (p *Pack) Span(name string) (off, end int64, ok bool) {
	for i, e := range p.Entries {
		if e.Name != name {
			continue
		}
		end = -1
		if i+1 < len(p.Entries) {
			end = int64(p.Entries[i+1].Offset)
		}
		return int64(e.Offset), end, true
	}
	return 0, 0, false
}
