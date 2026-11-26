// Package game loads the ground vehicle data the shot simulation needs from a
// War Thunder install (the .vromfs.bin files) or from an unpacked datamine tree.
package game

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wrpl"
	"github.com/maxsupermanhd/wrpl-inspector/v3/wtcontent"
)

// Files gives read access to the chosen directory. Paths are slash separated
// and relative to it. A missing file or directory must give an error that wraps
// fs.ErrNotExist.
type Files interface {
	Read(name string) ([]byte, error)
	// ReadAt reads n bytes at off, or fewer at the end of the file.
	ReadAt(name string, off int64, n int) ([]byte, error)
	// List gives the names of the files in a directory.
	List(dir string) ([]string, error)
}

// Dir is Files over a local directory.
type Dir string

func (d Dir) path(name string) string { return filepath.Join(string(d), filepath.FromSlash(name)) }

func (d Dir) Read(name string) ([]byte, error) { return os.ReadFile(d.path(name)) }

func (d Dir) ReadAt(name string, off int64, n int) (_ []byte, err error) {
	f, err := os.Open(d.path(name))
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	b := make([]byte, max(0, min(int64(n), st.Size()-off)))
	got, err := f.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b[:got], nil
}

func (d Dir) List(dir string) ([]string, error) {
	entries, err := os.ReadDir(d.path(dir))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Unit is one ground vehicle of the research trees.
type Unit struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Role    string  `json:"role"`
	Rank    int     `json:"rank"`
	BR      float64 `json:"br"`
}

// Table is a slope effect or ricochet preset: curves blended by caliberToArmor.
// Each curve is stored with x = sin(angle from the surface), so it is read at cos(angle from the normal).
type Table []Row

type Row struct {
	Ratio float64
	Curve []Point
}

// Data is the loaded game data. Vehicle details load on first use.
type Data struct {
	Source  string
	Version string
	Units   map[string]Unit
	Trees   []Tree

	Slope    map[string]Table // values are 1/S
	Ricochet map[string]Table // values are probabilities
	Classes  map[string]tree

	MaxArmorScale   float64
	ArmorBreachK    float64
	ArmorResistance float64
	FillerPenMod    []Point // filler mass / shell mass -> penetration multiplier

	names  map[string]string
	files  Files
	vromfs map[string]map[string][]byte
	slims  map[string]slim

	Globals

	mu       sync.Mutex
	vehicles map[string]*Vehicle
	weapons  map[string]tree
	ptypes   map[string]tree
	packs    map[string]packRef // collision resource -> pack, once indexed

}

var groundClasses = map[string]bool{"exp_tank": true, "exp_heavy_tank": true, "exp_tank_destroyer": true, "exp_SPAA": true}

// Open finds the game data in a directory and loads the tables and the unit list.
func Open(files Files) (*Data, error) {
	d := &Data{files: files, vehicles: map[string]*Vehicle{}, weapons: map[string]tree{}, ptypes: map[string]tree{}}
	raw, err := files.Read("aces.vromfs.bin")
	switch {
	case err == nil:
		d.Source = "game install (vromfs)"
		d.vromfs = map[string]map[string][]byte{}
		d.slims = map[string]slim{}
		if err := d.mount("aces", raw); err != nil {
			return nil, err
		}
		for _, c := range []string{"char", "lang"} {
			raw, err := files.Read(c + ".vromfs.bin")
			if err != nil {
				return nil, fmt.Errorf("%s.vromfs.bin: %w", c, err)
			}
			if err := d.mount(c, raw); err != nil {
				return nil, err
			}
		}
		if yup, err := files.Read("warthunder.yup"); err == nil {
			if m := regexp.MustCompile(`7:version\d+:([0-9.]+)`).FindSubmatch(yup); m != nil {
				d.Version = string(m[1])
			}
		}
	case errors.Is(err, fs.ErrNotExist):
		if _, err := files.Read("aces.vromfs.bin_u/gamedata/damage_model/slope_effect.blkx"); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, errors.New("no game data here: pick the War Thunder install folder (with aces.vromfs.bin) or an unpacked datamine (with aces.vromfs.bin_u)")
			}
			return nil, err
		}
		d.Source = "unpacked datamine"
		if v, err := files.Read("version"); err == nil {
			d.Version = strings.TrimSpace(string(v))
		}
	default:
		return nil, fmt.Errorf("aces.vromfs.bin: %w", err)
	}

	if err := d.loadTables(); err != nil {
		return nil, err
	}
	if err := d.loadNames(); err != nil {
		return nil, err
	}
	wp, err := d.blk("char", "config/wpcost.blk")
	if err != nil {
		return nil, err
	}
	if err := d.loadUnits(wp); err != nil {
		return nil, err
	}
	if err := d.loadTrees(wp); err != nil {
		return nil, err
	}
	return d, nil
}

