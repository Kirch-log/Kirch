// Package kicad reads KiCad designs and turns them into Kirch's circuit
// files (netlist.json, parts.json). See PLAN.md.
package kicad

import (
	"errors"
	"fmt"
	"strings"
)

// node is one s-expression: an atom (Atom set) or a list (List set).
// KiCad files are s-expressions: (export (version "E") (nets (net ...))).
type node struct {
	Atom   string
	List   []*node
	isList bool
}

// head is the first atom of a list: "net" for (net (code "1") ...).
func (n *node) head() string {
	if n.isList && len(n.List) > 0 && !n.List[0].isList {
		return n.List[0].Atom
	}
	return ""
}

// children returns the sub-lists whose head is name.
func (n *node) children(name string) []*node {
	var out []*node
	for _, c := range n.List {
		if c.head() == name {
			out = append(out, c)
		}
	}
	return out
}

// child returns the first sub-list whose head is name, or nil.
func (n *node) child(name string) *node {
	for _, c := range n.List {
		if c.head() == name {
			return c
		}
	}
	return nil
}

// value returns the first atom after the head of child name:
// (ref "R1") → "R1". "" if missing.
func (n *node) value(name string) string {
	c := n.child(name)
	if c == nil || len(c.List) < 2 || c.List[1].isList {
		return ""
	}
	return c.List[1].Atom
}

// parse reads one s-expression (the whole file).
func parse(src string) (*node, error) {
	p := &parser{s: src}
	p.space()
	n, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.s) {
		return nil, p.errorf("extra text after the end")
	}
	if !n.isList {
		return nil, errors.New("not a KiCad file (expected '(')")
	}
	return n, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) errorf(format string, args ...any) error {
	line := 1 + strings.Count(p.s[:p.i], "\n")
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
}

func (p *parser) space() {
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.i]) >= 0 {
		p.i++
	}
}

func (p *parser) expr(depth int) (*node, error) {
	if depth > 200 {
		return nil, p.errorf("nested too deeply")
	}
	if p.i >= len(p.s) {
		return nil, p.errorf("file ends too early")
	}
	switch p.s[p.i] {
	case '(':
		p.i++
		n := &node{isList: true}
		for {
			p.space()
			if p.i >= len(p.s) {
				return nil, p.errorf("missing ')'")
			}
			if p.s[p.i] == ')' {
				p.i++
				return n, nil
			}
			c, err := p.expr(depth + 1)
			if err != nil {
				return nil, err
			}
			n.List = append(n.List, c)
		}
	case ')':
		return nil, p.errorf("unexpected ')'")
	case '"':
		return p.quoted()
	}
	start := p.i
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n()\"", p.s[p.i]) < 0 {
		p.i++
	}
	return &node{Atom: p.s[start:p.i]}, nil
}

func (p *parser) quoted() (*node, error) {
	p.i++ // opening quote
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch c {
		case '"':
			p.i++
			return &node{Atom: b.String()}, nil
		case '\\':
			if p.i+1 >= len(p.s) {
				return nil, p.errorf("unfinished escape")
			}
			p.i++
			switch e := p.s[p.i]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(e) // \" \\ and anything else: the character itself
			}
		default:
			b.WriteByte(c)
		}
		p.i++
	}
	return nil, p.errorf("missing closing quote")
}
