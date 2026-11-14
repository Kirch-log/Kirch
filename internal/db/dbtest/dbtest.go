// Package dbtest gives tests a real, empty, migrated Postgres database.
//
// It uses KIRCH_TEST_DATABASE_URL (a server where it may create and drop
// databases) if set. Otherwise it starts a throwaway Postgres with the
// initdb / pg_ctl found in PATH, listening only on a Unix socket, and stops
// it when the package's tests finish. With neither, tests are skipped,
// unless KIRCH_REQUIRE_DB=1 (CI), which makes them fail instead.
//
// Packages using it call dbtest.Main from TestMain.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Kirch-log/kirch/internal/db"
	"github.com/jackc/pgx/v5"
)

var (
	once     sync.Once
	adminURL string
	setupErr error
	cluster  string // data dir of the throwaway server, if we started one
	counter  atomic.Int64
)

// Main runs the tests, then stops the throwaway server if one was started.
func Main(m *testing.M) {
	code := m.Run()
	if cluster != "" {
		exec.Command("pg_ctl", "-D", cluster, "-m", "immediate", "stop").Run()
		os.RemoveAll(filepath.Dir(cluster))
	}
	os.Exit(code)
}

func setup() {
	if u := os.Getenv("KIRCH_TEST_DATABASE_URL"); u != "" {
		adminURL = u
		return
	}
	for _, tool := range []string{"initdb", "pg_ctl"} {
		if _, err := exec.LookPath(tool); err != nil {
			setupErr = fmt.Errorf("no KIRCH_TEST_DATABASE_URL and no %s in PATH", tool)
			return
		}
	}
	base, err := os.MkdirTemp("", "kpg")
	if err != nil {
		setupErr = err
		return
	}
	data := filepath.Join(base, "data")
	if out, err := exec.Command("initdb", "-D", data, "-U", "postgres", "-A", "trust", "--no-sync", "-E", "UTF8").CombinedOutput(); err != nil {
		setupErr = fmt.Errorf("initdb: %v\n%s", err, out)
		return
	}
	opts := fmt.Sprintf("-c listen_addresses='' -k %s -c fsync=off -c full_page_writes=off", base)
	if out, err := exec.Command("pg_ctl", "-D", data, "-o", opts, "-w", "-l", filepath.Join(base, "log"), "start").CombinedOutput(); err != nil {
		setupErr = fmt.Errorf("pg_ctl start: %v\n%s", err, out)
		return
	}
	cluster = data
	adminURL = fmt.Sprintf("host=%s user=postgres dbname=postgres", base)
}

// New returns a fresh, migrated database dropped when t ends.
func New(t testing.TB) *db.DB {
	t.Helper()
	once.Do(setup)
	if setupErr != nil {
		if os.Getenv("KIRCH_REQUIRE_DB") == "1" {
			t.Fatalf("database required: %v", setupErr)
		}
		t.Skipf("no Postgres for tests: %v", setupErr)
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to test Postgres: %v", err)
	}
	name := fmt.Sprintf("kirch_test_%d_%d", os.Getpid(), counter.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	d, err := db.Open(ctx, withDatabase(adminURL, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Close()
		admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return d
}

// withDatabase points a connection string (URL or key=value form) at name.
func withDatabase(conn, name string) string {
	if strings.HasPrefix(conn, "postgres://") || strings.HasPrefix(conn, "postgresql://") {
		u, err := url.Parse(conn)
		if err == nil {
			u.Path = "/" + name
			return u.String()
		}
	}
	return conn + " dbname=" + name
}