// slim holds what the SLIM files of a container need: the shared name map, and
// a zstd decoder with the container's dictionary.
type slim struct {
	names []string
	zstd  *zstd.Decoder
}

func (d *Data) mount(container string, raw []byte) error {
	v, err := wtcontent.ReadVROMFS(raw)
	if err != nil {
		return fmt.Errorf("%s.vromfs.bin: %w", container, err)
	}
	var s slim
	if nm, ok := v.Files["\xff?nm"]; ok {
		if s.names, err = wrpl.ParseNameMap(nm); err != nil {
			return fmt.Errorf("%s.vromfs.bin: %w", container, err)
		}
	}
	opts := []zstd.DOption{zstd.WithDecoderConcurrency(1)}
	for name, data := range v.Files {
		if strings.HasSuffix(name, ".dict") {
			opts = append(opts, zstd.WithDecoderDicts(data))
		}
	}
	if s.zstd, err = zstd.NewReader(nil, opts...); err != nil {
		return fmt.Errorf("%s.vromfs.bin: %w", container, err)
	}
	d.vromfs[container] = v.Files
	d.slims[container] = s
	return nil
}

// read gives a raw file of a container, for example ("lang", "lang/units.csv").
func (d *Data) read(container, path string) ([]byte, error) {
	if d.vromfs == nil {
		return d.files.Read(container + ".vromfs.bin_u/" + path)
	}
	b, ok := d.vromfs[container][path]
	if !ok {
		return nil, fmt.Errorf("%s.vromfs.bin: %s: %w", container, path, fs.ErrNotExist)
	}
	return b, nil
}

