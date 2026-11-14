package repo

import (
	"bufio"
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IgnoreFile lists files kirch add . and kirch status skip, like .gitignore.
const IgnoreFile = ".kirchignore"

// Supported .kirchignore lines (a subset of .gitignore):
//
//	# comment
//	*.exe          any file or folder with that name, in any folder
//	build/         a folder (and everything in it), in any folder
//	/notes.txt     only at the top of the repository
//	docs/*.pdf     a pattern with a / is matched from the top
//	!keep.exe      un-ignore something an earlier line ignored
//
// Patterns use * ? [abc] (no **). Files that are already tracked are never
// ignored, and naming a file directly (kirch add secret.txt) adds it anyway.
type ignoreRule struct {
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool // has a slash: match the whole path from the top
}

type ignorer []ignoreRule

func (r *Repo) loadIgnore() ignorer {
	data, err := os.ReadFile(filepath.Join(r.Root, IgnoreFile))
	if err != nil {
		return nil
	}
	var rules ignorer
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rule ignoreRule
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			rule.dirOnly = true
			line = strings.TrimRight(line, "/")
		}
		if strings.Contains(line, "/") {
			rule.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if line == "" {
			continue
		}
		if _, err := path.Match(line, ""); err != nil {
			continue // bad pattern: skip it rather than fail every command
		}
		rule.pattern = line
		rules = append(rules, rule)
	}
	return rules
}

// matches reports whether the path itself (not its folders) is ignored.
// The last matching line wins.
func (ig ignorer) matches(rel string, isDir bool) bool {
	ignored := false
	for _, rule := range ig {
		if rule.dirOnly && !isDir {
			continue
		}
		target := path.Base(rel)
		if rule.anchored {
			target = rel
		}
		if ok, _ := path.Match(rule.pattern, target); ok {
			ignored = !rule.negate
		}
	}
	return ignored
}
