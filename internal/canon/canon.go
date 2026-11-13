// Package canon rewrites Kirch's circuit files into one standard layout, so
// the same circuit always produces the same bytes (and the same object id),
// and moving one wire changes one line. See docs/formats.md, "Rules for all files".
//
// Canonical form: object keys sorted, lists of strings sorted, 2-space indent,
// one item per line, trailing newline. Numbers are kept exactly as written.
package canon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Files that kirch add rewrites. Anything else (firmware, photos) is stored as is.
var circuitFiles = map[string]bool{
	"parts.json":   true,
	"netlist.json": true,
	"layout.json":  true,
	"capture.json": true,
}

// Applies reports whether the file at slash path p is a circuit file.
func Applies(p string) bool {
	return circuitFiles[path.Base(p)]
}

// Format returns the canonical form of a circuit file. name is the file's
// base name (e.g. "netlist.json"), used for file-specific checks.
// Errors don't repeat the file name; callers add the path.
func Format(name string, data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // keep 4975 as 4975, not 4975.0 or 4.975e3
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("extra data after the JSON value")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("must be a JSON object")
	}
	if err := check(path.Base(name), obj); err != nil {
		return nil, err
	}
	sortStringLists(obj)

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf) // map keys come out sorted
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil // Encode already ends with "\n"
}

// sortStringLists sorts every list whose items are all strings (pin lists,
// pin names). Lists of anything else keep their order.
func sortStringLists(v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			sortStringLists(child)
		}
	case []any:
		strs := make([]string, 0, len(t))
		for _, item := range t {
			sortStringLists(item)
			if s, ok := item.(string); ok {
				strs = append(strs, s)
			}
		}
		if len(strs) == len(t) && len(t) > 1 {
			sort.Strings(strs)
			for i, s := range strs {
				t[i] = s
			}
		}
	}
}

func check(name string, obj map[string]any) error {
	if format, ok := obj["format"].(string); ok {
		want := "kirch." + strings.TrimSuffix(name, ".json") + "/"
		if !strings.HasPrefix(format, want) {
			return fmt.Errorf("format is %q, expected %s<version>", format, want)
		}
	}
	if name == "netlist.json" {
		return checkNetlist(obj)
	}
	return nil
}

// Each pin may be in at most one net (docs/formats.md, netlist.json).
func checkNetlist(obj map[string]any) error {
	nets, ok := obj["nets"]
	if !ok {
		return nil
	}
	netMap, ok := nets.(map[string]any)
	if !ok {
		return fmt.Errorf(`"nets" must be an object of net name → list of pins`)
	}
	seen := map[string]string{}
	names := make([]string, 0, len(netMap))
	for n := range netMap {
		names = append(names, n)
	}
	sort.Strings(names) // stable error messages
	for _, net := range names {
		pins, ok := netMap[net].([]any)
		if !ok {
			return fmt.Errorf("net %q must be a list of pins", net)
		}
		for _, p := range pins {
			pin, ok := p.(string)
			if !ok || !strings.Contains(pin, ".") {
				return fmt.Errorf("net %q has %v; pins are written PART.PIN", net, p)
			}
			if other, dup := seen[pin]; dup {
				if other == net {
					return fmt.Errorf("%s is listed twice in net %q", pin, net)
				}
				return fmt.Errorf("%s is in two nets, %q and %q", pin, other, net)
			}
			seen[pin] = net
		}
	}
	return nil
}
