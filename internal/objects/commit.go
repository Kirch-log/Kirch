package objects

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signature is who made a commit and when.
type Signature struct {
	Name  string
	Email string
	When  time.Time // keeps its time zone
}

// String formats as git does: "Name <email> 1700000000 +0530".
func (s Signature) String() string {
	_, offset := s.When.Zone()
	sign := '+'
	if offset < 0 {
		sign = '-'
		offset = -offset
	}
	return fmt.Sprintf("%s <%s> %d %c%02d%02d", s.Name, s.Email, s.When.Unix(), sign, offset/3600, offset%3600/60)
}

func (s Signature) check() error {
	if strings.ContainsAny(s.Name, "<>\n") || strings.ContainsAny(s.Email, "<>\n") {
		return errors.New("name and email can't contain <, > or newlines")
	}
	if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Email) == "" {
		return errors.New("name and email are required")
	}
	return nil
}

// ParseSignature reads "Name <email> 1700000000 +0530".
func ParseSignature(line string) (Signature, error) {
	lt := strings.IndexByte(line, '<')
	gt := strings.LastIndexByte(line, '>')
	if lt < 0 || gt < lt {
		return Signature{}, fmt.Errorf("bad signature %q", line)
	}
	name := strings.TrimSpace(line[:lt])
	email := line[lt+1 : gt]
	fields := strings.Fields(line[gt+1:])
	if len(fields) != 2 || len(fields[1]) != 5 {
		return Signature{}, fmt.Errorf("bad signature time %q", line)
	}
	secs, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return Signature{}, fmt.Errorf("bad signature time %q", line)
	}
	tz := fields[1]
	hh, err1 := strconv.Atoi(tz[1:3])
	mm, err2 := strconv.Atoi(tz[3:5])
	if err1 != nil || err2 != nil || (tz[0] != '+' && tz[0] != '-') {
		return Signature{}, fmt.Errorf("bad time zone %q", tz)
	}
	offset := hh*3600 + mm*60
	if tz[0] == '-' {
		offset = -offset
	}
	when := time.Unix(secs, 0).In(time.FixedZone(tz, offset))
	return Signature{Name: name, Email: email, When: when}, nil
}

// CommitData is a decoded commit object.
type CommitData struct {
	Tree      string
	Parents   []string
	Author    Signature
	Committer Signature
	Message   string // always ends with "\n"
}

// EncodeCommit returns git's commit format:
//
//	tree <id>
//	parent <id>        (zero or more)
//	author <signature>
//	committer <signature>
//
//	<message>
func EncodeCommit(c CommitData) ([]byte, error) {
	if !ValidID(c.Tree) {
		return nil, fmt.Errorf("bad tree id %q", c.Tree)
	}
	for _, p := range c.Parents {
		if !ValidID(p) {
			return nil, fmt.Errorf("bad parent id %q", p)
		}
	}
	if err := c.Author.check(); err != nil {
		return nil, fmt.Errorf("author: %w", err)
	}
	if err := c.Committer.check(); err != nil {
		return nil, fmt.Errorf("committer: %w", err)
	}
	msg := c.Message
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "tree %s\n", c.Tree)
	for _, p := range c.Parents {
		fmt.Fprintf(&buf, "parent %s\n", p)
	}
	fmt.Fprintf(&buf, "author %s\n", c.Author)
	fmt.Fprintf(&buf, "committer %s\n", c.Committer)
	buf.WriteString("\n")
	buf.WriteString(msg)
	return buf.Bytes(), nil
}

// ParseCommit decodes a commit object. Headers it doesn't know (e.g. gpgsig
// from real git) are skipped.
func ParseCommit(data []byte) (CommitData, error) {
	var c CommitData
	headerEnd := bytes.Index(data, []byte("\n\n"))
	if headerEnd < 0 {
		return c, errors.New("commit: no blank line before message")
	}
	c.Message = string(data[headerEnd+2:])
	lines := strings.Split(string(data[:headerEnd]), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, " ") {
			continue // continuation of a multi-line header
		}
		key, value, _ := strings.Cut(line, " ")
		var err error
		switch key {
		case "tree":
			c.Tree = value
		case "parent":
			c.Parents = append(c.Parents, value)
		case "author":
			c.Author, err = ParseSignature(value)
		case "committer":
			c.Committer, err = ParseSignature(value)
		}
		if err != nil {
			return c, fmt.Errorf("commit: %w", err)
		}
	}
	if !ValidID(c.Tree) {
		return c, errors.New("commit: missing tree")
	}
	return c, nil
}
