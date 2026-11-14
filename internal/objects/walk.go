package objects

import "fmt"

// children returns the ids an object points to: a commit's tree and
// parents, a tree's entries. Blobs point to nothing and are not read.
func (s *Store) children(id string, t Type, data []byte) ([]string, error) {
	switch t {
	case Commit:
		c, err := ParseCommit(data)
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", id, err)
		}
		return append([]string{c.Tree}, c.Parents...), nil
	case Tree:
		entries, err := ParseTree(data)
		if err != nil {
			return nil, fmt.Errorf("tree %s: %w", id, err)
		}
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = e.ID
		}
		return out, nil
	}
	return nil, nil
}

// HaveSet is what the other side of a transfer already has, worked out from
// the commit tips it told us about: every commit reachable from those tips,
// plus every tree and blob in the tips' own snapshots. Tips we don't have are
// ignored.
//
// Objects only reachable from older snapshots aren't included, so an old file
// brought back by a revert may be sent again. That's harmless: storing an
// object twice is a no-op.
func (s *Store) HaveSet(tips []string) (map[string]bool, error) {
	have := map[string]bool{}
	var trees []string
	stack := []string{}
	for _, tip := range tips {
		if ValidID(tip) && s.Has(tip) {
			stack = append(stack, tip)
		}
	}
	isTip := map[string]bool{}
	for _, t := range stack {
		isTip[t] = true
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if have[id] {
			continue
		}
		t, data, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		if t != Commit {
			continue // a tip that isn't a commit: ignore
		}
		have[id] = true
		c, err := ParseCommit(data)
		if err != nil {
			return nil, fmt.Errorf("commit %s: %w", id, err)
		}
		if isTip[id] {
			trees = append(trees, c.Tree)
		}
		stack = append(stack, c.Parents...)
	}
	// Snapshots of the tips: trees and blobs.
	for len(trees) > 0 {
		id := trees[len(trees)-1]
		trees = trees[:len(trees)-1]
		if have[id] {
			continue
		}
		have[id] = true
		t, data, err := s.Read(id)
		if err != nil {
			return nil, err
		}
		if t != Tree {
			continue
		}
		entries, err := ParseTree(data)
		if err != nil {
			return nil, fmt.Errorf("tree %s: %w", id, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				trees = append(trees, e.ID)
			} else {
				have[e.ID] = true
			}
		}
	}
	return have, nil
}

// Missing lists every object reachable from wants that isn't in have, in an
// order safe to store one by one: each object comes after everything it
// points to (blobs, then their trees, then commits, oldest commit first). So
// a receiver that stops halfway never has a commit without its files.
func (s *Store) Missing(wants []string, have map[string]bool) ([]string, error) {
	type frame struct {
		id   string
		kids []string
		next int
	}
	var out []string
	visited := map[string]bool{}
	var stack []frame
	push := func(id string) error {
		if visited[id] || have[id] {
			return nil
		}
		visited[id] = true
		t, data, err := s.Read(id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		kids, err := s.children(id, t, data)
		if err != nil {
			return err
		}
		stack = append(stack, frame{id: id, kids: kids})
		return nil
	}
	for _, w := range wants {
		if err := push(w); err != nil {
			return nil, err
		}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next < len(top.kids) {
				kid := top.kids[top.next]
				top.next++
				if err := push(kid); err != nil {
					return nil, err
				}
				continue
			}
			out = append(out, top.id)
			stack = stack[:len(stack)-1]
		}
	}
	return out, nil
}

// CheckComplete makes sure tip and everything below it is stored. Commits in
// known are trusted to be complete already (for example the branch tips from
// before a push), which stops the walk there.
func (s *Store) CheckComplete(tip string, known map[string]bool) error {
	if !ValidID(tip) {
		return fmt.Errorf("bad object id %q", tip)
	}
	t, _, err := s.Read(tip)
	if err != nil {
		return fmt.Errorf("%s: %w", tip, err)
	}
	if t != Commit {
		return fmt.Errorf("%s is a %s, not a commit", tip, t)
	}
	seen := map[string]bool{}
	stack := []string{tip}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] || known[id] {
			continue
		}
		seen[id] = true
		t, data, err := s.Read(id)
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		if t == Blob {
			continue
		}
		kids, err := s.children(id, t, data)
		if err != nil {
			return err
		}
		stack = append(stack, kids...)
	}
	return nil
}
