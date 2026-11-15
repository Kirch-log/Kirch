package kicad

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Kirch-log/kirch/internal/canon"
)

// Schematic is the drawing of a KiCad design: where every part, wire and
// label is, so the website can draw the schematic the way it was drawn in
// KiCad. Coordinates are KiCad's: millimetres, x to the right, y down.
type Schematic struct {
	Source string            // where it came from, e.g. "hw/servo-board.kicad_sch"
	Main   string            // file name of the top sheet (a key of Sheets)
	Sheets map[string]*Sheet // by file name, e.g. "servo-board.kicad_sch"
}

// Sheet is one schematic page.
type Sheet struct {
	Parts     []*PlacedPart
	Power     []*PowerPort
	Wires     [][4]float64 // x1, y1, x2, y2
	Buses     [][4]float64
	Junctions [][2]float64
	NoConnect [][2]float64
	Labels    []*Label
	SubSheets []*SubSheet
}

// PlacedPart is one placed symbol (one unit of a multi-unit chip).
type PlacedPart struct {
	Ref      string
	Unit     int
	Value    string
	At       [2]float64
	Rotation float64
	Mirror   string // "", "x" or "y"
	Pins     map[string]*PlacedPin
	Shapes   []Shape
	Box      [4]float64 // min x, min y, max x, max y
}

// PlacedPin is a pin: At is where wires connect, End where the pin meets the body.
type PlacedPin struct {
	At, End [2]float64
	Name    string
}

// Shape is part of a symbol's drawing, already placed on the page.
type Shape struct {
	Kind   string       // "poly" (closed if first == last), "circle", "arc"
	Points [][2]float64 // poly: corners; circle: [centre, (radius, 0)]; arc: start, mid, end
}

// PowerPort is a power symbol (GND, +5V): it connects every wire touching it to
// the net of that name.
type PowerPort struct {
	Name     string
	At       [2]float64
	Rotation float64
}

// Label names a wire (and joins every wire with the same name).
type Label struct {
	Text     string
	Kind     string // "label", "global", "hierarchical"
	At       [2]float64
	Rotation float64
}

// SubSheet is a box on this page that stands for another sheet.
type SubSheet struct {
	Name, File string
	At, Size   [2]float64
}

