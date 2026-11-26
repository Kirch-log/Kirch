package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/canon"
)

// ErrNotNetlist means a version couldn't be read as a netlist, or a field
// other than "nets" changed on both sides; merge it as text instead.
var ErrNotNetlist = errors.New("can't merge as a netlist")

// Netlist merges netlist.json net by net (README, "Circuit files"):
//
//   - a net changed on one side only takes that side's pins
//   - a net both sides changed the same way takes that change
//   - a net both sides changed differently is a conflict: it keeps our pins,
//     and both versions are listed under "conflicts"
//   - a pin that would end up in two nets (each side moved it somewhere
//     else) is a conflict: it stays in our net, listed under "conflicts"
//
// So different nets edited on each branch merge on their own, but two
// people rewiring the same net is never combined silently (that could
// short pins together that neither person connected).
//
// kirch add refuses a netlist.json that still has "conflicts". Output is
// canonical. An empty base means both sides added the file. Returns the
// merged file and one line per conflict.
func Netlist(base, ours, theirs []byte) ([]byte, []string, error) {
	var docs [3]map[string]any
	var nets [3]map[string][]string
	for i, data := range [][]byte{base, ours, theirs} {
		doc, n, err := parseNetlist(data)
		if err != nil {
			return nil, nil, err
		}
		docs[i], nets[i] = doc, n
	}
	b, o, t := docs[0], docs[1], docs[2]

	// Fields other than "nets" (e.g. "format") merge as whole values.
	out := map[string]any{}
	for _, k := range keys(b, o, t) {
		if k == "nets" {
			continue
		}
		v, ok, clean := pick3(b[k] != nil, b[k], o[k] != nil, o[k], t[k] != nil, t[k])
		if !clean {
			return nil, nil, fmt.Errorf("%w: both sides changed %q", ErrNotNetlist, k)
		}
		if ok {
			out[k] = v
		}
	}

	var lines []string
	netConflicts := map[string]any{}
	result := map[string][]string{}
	names := map[string]bool{}
	for _, n := range nets {
		for name := range n {
			names[name] = true
		}
	}
	for _, name := range sortedKeys(names) {
		bp, bok := nets[0][name]
		op, ook := nets[1][name]
		tp, tok := nets[2][name]
		v, ok, clean := pick3(bok, bp, ook, op, tok, tp)
		if !clean {
			netConflicts[name] = map[string]any{"ours": listOrNil(op, ook), "theirs": listOrNil(tp, tok)}
			lines = append(lines, fmt.Sprintf("net %s: ours %s, theirs %s", name, showPins(op, ook), showPins(tp, tok)))
			// Keep ours; if ours deleted the net, keep theirs so no pin is lost.
			v, ok = op, ook
			if !ook {
				v, ok = tp, tok
			}
		}
		if ok {
			result[name] = slices.Clone(v.([]string))
		}
	}

	// A pin may be in one net only.
	where := map[string][]string{}
	for _, name := range sortedKeys(result) {
		for _, pin := range result[name] {
			where[pin] = append(where[pin], name)
		}
	}
	pinConflicts := map[string]any{}
	pinNet := func(side int, pin string) string {
		for name, pins := range nets[side] {
			if slices.Contains(pins, pin) {
				return name
			}
		}
		return ""
	}
	for _, pin := range sortedKeys(where) {
		in := where[pin]
		if len(in) < 2 {
			continue
		}
		keep := pinNet(1, pin)
		if !slices.Contains(in, keep) {
			keep = in[0]
		}
		for _, name := range in {
			if name != keep {
				result[name] = slices.DeleteFunc(result[name], func(p string) bool { return p == pin })
			}
		}
		on, tn := pinNet(1, pin), pinNet(2, pin)
		pinConflicts[pin] = map[string]any{"ours": netOrNil(on), "theirs": netOrNil(tn)}
		lines = append(lines, fmt.Sprintf("pin %s: ours on %s, theirs on %s", pin, showNet(on), showNet(tn)))
	}

	netObj := map[string]any{}
	for name, pins := range result {
		if len(pins) > 0 || len(nets[1][name]) == 0 && len(nets[2][name]) == 0 {
			netObj[name] = pins
		}
	}
	if len(netObj) > 0 || hasKey("nets", b, o, t) {
		out["nets"] = netObj
	}
	if len(lines) > 0 {
		c := map[string]any{}
		if len(netConflicts) > 0 {
			c["nets"] = netConflicts
		}
		if len(pinConflicts) > 0 {
			c["pins"] = pinConflicts
		}
		out["conflicts"] = c
	}
	data, err := canon.Marshal(out)
	return data, lines, err
}

// pick3 is the three-way rule for one value: ok says whether the value
// exists in the result, clean is false when both sides changed it differently.
func pick3(bok bool, b any, ook bool, o any, tok bool, t any) (v any, ok, clean bool) {
	same := func(aok bool, a any, cok bool, c any) bool { return aok == cok && (!aok || reflect.DeepEqual(a, c)) }
	switch {
	case same(ook, o, tok, t), same(bok, b, tok, t):
		return o, ook, true
	case same(bok, b, ook, o):
		return t, tok, true
	}
	return nil, false, false
}

func listOrNil(pins []string, ok bool) any {
	if !ok {
		return nil
	}
	return pins
}

func netOrNil(n string) any {
	if n == "" {
		return nil
	}
	return n
}

func showPins(pins []string, ok bool) string {
	switch {
	case !ok:
		return "(deleted)"
	case len(pins) == 0:
		return "(no pins)"
	}
	return strings.Join(pins, " ")
}

func showNet(n string) string {
	if n == "" {
		return "no net"
	}
	return n
}

// parseNetlist reads a netlist into its fields and net → sorted pins.
func parseNetlist(data []byte) (map[string]any, map[string][]string, error) {
	doc := map[string]any{}
	nets := map[string][]string{}
	if len(bytes.TrimSpace(data)) == 0 {
		return doc, nets, nil
	}
	if _, err := canon.Format("netlist.json", data); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNotNetlist, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrNotNetlist, err)
	}
	if n, ok := doc["nets"].(map[string]any); ok {
		for name, list := range n {
			pins := []string{}
			for _, p := range list.([]any) {
				pins = append(pins, p.(string))
			}
			sort.Strings(pins)
			nets[name] = pins
		}
	}
	return doc, nets, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keys(maps ...map[string]any) []string {
	all := map[string]bool{}
	for _, m := range maps {
		for k := range m {
			all[k] = true
		}
	}
	return sortedKeys(all)
}

func hasKey(k string, maps ...map[string]any) bool {
	for _, m := range maps {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}
