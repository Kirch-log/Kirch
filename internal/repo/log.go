package repo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
)

// ErrNoCommits means the current branch has no commits yet.
var ErrNoCommits = errors.New("the current branch has no commits yet")

// Resolve turns a revision into a commit id. A revision is "HEAD" (or ""),
// a branch name, a remote branch ("origin/main"), or a full or short (4+
// chars) commit id. Branch names win over short ids, like in git.
//
// Any of those can be followed by steps back through history, like git:
// "~N" goes N first-parents back (HEAD~2), "^" is the first parent and "^2"
// the second parent of a merge (HEAD^2, main~1^2).
func (r *Repo) Resolve(rev string) (string, error) {
	cut := strings.IndexAny(rev, "~^")
	if cut < 0 {
		return r.resolveName(rev)
	}
	if cut == 0 {
		return "", fmt.Errorf("unknown revision %q", rev)
	}
	id, err := r.resolveName(rev[:cut])
	if err != nil {
		return "", err
	}
	steps := rev[cut:]
	for steps != "" {
		op := steps[0]
		steps = steps[1:]
		n, digits := 1, 0
		for digits < len(steps) && steps[digits] >= '0' && steps[digits] <= '9' {
			digits++
		}
		if digits > 0 {
			if digits > 6 {
				return "", fmt.Errorf("unknown revision %q", rev)
			}
			n = 0
			for _, d := range steps[:digits] {
				n = n*10 + int(d-'0')
			}
			steps = steps[digits:]
		}
		if steps != "" && steps[0] != '~' && steps[0] != '^' {
			return "", fmt.Errorf("unknown revision %q", rev)
		}
		if op == '~' {
			for i := 0; i < n; i++ {
				if id, err = r.parent(id, 1, rev); err != nil {
					return "", err
				}
			}
		} else if n > 0 { // ^0 is the commit itself
			if id, err = r.parent(id, n, rev); err != nil {
				return "", err
			}
		}
	}
	return id, nil
}

// parent returns the n-th parent (1 = first) of commit id.
func (r *Repo) parent(id string, n int, rev string) (string, error) {
	c, err := r.ReadCommit(id)
	if err != nil {
		return "", err
	}
	if n > len(c.Parents) {
		return "", fmt.Errorf("%s: history doesn't go back that far (%s has %d parent(s))", rev, id[:7], len(c.Parents))
	}
	return c.Parents[n-1], nil
}

func (r *Repo) resolveName(rev string) (string, error) {
	if rev == "" || rev == "HEAD" {
		_, id, err := r.Refs().Head()
		if err != nil {
			return "", err
		}
		if id == "" {
			return "", ErrNoCommits
		}
		return id, nil
	}
	if id, err := r.Refs().Branch(rev); err == nil && id != "" {
		return id, nil
	}
	if remote, branch, ok := strings.Cut(rev, "/"); ok { // e.g. origin/main
		if id, err := r.Refs().RemoteBranch(remote, branch); err == nil && id != "" {
			return id, nil
		}
	}
	id, err := r.Objects.Expand(rev)
	if errors.Is(err, objects.ErrAmbiguous) {
		return "", err
	}
	if err != nil {
		return "", fmt.Errorf("unknown revision %q (not a branch or commit id)", rev)
	}
	if _, err := r.ReadCommit(id); err != nil {
		return "", err
	}
	return id, nil
}

// LogEntry is one commit in a history listing.
type LogEntry struct {
	ID string
	objects.CommitData
}

// Log returns the commits reachable from rev, newest first, stopping after
// max commits (0 = no limit). Order matches git log's default: always show
// the newest committer date next, ties in the order commits were found.
func (r *Repo) Log(rev string, max int) ([]LogEntry, error) {
	start, err := r.Resolve(rev)
	if err != nil {
		return nil, err
	}
	first, err := r.ReadCommit(start)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{start: true}
	queue := []LogEntry{{ID: start, CommitData: first}}
	var out []LogEntry
	for len(queue) > 0 && (max <= 0 || len(out) < max) {
		e := queue[0]
		queue = queue[1:]
		out = append(out, e)
		for _, p := range e.Parents {
			if seen[p] {
				continue
			}
			seen[p] = true
			c, err := r.ReadCommit(p)
			if err != nil {
				return nil, fmt.Errorf("parent of %s: %w", e.ID, err)
			}
			queue = insertByDate(queue, LogEntry{ID: p, CommitData: c})
		}
	}
	return out, nil
}

// insertByDate puts e after every queued commit that is as new or newer.
func insertByDate(queue []LogEntry, e LogEntry) []LogEntry {
	when := e.Committer.When.Unix()
	i := 0
	for i < len(queue) && queue[i].Committer.When.Unix() >= when {
		i++
	}
	queue = append(queue, LogEntry{})
	copy(queue[i+1:], queue[i:])
	queue[i] = e
	return queue
}
