// Package db is the Postgres index of the server's repositories: users,
// repos, branches and commit summaries, so the website can list and page
// through history quickly. Repository folders stay the source of truth.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

var (
	// ErrNotFound means no such repo or commit.
	ErrNotFound = errors.New("not found")
	// ErrAmbiguous means a short commit id matches more than one commit.
	ErrAmbiguous = errors.New("short commit id matches more than one commit")
)

// DB is a connection pool.
type DB struct {
	Pool *pgxpool.Pool
}

// Open connects and checks the connection works.
func Open(ctx context.Context, url string) (*DB, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: %w", err)
	}
	return &DB{Pool: pool}, nil
}

// Close closes the pool.
func (d *DB) Close() { d.Pool.Close() }

// Migrate applies migrations/NNNN_*.sql that haven't run yet, each in its
// own transaction. Safe to run from several servers at once.
func (d *DB) Migrate(ctx context.Context) error {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	if _, err := d.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    integer PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for _, f := range files {
		base := strings.TrimPrefix(f, "migrations/")
		num, _, _ := strings.Cut(base, "_")
		version, err := strconv.Atoi(num)
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", base)
		}
		sql, err := migrations.ReadFile(f)
		if err != nil {
			return err
		}
		err = d.InTx(ctx, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7206541)`); err != nil {
				return err
			}
			var done bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
				return err
			}
			if done {
				return nil
			}
			if _, err := tx.Exec(ctx, string(sql)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", base, err)
		}
	}
	return nil
}

// InTx runs fn in a transaction, committing if it returns nil.
func (d *DB) InTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Person is an author or committer.
type Person struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	When  time.Time `json:"when"`
}

// Commit is one commit's summary.
type Commit struct {
	ID        string   `json:"id"`
	Tree      string   `json:"tree"`
	Parents   []string `json:"parents"`
	Author    Person   `json:"author"`
	Committer Person   `json:"committer"`
	Message   string   `json:"message"`
	Summary   string   `json:"summary"`
}

// Repo is one repository.
type Repo struct {
	ID         int64      `json:"-"`
	Owner      string     `json:"owner"`
	Name       string     `json:"name"`
	HeadBranch string     `json:"head_branch"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Branches   int        `json:"branch_count"`
	Commits    int        `json:"commit_count"`
	LastCommit *Commit    `json:"last_commit,omitempty"`
	IndexedAt  *time.Time `json:"-"`
}

// Branch is a branch and the commit it points to.
type Branch struct {
	Name   string `json:"name"`
	Commit Commit `json:"commit"`
}

func tzMinutes(t time.Time) int16 {
	_, off := t.Zone()
	return int16(off / 60)
}

func inZone(t time.Time, minutes int16) time.Time {
	return t.In(time.FixedZone("", int(minutes)*60))
}

const commitCols = `c.id, c.tree, c.parents, c.author_name, c.author_email, c.authored_at, c.author_tz,
	c.committer_name, c.committer_email, c.committed_at, c.committer_tz, c.message, c.summary`

func scanCommit(row pgx.Row) (Commit, error) {
	var c Commit
	var atz, ctz int16
	err := row.Scan(&c.ID, &c.Tree, &c.Parents, &c.Author.Name, &c.Author.Email, &c.Author.When, &atz,
		&c.Committer.Name, &c.Committer.Email, &c.Committer.When, &ctz, &c.Message, &c.Summary)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	c.Author.When = inZone(c.Author.When, atz)
	c.Committer.When = inZone(c.Committer.When, ctz)
	if c.Parents == nil {
		c.Parents = []string{}
	}
	return c, err
}

// --- Writes (used by the indexer, inside one transaction) ---

// LockRepo makes indexing of owner/name one-at-a-time across all servers,
// until tx ends.
func LockRepo(ctx context.Context, tx pgx.Tx, owner, name string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, owner+"/"+name)
	return err
}