// blk gives a decoded .blk file of a container. Paths are matched in lower case,
// as the datamine stores them.
func (d *Data) blk(container, path string) (tree, error) {
	path = strings.ToLower(path)
	if d.vromfs == nil {
		raw, err := d.read(container, path+"x")
		if err != nil {
			return nil, err
		}
		var t tree
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return t, nil
	}
	raw, err := d.read(container, path)
	if err != nil {
		return nil, err
	}
	s := d.slims[container]
	t, err := wrpl.ParseBlkWithDecoder(raw, s.names, s.zstd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	floats(t)
	return t, nil
}

// floats turns the int64 values of a BLK tree into float64, as in the .blkx JSON.
func floats(v any) any {
	switch v := v.(type) {
	case int64:
		return float64(v)
	case map[string]any:
		for k, e := range v {
			v[k] = floats(e)
		}
	case []any:
		for i, e := range v {
			v[i] = floats(e)
		}
	}
	return v
}

func (d *Data) loadTables() error {
	dm, err := d.blk("aces", "config/damagemodel.blk")
	if err != nil {
		return err
	}
	d.MaxArmorScale = numOr(dm, "maxArmorEffectiveScale", 20)
	d.ArmorBreachK = numOr(dm, "armorBreachK", 7)
	d.ArmorResistance = numOr(dm, "armorResistance", 1900)
	if m, ok := dm["penetrationByExplosiveMassModifier"].(tree); ok {
		d.FillerPenMod = points(m, "mod")
	}

	slope, err := d.blk("aces", "gamedata/damage_model/slope_effect.blk")
	if err != nil {
		return err
	}
	d.Slope = map[string]Table{}
	for name, v := range slope {
		if preset, ok := v.(tree); ok {
			d.Slope[name] = table(preset, "slopeEffect", true)
		}
	}
	ric, err := d.blk("aces", "gamedata/damage_model/ricochet.blk")
	if err != nil {
		return err
	}
	d.Ricochet = map[string]Table{}
	for name, v := range ric {
		if preset, ok := v.(tree); ok {
			d.Ricochet[name] = table(preset, "ricochetProbability", false)
		}
	}

	classes, err := d.blk("aces", "gamedata/damage_model/armor_classes.blk")
	if err != nil {
		return err
	}
	d.Classes = map[string]tree{}
	for name, v := range classes {
		if c, ok := v.(tree); ok {
			d.Classes[name] = c
		}
	}

	expl, err := d.blk("aces", "gamedata/damage_model/explosive.blk")
	if err != nil {
		return err
	}
	return d.loadGlobals(dm, expl)
}

// table reads a preset. Its rows are the child blocks with caliberToArmor;
// a preset without rows is one row itself.
func table(preset tree, prefix string, inverse bool) Table {
	row := func(b tree) Row {
		var c []Point
		for _, p := range points(b, prefix) {
			if inverse && p.Y < 1 {
				continue
			}
			y := p.Y
			if inverse {
				y = 1 / y
			}
			c = append(c, Point{math.Sin(p.X * math.Pi / 180), y})
		}
		return Row{Ratio: numOr(b, "caliberToArmor", 1), Curve: c}
	}
	var t Table
	for _, v := range preset {
		if b, ok := v.(tree); ok {
			if _, ok := num(b, "caliberToArmor"); ok {
				t = append(t, row(b))
			}
		}
	}
	if len(t) == 0 {
		t = Table{row(preset)}
	}
	sort.Slice(t, func(i, j int) bool { return t[i].Ratio < t[j].Ratio })
	return t
}

// At reads the table at cos(angle from the normal), blending rows linearly on ratio.
func (t Table) At(cos, ratio float64) float64 {
	if len(t) == 0 {
		return 0
	}
	if ratio <= t[0].Ratio {
		return Interp(t[0].Curve, cos)
	}
	for i := 1; i < len(t); i++ {
		if ratio <= t[i].Ratio {
			a, b := t[i-1], t[i]
			f := (ratio - a.Ratio) / (b.Ratio - a.Ratio)
			return Interp(a.Curve, cos)*(1-f) + Interp(b.Curve, cos)*f
		}
	}
	return Interp(t[len(t)-1].Curve, cos)
}

func (d *Data) loadNames() error {
	d.names = map[string]string{}
	// Only the keys with the prefix are kept; menu.csv is large.
	for _, f := range []struct{ file, prefix string }{
		{"lang/units.csv", ""},
		{"lang/units_weaponry.csv", ""},
		{"lang/menu.csv", "mainmenu/type_"},
		{"lang/_common_languages.csv", "country_"},
	} {
		file := f.file
		raw, err := d.read("lang", file)
		if err != nil {
			return err
		}
		r := csv.NewReader(bytes.NewReader(raw))
		r.Comma = ';'
		r.LazyQuotes = true
		r.FieldsPerRecord = -1
		r.ReuseRecord = true
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			if len(rec) > 1 && strings.HasPrefix(rec[0], f.prefix) {
				d.names[rec[0]] = rec[1]
			}
		}
	}
	return nil
}

// roles are the ground role tags of unittags.blk, in the order that picks one
// when a unit has several (a missile carrier is also a tank destroyer).
var roles = []string{"type_light_tank", "type_medium_tank", "type_heavy_tank", "type_tank_destroyer", "type_spaa", "type_missile_tank", "type_football_tank"}

func (d *Data) loadUnits(wp tree) error {
	tags, err := d.blk("char", "config/unittags.blk")
	if err != nil {
		return err
	}
	lower := map[string]tree{}
	for id, v := range tags {
		if t, ok := v.(tree); ok {
			lower[strings.ToLower(id)] = t
		}
	}
	d.Units = map[string]Unit{}
	for id, v := range wp {
		u, ok := v.(tree)
		if !ok || !groundClasses[str(u, "unitClass")] {
			continue
		}
		rank, ok := num(u, "economicRankHistorical")
		if !ok {
			continue
		}
		name := d.names[id+"_shop"]
		if name == "" {
			name = id
		}
		var role string
		if t, ok := lower[strings.ToLower(id)]["tags"].(tree); ok {
			for _, r := range roles {
				if t[r] == true {
					role = d.names["mainmenu/"+r]
					break
				}
			}
		}
		// Keyed in lower case: wpcost.blk and shop.blk mix case, the datamine paths do not.
		d.Units[strings.ToLower(id)] = Unit{
			ID:      strings.ToLower(id),
			Name:    name,
			Country: strings.TrimPrefix(str(u, "country"), "country_"),
			Role:    role,
			Rank:    int(numOr(u, "rank", 0)),
			BR:      math.Round((rank/3+1)*10) / 10,
		}
	}
	if len(d.Units) == 0 {
		return errors.New("wpcost.blk lists no ground units")
	}
	return nil
}
