package config

import (
	"path/filepath"
	"testing"
)

// isolate points the home folder at a temp dir and clears env overrides.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	t.Setenv("KIRCH_AUTHOR_NAME", "")
	t.Setenv("KIRCH_AUTHOR_EMAIL", "")
	return home
}

func TestRepoBeatsGlobal(t *testing.T) {
	isolate(t)
	global, err := GlobalFile()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "config")
	if err := Set(global, "user.name", "Global Name"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := Get(repo, "user.name"); !ok || v != "Global Name" {
		t.Fatalf("Get = %q %v", v, ok)
	}
	if err := Set(repo, "user.name", "Repo Name"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := Get(repo, "user.name"); v != "Repo Name" {
		t.Fatalf("Get = %q", v)
	}
}

func TestEnvBeatsFiles(t *testing.T) {
	isolate(t)
	repo := filepath.Join(t.TempDir(), "config")
	Set(repo, "user.email", "file@example.com")
	t.Setenv("KIRCH_AUTHOR_EMAIL", "env@example.com")
	if v, _, _ := Get(repo, "user.email"); v != "env@example.com" {
		t.Fatalf("Get = %q", v)
	}
}

func TestUnset(t *testing.T) {
	isolate(t)
	if _, ok, err := Get("", "user.name"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestRejects(t *testing.T) {
	isolate(t)
	file := filepath.Join(t.TempDir(), "config")
	if err := Set(file, "core.editor", "vim"); err == nil {
		t.Error("unknown key accepted")
	}
	if err := Set(file, "user.name", "A <b>"); err == nil {
		t.Error("name with <> accepted")
	}
	if err := Set(file, "user.email", "  "); err == nil {
		t.Error("empty email accepted")
	}
}
