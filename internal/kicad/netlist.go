package kicad

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/canon"
)

// Netlist is what Kirch keeps from a KiCad netlist export
// (kicad-cli sch export netlist --format kicadsexpr).
type Netlist struct {
	Source string // schematic file name, e.g. "servo-board.kicad_sch"
	Title  string
	Parts  map[string]*Part // by reference: "R1", "U1"
	Nets   []Net
}

// Part is one component on the schematic.
type Part struct {
	Ref         string
	Value       string            // "10k", "ESP32-S3-WROOM-1"
	Footprint   string            // "Resistor_SMD:R_0603_1608Metric"
	Symbol      string            // library symbol, "Device:R"
	Description string            // "Resistor"
	PinNames    map[string]string // pin number → name, when the pin has one ("9" → "GPIO9")
	Pins        []string          // every pin number the symbol has
}

// Net is a group of connected pins.
type Net struct {
	Name string
	Pins []Pin
}

// Pin is one pin on a net.
type Pin struct {
	Ref, Num string
}

// Parse reads a KiCad netlist in KiCad's own s-expression format.
func Parse(data []byte) (*Netlist, error) {
	root, err := parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("not a KiCad netlist: %w", err)
	}
	if root.head() != "export" {
		return nil, errors.New("not a KiCad netlist (it should start with \"(export\"); export it with KiCad's \"KiCad\" netlist format")
	}
	n := &Netlist{Parts: map[string]*Part{}}
	if d := root.child("design"); d != nil {
		n.Source = path.Base(strings.ReplaceAll(d.value("source"), `\`, "/"))
		if s := d.child("sheet"); s != nil {
			if tb := s.child("title_block"); tb != nil {
				n.Title = tb.value("title")
			}
		}
	}

	// Pin names per library symbol: (libpart (lib "Device") (part "R") (pins (pin (num "1") (name "~"))))
	libPins := map[string]map[string]string{}
	libPinList := map[string][]string{}
	if lp := root.child("libparts"); lp != nil {
		for _, l := range lp.children("libpart") {
			key := l.value("lib") + ":" + l.value("part")
			names := map[string]string{}
			if pins := l.child("pins"); pins != nil {
				for _, p := range pins.children("pin") {
					num := p.value("num")
					if num == "" {
						continue
					}
					libPinList[key] = append(libPinList[key], num)
					if name := p.value("name"); name != "" && name != "~" && name != num {
						names[num] = name
					}
				}
			}
			libPins[key] = names
		}
	}

	comps := root.child("components")
	if comps == nil {
		return nil, errors.New("KiCad netlist has no components")
	}
	for _, c := range comps.children("comp") {
		ref := c.value("ref")
		if ref == "" || strings.HasPrefix(ref, "#") { // #PWR, #FLG: not real parts
			continue
		}
		p := &Part{Ref: ref, Value: unescape(c.value("value")), Footprint: c.value("footprint"), PinNames: map[string]string{}}
		if ls := c.child("libsource"); ls != nil {
			p.Symbol = ls.value("lib") + ":" + ls.value("part")
			p.Description = ls.value("description")
		}
		for num, name := range libPins[p.Symbol] {
			p.PinNames[num] = name
		}
		p.Pins = append(p.Pins, libPinList[p.Symbol]...)
		// KiCad 7+: (units (unit (pins (pin (num "1")))))
		if units := c.child("units"); units != nil {
			for _, u := range units.children("unit") {
				if pins := u.child("pins"); pins != nil {
					for _, pin := range pins.children("pin") {
						if num := pin.value("num"); num != "" {
							p.Pins = append(p.Pins, num)
						}
					}
				}
			}
		}
		n.Parts[ref] = p
	}

	nets := root.child("nets")
	if nets == nil {
		return nil, errors.New("KiCad netlist has no nets")
	}
	for _, nt := range nets.children("net") {
		net := Net{Name: unescape(nt.value("name"))}
		for _, nd := range nt.children("node") {
			ref, num := nd.value("ref"), nd.value("pin")
			part := n.Parts[ref]
			if part == nil || num == "" {
				continue
			}
			net.Pins = append(net.Pins, Pin{Ref: ref, Num: num})
			part.Pins = append(part.Pins, num)
			if name := pinName(nd.value("pinfunction"), num); name != "" {
				if _, ok := part.PinNames[num]; !ok {
					part.PinNames[num] = name
				}
			}
		}
		n.Nets = append(n.Nets, net)
	}
	for _, p := range n.Parts {
		p.Pins = uniqueSorted(p.Pins)
	}
	return n, nil
}

// KiCad writes some characters in names as {words}: "VPP{slash}MCLR".
var kicadEscapes = strings.NewReplacer(
	"{slash}", "/", "{backslash}", `\`, "{lt}", "<", "{gt}", ">", "{colon}", ":",
	"{dblquote}", `"`, "{quote}", "'", "{tab}", "\t", "{return}", "\n", "{space}", " ",
)

func unescape(s string) string { return kicadEscapes.Replace(s) }

// pinName cleans KiCad's pinfunction: KiCad 10 writes "GPIO9_9" (name + "_" +
// number) when pin names repeat, older versions just "GPIO9".
func pinName(function, num string) string {
	name := strings.TrimSuffix(function, "_"+num)
	if name == "" || name == "~" || name == num {
		return ""
	}
	return name
}

// Kirch's files from a netlist (README, "Circuit files"):
//
//	netlist.json  nets → "REF.PIN" lists, pins by number ("U1.9"); nets with a
//	              single pin are unconnected pins and left out
//	parts.json    one entry per part: type, value, symbol, footprint, pin names
//
// Both come out canonical, so the same design always gives the same bytes.
func (n *Netlist) Files() (netlist, parts []byte, err error) {
	nets := map[string]any{}
	used := map[string]bool{}
	for _, net := range n.Nets {
		if len(net.Pins) < 2 {
			continue
		}
		name := net.Name
		if name == "" || used[name] {
			return nil, nil, fmt.Errorf("two nets are both called %q", name)
		}
		used[name] = true
		pins := make([]any, len(net.Pins))
		for i, p := range net.Pins {
			pins[i] = p.Ref + "." + p.Num
		}
		nets[name] = pins
	}
	nl := map[string]any{"format": "kirch.netlist/1", "nets": nets}
	if n.Source != "" {
		nl["source"] = "kicad:" + n.Source
	}
	if netlist, err = canon.Format("netlist.json", mustJSON(nl)); err != nil {
		return nil, nil, err
	}

	ps := map[string]any{}
	for ref, p := range n.Parts {
		e := map[string]any{"type": partType(p)}
		for k, v := range map[string]string{"value": p.Value, "symbol": p.Symbol, "footprint": p.Footprint, "description": p.Description} {
			if v != "" && v != "~" {
				e[k] = v
			}
		}
		if len(p.Pins) > 0 {
			pins := make([]any, len(p.Pins))
			for i, num := range p.Pins {
				pins[i] = num
			}
			e["pins"] = pins
		}
		if len(p.PinNames) > 0 {
			names := map[string]any{}
			for num, name := range p.PinNames {
				names[num] = name
			}
			e["pin_names"] = names
		}
		ps[ref] = e
	}
	pj := map[string]any{"format": "kirch.parts/1", "parts": ps}
	if n.Title != "" {
		pj["title"] = n.Title
	}
	if parts, err = canon.Format("parts.json", mustJSON(pj)); err != nil {
		return nil, nil, err
	}
	return netlist, parts, nil
}

var refPrefix = regexp.MustCompile(`^[A-Za-z]+`)

var mcuWords = regexp.MustCompile(`(?i)esp32|esp8266|atmega|attiny|rp2040|rp2350|stm32|nrf5|samd|pic1|pic16|pic18|arduino|teensy|ch32`)

// partType guesses Kirch's generic part type from the reference prefix
// (R1 → resistor) and, for chips, the symbol / value.
func partType(p *Part) string {
	if mcuWords.MatchString(p.Symbol + " " + p.Value) {
		return "mcu"
	}
	switch strings.ToUpper(refPrefix.FindString(p.Ref)) {
	case "R":
		return "resistor"
	case "RV", "VR", "POT":
		return "potentiometer"
	case "C":
		return "capacitor"
	case "L":
		return "inductor"
	case "D":
		if strings.Contains(strings.ToUpper(p.Symbol), "LED") {
			return "led"
		}
		return "diode"
	case "LED":
		return "led"
	case "Q", "T":
		return "transistor"
	case "U", "IC":
		return "ic"
	case "J", "P", "CN", "CON", "X":
		return "connector"
	case "SW", "S", "BTN":
		return "switch"
	case "Y":
		return "crystal"
	case "F":
		return "fuse"
	case "BT", "BAT":
		return "battery"
	case "K", "RL":
		return "relay"
	case "M":
		return "motor"
	case "BZ", "LS", "SP":
		return "speaker"
	case "TP":
		return "testpoint"
	case "H", "MH":
		return "mounting-hole"
	case "V":
		return "tube"
	}
	return "part"
}

func uniqueSorted(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return pinLess(out[i], out[j]) })
	return out
}

// pinLess sorts pin numbers naturally: 2 before 10, then letters (A1, B2).
func pinLess(a, b string) bool {
	if len(a) != len(b) && isDigits(a) && isDigits(b) {
		return len(a) < len(b)
	}
	return a < b
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}