// EnsureRepo creates the user and repo rows if needed and returns the repo id.
func EnsureRepo(ctx context.Context, tx pgx.Tx, owner, name string) (int64, error) {
	var userID int64
	err := tx.QueryRow(ctx, `INSERT INTO users (name) VALUES ($1)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, owner).Scan(&userID)
	if err != nil {
		return 0, err
	}
	var repoID int64
	err = tx.QueryRow(ctx, `INSERT INTO repos (owner_id, name) VALUES ($1, $2)
		ON CONFLICT (owner_id, name) DO UPDATE SET name = EXCLUDED.name RETURNING id`, userID, name).Scan(&repoID)
	return repoID, err
}

// KnownCommits returns the ids of every commit already indexed for repoID.
func KnownCommits(ctx context.Context, tx pgx.Tx, repoID int64) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM commits WHERE repo_id = $1`, repoID)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// InsertCommits adds commits (none of them indexed yet) in one COPY.
func InsertCommits(ctx context.Context, tx pgx.Tx, repoID int64, cs []Commit) error {
	if len(cs) == 0 {
		return nil
	}
	_, err := tx.CopyFrom(ctx, pgx.Identifier{"commits"},
		[]string{"repo_id", "id", "tree", "parents", "author_name", "author_email", "authored_at", "author_tz",
			"committer_name", "committer_email", "committed_at", "committer_tz", "message"},
		pgx.CopyFromSlice(len(cs), func(i int) ([]any, error) {
			c := cs[i]
			parents := c.Parents
			if parents == nil {
				parents = []string{}
			}
			return []any{repoID, c.ID, c.Tree, parents, c.Author.Name, c.Author.Email, c.Author.When, tzMinutes(c.Author.When),
				c.Committer.Name, c.Committer.Email, c.Committer.When, tzMinutes(c.Committer.When), c.Message}, nil
		}))
	return err
}

// SyncRefs makes the repo's branches exactly branches (name → commit id)
// and sets its HEAD branch. Returns whether any branch changed.
func SyncRefs(ctx context.Context, tx pgx.Tx, repoID int64, head string, branches map[string]string) (bool, error) {
	names := make([]string, 0, len(branches))
	ids := make([]string, 0, len(branches))
	for n, id := range branches {
		names = append(names, n)
		ids = append(ids, id)
	}
	del, err := tx.Exec(ctx, `DELETE FROM refs WHERE repo_id = $1 AND NOT (branch = ANY($2))`, repoID, names)
	if err != nil {
		return false, err
	}
	up, err := tx.Exec(ctx, `INSERT INTO refs (repo_id, branch, commit_id)
		SELECT $1, b, c FROM unnest($2::text[], $3::text[]) AS t(b, c)
		ON CONFLICT (repo_id, branch) DO UPDATE SET commit_id = EXCLUDED.commit_id, updated_at = now()
		WHERE refs.commit_id <> EXCLUDED.commit_id`, repoID, names, ids)
	if err != nil {
		return false, err
	}
	changed := del.RowsAffected() > 0 || up.RowsAffected() > 0
	tag, err := tx.Exec(ctx, `UPDATE repos SET head_branch = $2, indexed_at = now(),
		updated_at = CASE WHEN $3 THEN now() ELSE updated_at END WHERE id = $1`, repoID, head, changed)
	if err == nil && tag.RowsAffected() != 1 {
		err = fmt.Errorf("repo %d vanished", repoID)
	}
	return changed, err
}

// --- Reads (used by the HTTP API) ---

const repoSelect = `SELECT r.id, u.name, r.name, r.head_branch, r.created_at, r.updated_at, r.indexed_at,
	(SELECT count(*) FROM refs WHERE repo_id = r.id),
	(SELECT count(*) FROM commits WHERE repo_id = r.id)
	FROM repos r JOIN users u ON u.id = r.owner_id`

func scanRepo(row pgx.Row) (Repo, error) {
	var r Repo
	err := row.Scan(&r.ID, &r.Owner, &r.Name, &r.HeadBranch, &r.CreatedAt, &r.UpdatedAt, &r.IndexedAt, &r.Branches, &r.Commits)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ListRepos returns every repo, most recently updated first, each with
// its HEAD branch's latest commit.
func (d *DB) ListRepos(ctx context.Context) ([]Repo, error) {
	rows, err := d.Pool.Query(ctx, repoSelect+` ORDER BY r.updated_at DESC, u.name, r.name`)
	if err != nil {
		return nil, err
	}
	repos, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Repo, error) { return scanRepo(row) })
	if err != nil {
		return nil, err
	}
	for i := range repos {
		if c, err := d.headCommit(ctx, repos[i]); err == nil {
			repos[i].LastCommit = &c
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	return repos, nil
}

// GetRepo returns owner/name with its HEAD branch's latest commit.
func (d *DB) GetRepo(ctx context.Context, owner, name string) (Repo, error) {
	r, err := scanRepo(d.Pool.QueryRow(ctx, repoSelect+` WHERE u.name = $1 AND r.name = $2`, owner, name))
	if err != nil {
		return r, err
	}
	if c, err := d.headCommit(ctx, r); err == nil {
		r.LastCommit = &c
	} else if !errors.Is(err, ErrNotFound) {
		return r, err
	}
	return r, nil
}

func (d *DB) headCommit(ctx context.Context, r Repo) (Commit, error) {
	return scanCommit(d.Pool.QueryRow(ctx, `SELECT `+commitCols+` FROM refs f
		JOIN commits c ON c.repo_id = f.repo_id AND c.id = f.commit_id
		WHERE f.repo_id = $1 AND f.branch = $2`, r.ID, r.HeadBranch))
}

// Branches lists repoID's branches by name, each with its tip commit.
func (d *DB) Branches(ctx context.Context, repoID int64) ([]Branch, error) {
	rows, err := d.Pool.Query(ctx, `SELECT f.branch, `+commitCols+` FROM refs f
		JOIN commits c ON c.repo_id = f.repo_id AND c.id = f.commit_id
		WHERE f.repo_id = $1 ORDER BY f.branch`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Branch
	for rows.Next() {
		var b Branch
		var atz, ctz int16
		c := &b.Commit
		if err := rows.Scan(&b.Name, &c.ID, &c.Tree, &c.Parents, &c.Author.Name, &c.Author.Email, &c.Author.When, &atz,
			&c.Committer.Name, &c.Committer.Email, &c.Committer.When, &ctz, &c.Message, &c.Summary); err != nil {
			return nil, err
		}
		c.Author.When = inZone(c.Author.When, atz)
		c.Committer.When = inZone(c.Committer.When, ctz)
		out = append(out, b)
	}
	return out, rows.Err()
}

// BranchTip returns the commit id branch points to.
func (d *DB) BranchTip(ctx context.Context, repoID int64, branch string) (string, error) {
	var id string
	err := d.Pool.QueryRow(ctx, `SELECT commit_id FROM refs WHERE repo_id = $1 AND branch = $2`, repoID, branch).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// History returns commits reachable from tip, newest committer date first
// (ties: by id), skipping offset and returning at most limit.
func (d *DB) History(ctx context.Context, repoID int64, tip string, limit, offset int) ([]Commit, error) {
	rows, err := d.Pool.Query(ctx, `WITH RECURSIVE h(id) AS (
			SELECT $2::text
			UNION
			SELECT p FROM commits c JOIN h ON c.repo_id = $1 AND c.id = h.id, unnest(c.parents) AS p
		)
		SELECT `+commitCols+` FROM commits c JOIN h ON c.id = h.id
		WHERE c.repo_id = $1
		ORDER BY c.committed_at DESC, c.id
		LIMIT $3 OFFSET $4`, repoID, tip, limit, offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Commit, error) { return scanCommit(row) })
}

// GetCommit finds a commit by full id or a prefix of 4+ characters.
func (d *DB) GetCommit(ctx context.Context, repoID int64, id string) (Commit, error) {
	id = strings.ToLower(id)
	if len(id) < 4 || len(id) > 40 || strings.Trim(id, "0123456789abcdef") != "" {
		return Commit{}, ErrNotFound
	}
	rows, err := d.Pool.Query(ctx, `SELECT `+commitCols+` FROM commits c
		WHERE c.repo_id = $1 AND c.id LIKE $2 || '%' ORDER BY c.id LIMIT 2`, repoID, id)
	if err != nil {
		return Commit{}, err
	}
	cs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Commit, error) { return scanCommit(row) })
	switch {
	case err != nil:
		return Commit{}, err
	case len(cs) == 0:
		return Commit{}, ErrNotFound
	case len(cs) > 1:
		return Commit{}, ErrAmbiguous
	}
	return cs[0], nil
}

// IsUnavailable reports whether err means the database can't be reached
// (as opposed to a bad query).
func IsUnavailable(err error) bool {
	var pe *pgconn.PgError
	return err != nil && !errors.As(err, &pe) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrAmbiguous)
}
