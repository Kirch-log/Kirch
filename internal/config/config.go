// Package config stores settings such as the author name for commits.
//
// Two JSON files, repo settings win over global ones:
//
//	<repo>/.kirch/config.json   this repository only (not "config": git reads that name)
//	~/.kirchconfig              every repository on this computer
//
// Environment variables KIRCH_AUTHOR_NAME and KIRCH_AUTHOR_EMAIL override both.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Keys that can be set.
var Keys = map[string]string{
	"user.name":  "KIRCH_AUTHOR_NAME",
	"user.email": "KIRCH_AUTHOR_EMAIL",
}

// GlobalFile returns ~/.kirchconfig.
func GlobalFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kirchconfig"), nil
}

// RepoFile returns the config file inside a .kirch folder.
func RepoFile(kirchDir string) string { return filepath.Join(kirchDir, "config.json") }

func checkKey(key string) error {
	if _, ok := Keys[key]; !ok {
		names := make([]string, 0, len(Keys))
		for k := range Keys {
			names = append(names, k)
		}
		sort.Strings(names)
		return fmt.Errorf("unknown setting %q (known: %s)", key, strings.Join(names, ", "))
	}
	return nil
}

func load(file string) (map[string]string, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", file, err)
	}
	return m, nil
}

// Set writes key=value into file.
func Set(file, key, value string) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if strings.ContainsAny(value, "<>\n") || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s can't be empty or contain <, > or newlines", key)
	}
	m, err := load(file)
	if err != nil {
		return err
	}
	m[key] = value
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(file, append(data, '\n'), 0o644)
}

// Get returns a setting from the environment, then the repo file, then the
// global file. repoFile may be "" outside a repository. ok is false if unset.
func Get(repoFile, key string) (value string, ok bool, err error) {
	if err := checkKey(key); err != nil {
		return "", false, err
	}
	if v := os.Getenv(Keys[key]); v != "" {
		return v, true, nil
	}
	files := []string{}
	if repoFile != "" {
		files = append(files, repoFile)
	}
	if g, err := GlobalFile(); err == nil {
		files = append(files, g)
	}
	for _, f := range files {
		m, err := load(f)
		if err != nil {
			return "", false, err
		}
		if v, ok := m[key]; ok && v != "" {
			return v, true, nil
		}
	}
	return "", false, nil
}
