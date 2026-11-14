package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/repo"
)

func TestResetAndRevertCommands(t *testing.T) {
	root := t.TempDir()
	if _, err := repo.Init(root); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	t.Setenv("KIRCH_AUTHOR_NAME", "T")
	t.Setenv("KIRCH_AUTHOR_EMAIL", "t@example.com")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commit := func(msg string) string {
		t.Helper()
		runKirch(t, "add", ".")
		out := runKirch(t, "commit", "-m", msg)
		head, _, _ := strings.Cut(out, "]")
		fields := strings.Fields(head)
		return fields[len(fields)-1] // short id, e.g. "[main abc1234] msg"
	}
	write("a.txt", "1\n")
	first := commit("one")
	write("a.txt", "2\n")
	second := commit("two")

	write("a.txt", "3\n")
	runKirch(t, "add", "a.txt")
	if got := runKirch(t, "reset", "a.txt"); got != "Unstaged changes after reset:\nM\ta.txt\n" {
		t.Fatalf("reset path = %q", got)
	}
	if got := runKirch(t, "reset", "--hard"); got != "HEAD is now at "+second[:7]+" two\n" {
		t.Fatalf("reset --hard = %q", got)
	}
	if got := runKirch(t, "revert", second); !strings.HasPrefix(got, "[main ") || !strings.HasSuffix(got, "] Revert \"two\"\n") {
		t.Fatalf("revert = %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(data) != "1\n" {
		t.Fatalf("a.txt = %q", data)
	}
	if got := runKirch(t, "reset", first); got != "Unstaged changes after reset:\n" && got != "" {
		t.Fatalf("reset = %q", got)
	}
	if got := runKirch(t, "status", "--porcelain"); got != "" {
		t.Fatalf("status after reset to one = %q", got)
	}
	if got := runKirch(t, "reset", "--soft", second); got != "" {
		t.Fatalf("reset --soft = %q", got)
	}
	if got := runKirch(t, "status", "--porcelain"); got != "M  a.txt\n" {
		t.Fatalf("status after soft reset = %q", got)
	}
}
