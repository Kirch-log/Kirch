package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// WiringKind is the kind of a wiring change.
type WiringKind string

const (
	NetAdded   WiringKind = "net-added"   // Net with Pins is new
	NetRemoved WiringKind = "net-removed" // Net with Pins is gone
	NetRenamed WiringKind = "net-renamed" // OldNet is now called Net
	PinMoved   WiringKind = "pin-moved"   // Pin went from net OldNet to net Net
	PinSwapped WiringKind = "pin-swapped" // in Net, OldPin was replaced by Pin
	PinJoined  WiringKind = "pin-joined"  // Pin (unconnected before) joined Net
	PinLeft    WiringKind = "pin-left"    // Pin left Net and is now unconnected
)

// WiringChange is one change in which pins are connected.
type WiringChange struct {
	Kind   WiringKind
	Net    string
	OldNet string
	Pin    string
	OldPin string
	Pins   []string
	// Label names what a swap is about: the one pin still on the net
	// (e.g. SERVO1.SIG), or the net name when several remain.
	Label string
}

func (c WiringChange) String() string {
	switch c.Kind {
	case NetAdded:
		return fmt.Sprintf("+ net %s: %s", c.Net, strings.Join(c.Pins, ", "))
	case NetRemoved:
		return fmt.Sprintf("- net %s: %s", c.Net, strings.Join(c.Pins, ", "))
	case NetRenamed:
		return fmt.Sprintf("net %s renamed to %s", c.OldNet, c.Net)
	case PinMoved:
		return fmt.Sprintf("%s moved from net %s to net %s", c.Pin, c.OldNet, c.Net)
	case PinSwapped:
		return fmt.Sprintf("%s: %s → %s", c.Label, c.OldPin, c.Pin)
	case PinJoined:
		return fmt.Sprintf("+ %s connected to net %s", c.Pin, c.Net)
	case PinLeft:
		return fmt.Sprintf("- %s disconnected from net %s", c.Pin, c.Net)
	}
	return string(c.Kind)
}

// Wiring compares two netlist.json versions pin by pin (docs/formats.md).
// Either side may be empty (file added or deleted).
func Wiring(oldData, newData []byte) ([]WiringChange, error) {
	oldNets, err := parseNets(oldData)
	if err != nil {
		return nil, fmt.Errorf("old netlist.json: %w", err)
	}
	newNets, err := parseNets(newData)
	if err != nil {
		return nil, fmt.Errorf("new netlist.json: %w", err)
	}
	var changes []WiringChange

	// Match old nets to new ones: same name, or a renamed net sharing at
	// least half of its pins.
	rename := map[string]string{} // old name → new name
	matchedNew := map[string]bool{}
	var goneOld []string
	for name := range oldNets {
		if _, ok := newNets[name]; ok {
			rename[name] = name
			matchedNew[name] = true
		} else {
			goneOld = append(goneOld, name)
		}
	}
	sort.Strings(goneOld)
	for _, o := range goneOld {
		best, bestOverlap := "", 0
		for _, n := range sortedKeys(newNets) {
			if matchedNew[n] {
				continue
			}
			overlap := 0
			for p := range oldNets[o] {
				if newNets[n][p] {
					overlap++
				}
			}
			if overlap > bestOverlap && 2*overlap >= max(len(oldNets[o]), len(newNets[n])) {
				best, bestOverlap = n, overlap
			}
		}
		if best != "" {
			rename[o] = best
			matchedNew[best] = true
			changes = append(changes, WiringChange{Kind: NetRenamed, OldNet: o, Net: best})
		}
	}
	for _, o := range sortedKeys(oldNets) {
		if _, ok := rename[o]; !ok {
			changes = append(changes, WiringChange{Kind: NetRemoved, Net: o, Pins: sortedKeys(oldNets[o])})
		}
	}
	for _, n := range sortedKeys(newNets) {
		if !matchedNew[n] {
			changes = append(changes, WiringChange{Kind: NetAdded, Net: n, Pins: sortedKeys(newNets[n])})
		}
	}

	// Pin by pin. Joins and leaves on surviving nets are collected first,
	// so a net that lost one pin and gained one reads as a swap.
	oldPin, newPin := pinNets(oldNets), pinNets(newNets)
	pins := map[string]bool{}
	for p := range oldPin {
		pins[p] = true
	}
	for p := range newPin {
		pins[p] = true
	}
	joined, left := map[string][]string{}, map[string][]string{}
	for _, p := range sortedKeys(pins) {
		oldName, wasOn := oldPin[p]
		newName, isOn := newPin[p]
		oldSurvives := wasOn && rename[oldName] != ""
		newSurvives := isOn && matchedNew[newName]
		switch {
		case wasOn && isOn:
			if rename[oldName] == newName || (!oldSurvives && !newSurvives) {
				continue // same net, or both covered by the net lines above
			}
			changes = append(changes, WiringChange{Kind: PinMoved, Pin: p, OldNet: oldName, Net: newName})
		case isOn && newSurvives:
			joined[newName] = append(joined[newName], p)
		case wasOn && oldSurvives:
			left[rename[oldName]] = append(left[rename[oldName]], p)
		}
	}
	for _, n := range sortedKeys(newNets) {
		j, l := joined[n], left[n]
		if len(j) == 1 && len(l) == 1 {
			label := "net " + n
			if stay := sharedPins(oldNets, newNets, rename, n); len(stay) == 1 {
				label = stay[0]
			}
			changes = append(changes, WiringChange{Kind: PinSwapped, Net: n, OldPin: l[0], Pin: j[0], Label: label})
			continue
		}
		for _, p := range l {
			changes = append(changes, WiringChange{Kind: PinLeft, Pin: p, Net: n})
		}
		for _, p := range j {
			changes = append(changes, WiringChange{Kind: PinJoined, Pin: p, Net: n})
		}
	}
	return changes, nil
}

type netSet map[string]map[string]bool

func parseNets(data []byte) (netSet, error) {
	nets := netSet{}
	if len(data) == 0 {
		return nets, nil
	}
	var doc struct {
		Nets map[string][]string `json:"nets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	for name, pins := range doc.Nets {
		nets[name] = map[string]bool{}
		for _, p := range pins {
			nets[name][p] = true
		}
	}
	return nets, nil
}

func pinNets(nets netSet) map[string]string {
	out := map[string]string{}
	for name, pins := range nets {
		for p := range pins {
			out[p] = name
		}
	}
	return out
}

// sharedPins lists pins on new net n that were already on its old net.
func sharedPins(oldNets, newNets netSet, rename map[string]string, n string) []string {
	for o, to := range rename {
		if to != n {
			continue
		}
		var out []string
		for p := range newNets[n] {
			if oldNets[o][p] {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
