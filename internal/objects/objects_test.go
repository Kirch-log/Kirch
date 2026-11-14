package objects

import (
	"bytes"
	"compress/zlib"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Known git ids: `printf 'hello\n' | git hash-object --stdin` etc.
func TestHashMatchesKnownGitIDs(t *testing.T) {
	cases := []struct {
		content string
		want    string
	}{
		{"", "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"},
		{"hello\n", "ce013625030ba8dba906f756967f9e9ca394464a"},
	}
	for _, c := range cases {
		if got := Hash(Blob, []byte(c.content)); got != c.want {
			t.Errorf("Hash(blob, %q) = %s, want %s", c.content, got, c.want)
		}
	}
}

func TestWriteThenRead(t *testing.T) {
	s := NewStore(t.TempDir())
	content := []byte(`{"format": "kirch.netlist/1", "nets": {}}` + "\n")

	id, err := s.Write(Blob, content)
	if err != nil {
		t.Fatal(err)
	}
	if id != Hash(Blob, content) {
		t.Fatalf("Write id %s != Hash %s", id, Hash(Blob, content))
	}
	if !s.Has(id) {
		t.Fatal("Has = false after Write")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, id[:2], id[2:])); err != nil {
		t.Fatalf("object not at git path: %v", err)
	}

	typ, got, err := s.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if typ != Blob || !bytes.Equal(got, content) {
		t.Fatalf("Read = (%s, %q), want (blob, %q)", typ, got, content)
	}
}

func TestWriteTwiceIsNoOp(t *testing.T) {
	s := NewStore(t.TempDir())
	a, err := s.Write(Blob, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Write(Blob, []byte("same"))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("ids differ: %s %s", a, b)
	}
}

func TestBinaryContent(t *testing.T) {
	s := NewStore(t.TempDir())
	content := []byte{0, 1, 2, 0, 255, 0}
	id, err := s.Write(Blob, content)
	if err != nil {
		t.Fatal(err)
	}
	_, got, err := s.Read(id)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("Read = %v, %v", got, err)
	}
}

func TestReadMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _, err := s.Read(Hash(Blob, []byte("never written")))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestReadRejectsBadIDs(t *testing.T) {
	s := NewStore(t.TempDir())
	for _, id := range []string{"", "abc", strings.Repeat("g", 40), strings.Repeat("A", 40), "../../etc/passwd"} {
		if _, _, err := s.Read(id); err == nil {
			t.Errorf("Read(%q) succeeded", id)
		}
	}
}

func TestWriteRejectsUnknownType(t *testing.T) {
	if _, err := NewStore(t.TempDir()).Write("tag", []byte("x")); err == nil {
		t.Fatal("Write with type tag succeeded")
	}
}

// writeRaw stores arbitrary bytes under id, to simulate damaged objects.
func writeRaw(t *testing.T, s *Store, id string, raw []byte, compress bool) {
	t.Helper()
	path := filepath.Join(s.Dir, id[:2], id[2:])
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := raw
	if compress {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		zw.Write(raw)
		zw.Close()
		data = buf.Bytes()
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadDetectsCorruption(t *testing.T) {
	id := Hash(Blob, []byte("hello\n"))
	cases := map[string]struct {
		raw      []byte
		compress bool
	}{
		"not zlib":        {[]byte("plain text"), false},
		"no header end":   {[]byte("blob 6"), true},
		"wrong size":      {[]byte("blob 99\x00hello\n"), true},
		"unknown type":    {[]byte("tag 6\x00hello\n"), true},
		"content swapped": {[]byte("blob 6\x00HELLO\n"), true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := NewStore(t.TempDir())
			writeRaw(t, s, id, c.raw, c.compress)
			if _, _, err := s.Read(id); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// Real git as the test oracle. Skipped when git isn't installed.

func gitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	return p
}

func TestSameIDAsGitHashObject(t *testing.T) {
	git := gitPath(t)
	contents := []string{"", "hello\n", "no newline", `{"nets": {"GND": ["U1.GND"]}}`, strings.Repeat("x", 100000)}
	for _, c := range contents {
		cmd := exec.Command(git, "hash-object", "--stdin")
		cmd.Stdin = strings.NewReader(c)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.TrimSpace(string(out))
		if got := Hash(Blob, []byte(c)); got != want {
			t.Errorf("len %d: Hash = %s, git = %s", len(c), got, want)
		}
	}
}

// Objects we write must be readable by git itself (`git cat-file`).
func TestGitCanReadOurObjects(t *testing.T) {
	git := gitPath(t)
	repo := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	s := NewStore(filepath.Join(repo, ".git", "objects"))
	content := "ECHO_3V3: R1.2 R2.1 U1.GPIO16\n"
	id, err := s.Write(Blob, []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(git, "-C", repo, "cat-file", "-p", id).CombinedOutput()
	if err != nil {
		t.Fatalf("git cat-file: %v %s", err, out)
	}
	if string(out) != content {
		t.Fatalf("git read %q, want %q", out, content)
	}
	typ, err := exec.Command(git, "-C", repo, "cat-file", "-t", id).Output()
	if err != nil || strings.TrimSpace(string(typ)) != "blob" {
		t.Fatalf("git type = %q, %v", typ, err)
	}
}

// And we must read objects git wrote.
func TestWeCanReadGitObjects(t *testing.T) {
	git := gitPath(t)
	repo := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	content := "written by real git\n"
	cmd := exec.Command(git, "-C", repo, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(out))

	typ, got, err := NewStore(filepath.Join(repo, ".git", "objects")).Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if typ != Blob || string(got) != content {
		t.Fatalf("Read = (%s, %q)", typ, got)
	}
}
