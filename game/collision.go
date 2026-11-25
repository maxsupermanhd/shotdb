package game

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"shotdb/geom"
)

// TanksDir holds the resource packs of ground vehicles, in the install.
const TanksDir = "content/base/res/tanks"

type packRef struct {
	file     string
	off, end int64
}

func (d *Data) packTable(file string) (*geom.Pack, error) {
	path := TanksDir + "/" + file
	head, err := d.files.ReadAt(path, 0, 16<<10)
	if err != nil {
		return nil, err
	}
	n, err := geom.HeadSize(head)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if n > len(head) {
		if head, err = d.files.ReadAt(path, 0, n); err != nil {
			return nil, err
		}
	}
	p, err := geom.ReadPack(head)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func refIn(p *geom.Pack, file, key string) (packRef, bool) {
	for _, e := range p.Entries {
		if strings.EqualFold(e.Name, key) {
			off, end, _ := p.Span(e.Name)
			return packRef{file, off, end}, true
		}
	}
	return packRef{}, false
}

// indexPacks maps every collision resource of the tank packs to its pack.
func (d *Data) indexPacks() error {
	files, err := d.files.List(TanksDir)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no %s here: the collision models are in the game install, open that folder", TanksDir)
	}
	if err != nil {
		return err
	}
	d.packs = map[string]packRef{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".grp") {
			continue
		}
		p, err := d.packTable(f)
		if err != nil {
			return err
		}
		for _, e := range p.Entries {
			if strings.HasSuffix(e.Name, "_collision") {
				d.packs[strings.ToLower(e.Name)], _ = refIn(p, f, e.Name)
			}
		}
	}
	return nil
}

// collision loads the collision nodes of a unit's model. Most units sit in a
// pack named after them; the others are found through an index of all packs.
func (d *Data) collision(unit, model string) ([]geom.Node, error) {
	key := strings.ToLower(model) + "_collision"
	ref, ok := d.packs[key]
	if !ok {
		p, err := d.packTable(unit + ".grp")
		switch {
		case err == nil:
			ref, ok = refIn(p, unit+".grp", key)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
	}
	if !ok && d.packs == nil {
		if err := d.indexPacks(); err != nil {
			return nil, err
		}
		ref, ok = d.packs[key]
	}
	if !ok {
		return nil, fmt.Errorf("%s: no collision model %s in %s", unit, key, TanksDir)
	}
	n := 64 << 20
	if ref.end >= 0 {
		n = int(ref.end - ref.off)
	}
	raw, err := d.files.ReadAt(TanksDir+"/"+ref.file, ref.off, n)
	if err != nil {
		return nil, err
	}
	nodes, err := geom.ParseCollision(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", unit, key, err)
	}
	return nodes, nil
}