// ReadSchematic reads a .kicad_sch and every sub-sheet it uses (files next
// to it).
func ReadSchematic(file string) (*Schematic, error) {
	s := &Schematic{Source: filepath.Base(file), Main: filepath.Base(file), Sheets: map[string]*Sheet{}}
	if err := s.read(filepath.Dir(file), filepath.Base(file), 0); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Schematic) read(dir, name string, depth int) error {
	if _, done := s.Sheets[name]; done || depth > 20 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	sheet, err := ParseSheet(data)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	s.Sheets[name] = sheet
	for _, sub := range sheet.SubSheets {
		if sub.File != "" && !strings.ContainsAny(sub.File, `/\`) {
			if err := s.read(dir, sub.File, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// ParseSheet reads one .kicad_sch file.
func ParseSheet(data []byte) (*Sheet, error) {
	root, err := parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("not a KiCad schematic: %w", err)
	}
	if root.head() != "kicad_sch" {
		return nil, fmt.Errorf("not a KiCad schematic (it should start with \"(kicad_sch\")")
	}
	lib := map[string]*node{}
	if ls := root.child("lib_symbols"); ls != nil {
		for _, sym := range ls.children("symbol") {
			if len(sym.List) > 1 {
				lib[sym.List[1].Atom] = sym
			}
		}
	}
	sh := &Sheet{}
	for _, item := range root.List {
		switch item.head() {
		case "symbol":
			placeSymbol(sh, item, lib)
		case "wire", "bus":
			pts := points(item.child("pts"))
			for i := 0; i+1 < len(pts); i++ {
				seg := [4]float64{pts[i][0], pts[i][1], pts[i+1][0], pts[i+1][1]}
				if item.head() == "wire" {
					sh.Wires = append(sh.Wires, seg)
				} else {
					sh.Buses = append(sh.Buses, seg)
				}
			}
		case "junction":
			sh.Junctions = append(sh.Junctions, xy(item.child("at")))
		case "no_connect":
			sh.NoConnect = append(sh.NoConnect, xy(item.child("at")))
		case "label", "global_label", "hierarchical_label":
			kind := map[string]string{"label": "label", "global_label": "global", "hierarchical_label": "hierarchical"}[item.head()]
			if len(item.List) > 1 {
				sh.Labels = append(sh.Labels, &Label{Text: unescape(item.List[1].Atom), Kind: kind, At: xy(item.child("at")), Rotation: angle(item.child("at"))})
			}
		case "sheet":
			sub := &SubSheet{At: xy(item.child("at")), Size: xy(item.child("size"))}
			for _, p := range item.children("property") {
				if len(p.List) < 3 {
					continue
				}
				switch p.List[1].Atom {
				case "Sheetname", "Sheet name":
					sub.Name = p.List[2].Atom
				case "Sheetfile", "Sheet file":
					sub.File = p.List[2].Atom
				}
			}
			sh.SubSheets = append(sh.SubSheets, sub)
		}
	}
	sh.sort()
	return sh, nil
}

// placeSymbol adds one placed symbol: a part, or a power port (#PWR).
func placeSymbol(sh *Sheet, item *node, lib map[string]*node) {
	libID := item.value("lib_id")
	at := item.child("at")
	pos, rot := xy(at), angle(at)
	mirror := item.value("mirror")
	unit, _ := strconv.Atoi(item.value("unit"))
	if unit == 0 {
		unit = 1
	}
	style, _ := strconv.Atoi(item.value("body_style"))
	if style == 0 {
		style, _ = strconv.Atoi(item.value("convert"))
	}
	if style == 0 {
		style = 1
	}
	ref, value := "", ""
	for _, p := range item.children("property") {
		if len(p.List) < 3 {
			continue
		}
		switch p.List[1].Atom {
		case "Reference":
			ref = p.List[2].Atom
		case "Value":
			value = unescape(p.List[2].Atom)
		}
	}
	// In a sheet used more than once the real reference is per instance.
	if inst := item.child("instances"); inst != nil {
		for _, proj := range inst.children("project") {
			for _, path := range proj.children("path") {
				if r := path.value("reference"); r != "" {
					ref = r
					break
				}
			}
		}
	}
	t := transform{origin: pos, rotation: rot, mirror: mirror}
	if strings.HasPrefix(ref, "#") { // power symbol: GND, +5V…
		if strings.HasPrefix(ref, "#PWR") || strings.HasPrefix(libID, "power:") {
			sh.Power = append(sh.Power, &PowerPort{Name: value, At: pos, Rotation: rot})
		}
		return
	}
	part := &PlacedPart{Ref: ref, Unit: unit, Value: value, At: pos, Rotation: rot, Mirror: mirror, Pins: map[string]*PlacedPin{}}
	if def := lib[libID]; def != nil {
		for _, sub := range def.children("symbol") {
			if len(sub.List) < 2 {
				continue
			}
			u, st, ok := unitStyle(sub.List[1].Atom)
			if !ok || (u != 0 && u != unit) || (st != 0 && st != style) {
				continue
			}
			addGraphics(part, sub, t)
		}
	}
	part.Box = bounds(part)
	sh.Parts = append(sh.Parts, part)
}

// unitStyle reads "R_1_1" → unit 1, body style 1.
func unitStyle(name string) (unit, style int, ok bool) {
	i := strings.LastIndexByte(name, '_')
	if i < 0 {
		return 0, 0, false
	}
	j := strings.LastIndexByte(name[:i], '_')
	if j < 0 {
		return 0, 0, false
	}
	u, err1 := strconv.Atoi(name[j+1 : i])
	s, err2 := strconv.Atoi(name[i+1:])
	return u, s, err1 == nil && err2 == nil
}

func addGraphics(part *PlacedPart, sub *node, t transform) {
	for _, g := range sub.List {
		switch g.head() {
		case "pin":
			at := g.child("at")
			tip := xy(at)
			length := num(g.child("length"))
			dir := angle(at) * math.Pi / 180
			end := [2]float64{tip[0] + length*math.Cos(dir), tip[1] + length*math.Sin(dir)}
			number := g.value("number")
			if number == "" {
				continue
			}
			name := unescape(g.value("name"))
			if name == "~" {
				name = ""
			}
			part.Pins[number] = &PlacedPin{At: t.apply(tip), End: t.apply(end), Name: name}
		case "rectangle":
			a, b := xy(g.child("start")), xy(g.child("end"))
			part.Shapes = append(part.Shapes, Shape{Kind: "poly", Points: t.all([][2]float64{a, {b[0], a[1]}, b, {a[0], b[1]}, a})})
		case "polyline":
			part.Shapes = append(part.Shapes, Shape{Kind: "poly", Points: t.all(points(g.child("pts")))})
		case "circle":
			c := xy(g.child("center"))
			part.Shapes = append(part.Shapes, Shape{Kind: "circle", Points: [][2]float64{t.apply(c), {num(g.child("radius")), 0}}})
		case "arc":
			part.Shapes = append(part.Shapes, Shape{Kind: "arc", Points: t.all([][2]float64{xy(g.child("start")), xy(g.child("mid")), xy(g.child("end"))})})
		}
	}
}

// transform places a point of a library symbol on the page. Library symbols
// are drawn with y up; the page has y down. Then the symbol is mirrored,
// rotated (counter-clockwise on screen) and moved to its position.
type transform struct {
	origin   [2]float64
	rotation float64
	mirror   string
}

func (t transform) apply(p [2]float64) [2]float64 {
	x, y := p[0], -p[1] // y up → y down
	switch t.mirror {
	case "x": // mirrored around the x axis: flips up / down
		y = -y
	case "y": // around the y axis: flips left / right
		x = -x
	}
	a := t.rotation * math.Pi / 180
	cos, sin := math.Round(math.Cos(a)*1e9)/1e9, math.Round(math.Sin(a)*1e9)/1e9
	rx := x*cos + y*sin
	ry := -x*sin + y*cos
	return [2]float64{round(t.origin[0] + rx), round(t.origin[1] + ry)}
}

func (t transform) all(ps [][2]float64) [][2]float64 {
	out := make([][2]float64, len(ps))
	for i, p := range ps {
		out[i] = t.apply(p)
	}
	return out
}

func bounds(p *PlacedPart) [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	grow := func(q [2]float64) {
		b[0], b[1] = math.Min(b[0], q[0]), math.Min(b[1], q[1])
		b[2], b[3] = math.Max(b[2], q[0]), math.Max(b[3], q[1])
	}
	for _, s := range p.Shapes {
		if s.Kind == "circle" {
			r := s.Points[1][0]
			grow([2]float64{s.Points[0][0] - r, s.Points[0][1] - r})
			grow([2]float64{s.Points[0][0] + r, s.Points[0][1] + r})
			continue
		}
		for _, q := range s.Points {
			grow(q)
		}
	}
	for _, pin := range p.Pins {
		grow(pin.At)
		grow(pin.End)
	}
	if math.IsInf(b[0], 1) {
		return [4]float64{p.At[0], p.At[1], p.At[0], p.At[1]}
	}
	for i := range b {
		b[i] = round(b[i])
	}
	return b
}

func (sh *Sheet) sort() {
	sort.Slice(sh.Parts, func(i, j int) bool {
		if sh.Parts[i].Ref != sh.Parts[j].Ref {
			return pinLess(sh.Parts[i].Ref, sh.Parts[j].Ref) // R2 before R10 (same rule as pins)
		}
		return sh.Parts[i].Unit < sh.Parts[j].Unit
	})
	less4 := func(s [][4]float64) func(i, j int) bool {
		return func(i, j int) bool {
			for k := 0; k < 4; k++ {
				if s[i][k] != s[j][k] {
					return s[i][k] < s[j][k]
				}
			}
			return false
		}
	}
	sort.Slice(sh.Wires, less4(sh.Wires))
	sort.Slice(sh.Buses, less4(sh.Buses))
	for _, s := range [][][2]float64{sh.Junctions, sh.NoConnect} {
		sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] || s[i][0] == s[j][0] && s[i][1] < s[j][1] })
	}
	sort.SliceStable(sh.Power, func(i, j int) bool {
		a, b := sh.Power[i], sh.Power[j]
		return a.Name < b.Name || a.Name == b.Name && (a.At[0] < b.At[0] || a.At[0] == b.At[0] && a.At[1] < b.At[1])
	})
	sort.SliceStable(sh.Labels, func(i, j int) bool {
		a, b := sh.Labels[i], sh.Labels[j]
		return a.Text < b.Text || a.Text == b.Text && (a.At[0] < b.At[0] || a.At[0] == b.At[0] && a.At[1] < b.At[1])
	})
}

// File returns schematic.json (docs/formats.md), canonical.
func (s *Schematic) File() ([]byte, error) {
	sheets := map[string]any{}
	for name, sh := range s.Sheets {
		parts := []any{}
		for _, p := range sh.Parts {
			pins := map[string]any{}
			for n, pin := range p.Pins {
				e := map[string]any{"at": pt(pin.At), "end": pt(pin.End)}
				if pin.Name != "" {
					e["name"] = pin.Name
				}
				pins[n] = e
			}
			shapes := []any{}
			for _, sh := range p.Shapes {
				pts := make([]any, len(sh.Points))
				for i, q := range sh.Points {
					pts[i] = pt(q)
				}
				shapes = append(shapes, map[string]any{"kind": sh.Kind, "points": pts})
			}
			e := map[string]any{
				"ref": p.Ref, "unit": p.Unit, "at": pt(p.At), "rotation": n(p.Rotation),
				"box":  []any{n(p.Box[0]), n(p.Box[1]), n(p.Box[2]), n(p.Box[3])},
				"pins": pins, "shapes": shapes,
			}
			if p.Value != "" {
				e["value"] = p.Value
			}
			if p.Mirror != "" {
				e["mirror"] = p.Mirror
			}
			parts = append(parts, e)
		}
		page := map[string]any{"parts": parts}
		add := func(key string, v []any) {
			if len(v) > 0 {
				page[key] = v
			}
		}
		add("wires", segs(sh.Wires))
		add("buses", segs(sh.Buses))
		add("junctions", pts(sh.Junctions))
		add("no_connects", pts(sh.NoConnect))
		var power, labels, subs []any
		for _, p := range sh.Power {
			power = append(power, map[string]any{"name": p.Name, "at": pt(p.At), "rotation": n(p.Rotation)})
		}
		for _, l := range sh.Labels {
			labels = append(labels, map[string]any{"text": l.Text, "kind": l.Kind, "at": pt(l.At), "rotation": n(l.Rotation)})
		}
		for _, sub := range sh.SubSheets {
			subs = append(subs, map[string]any{"name": sub.Name, "file": sub.File, "at": pt(sub.At), "size": pt(sub.Size)})
		}
		add("power", power)
		add("labels", labels)
		add("sheets", subs)
		sheets[name] = page
	}
	doc := map[string]any{"format": "kirch.schematic/1", "units": "mm", "main": s.Main, "sheets": sheets}
	if s.Source != "" {
		doc["source"] = "kicad:" + s.Source
	}
	return canon.Format("schematic.json", mustJSON(doc))
}

// --- small helpers -------------------------------------------------------

func num(n *node) float64 {
	if n == nil || len(n.List) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(n.List[1].Atom, 64)
	return v
}

func xy(n *node) [2]float64 {
	if n == nil || len(n.List) < 3 {
		return [2]float64{}
	}
	x, _ := strconv.ParseFloat(n.List[1].Atom, 64)
	y, _ := strconv.ParseFloat(n.List[2].Atom, 64)
	return [2]float64{x, y}
}

func angle(at *node) float64 {
	if at == nil || len(at.List) < 4 {
		return 0
	}
	a, _ := strconv.ParseFloat(at.List[3].Atom, 64)
	return a
}

func points(pts *node) [][2]float64 {
	if pts == nil {
		return nil
	}
	var out [][2]float64
	for _, p := range pts.children("xy") {
		out = append(out, xy(p))
	}
	return out
}

func round(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// n turns a number into exact JSON text (no 1e-05 or 0.30000000000000004).
func n(v float64) jsonNumber {
	v = round(v)
	if v == 0 {
		v = 0 // no "-0"
	}
	return jsonNumber(strconv.FormatFloat(v, 'f', -1, 64))
}

func pt(p [2]float64) []any { return []any{n(p[0]), n(p[1])} }

func pts(ps [][2]float64) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = pt(p)
	}
	return out
}

func segs(ss [][4]float64) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = []any{n(s[0]), n(s[1]), n(s[2]), n(s[3])}
	}
	return out
}
