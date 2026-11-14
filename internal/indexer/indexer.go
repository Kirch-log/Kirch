// Package indexer copies what's in the server's repository folders into
// Postgres: new commits and the current branch tips. Running it again
// changes nothing; the database can always be rebuilt from the folders.
package indexer

import (
	"context"
	"fmt"
	"sort"

	"github.com/Kirch-log/kirch/internal/db"
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/jackc/pgx/v5"
)

// Opener opens owner/name on disk (server.Repos does this).
type Opener interface {
	Open(owner, name string) (*repo.Repo, error)
	List() ([][2]string, error)
}

// Stats says what one IndexRepo call did.
type Stats struct {
	NewCommits     int
	BranchesMoved  bool
	BranchesStored int
}

// IndexRepo brings owner/name's rows up to date with its folder. Only one
// indexer works on a repo at a time (a Postgres advisory lock), and the
// folder is read while holding it, so an older snapshot can never overwrite
// a newer one.
func IndexRepo(ctx context.Context, d *db.DB, repos Opener, owner, name string) (Stats, error) {
	var st Stats
	err := d.InTx(ctx, func(tx pgx.Tx) error {
		if err := db.LockRepo(ctx, tx, owner, name); err != nil {
			return err
		}
		r, err := repos.Open(owner, name)
		if err != nil {
			return err
		}
		branches, err := r.Refs().Branches()
		if err != nil {
			return err
		}
		head, _, err := r.Refs().Head()
		if err != nil {
			return err
		}
		repoID, err := db.EnsureRepo(ctx, tx, owner, name)
		if err != nil {
			return err
		}
		known, err := db.KnownCommits(ctx, tx, repoID)
		if err != nil {
			return err
		}
		fresh, err := newCommits(r, branches, known)
		if err != nil {
			return err
		}
		if err := db.InsertCommits(ctx, tx, repoID, fresh); err != nil {
			return err
		}
		moved, err := db.SyncRefs(ctx, tx, repoID, head, branches)
		if err != nil {
			return err
		}
		st = Stats{NewCommits: len(fresh), BranchesMoved: moved, BranchesStored: len(branches)}
		return nil
	})
	if err != nil {
		return Stats{}, fmt.Errorf("index %s/%s: %w", owner, name, err)
	}
	return st, nil
}

// newCommits walks back from the branch tips, stopping at commits already
// indexed (everything below an indexed commit is indexed too).
func newCommits(r *repo.Repo, branches map[string]string, known map[string]bool) ([]db.Commit, error) {
	tips := make([]string, 0, len(branches))
	for _, id := range branches {
		tips = append(tips, id)
	}
	sort.Strings(tips)
	seen := map[string]bool{}
	var out []db.Commit
	stack := tips
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] || known[id] {
			continue
		}
		seen[id] = true
		c, err := r.ReadCommit(id)
		if err != nil {
			return nil, err
		}
		out = append(out, db.Commit{
			ID:        id,
			Tree:      c.Tree,
			Parents:   c.Parents,
			Author:    db.Person{Name: c.Author.Name, Email: c.Author.Email, When: c.Author.When},
			Committer: db.Person{Name: c.Committer.Name, Email: c.Committer.Email, When: c.Committer.When},
			Message:   c.Message,
		})
		stack = append(stack, c.Parents...)
	}
	return out, nil
}

// IndexAll indexes every repository folder (at server start, to catch up
// on anything pushed while the database was away). It keeps going past
// failures and returns the first one.
func IndexAll(ctx context.Context, d *db.DB, repos Opener) (int, error) {
	list, err := repos.List()
	if err != nil {
		return 0, err
	}
	var first error
	n := 0
	for _, on := range list {
		if _, err := IndexRepo(ctx, d, repos, on[0], on[1]); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		n++
	}
	return n, first
}
