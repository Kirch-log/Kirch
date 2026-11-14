package diff

import (
	"bytes"
	"fmt"
	"io"
	"strings"
)

// Context is how many unchanged lines are shown around a change (git's default).
const Context = 3

// File is one file's change. An empty ID means the file is absent on that side.
type File struct {
	Path             string
	OldID, NewID     string
	OldMode, NewMode string
	Old, New         []byte
}

const zeroID = "0000000"

// WriteHeader writes the "diff --git" header lines git puts before a patch.
func WriteHeader(w io.Writer, f File) {
	fmt.Fprintf(w, "diff --git a/%s b/%s\n", f.Path, f.Path)
	switch {
	case f.OldID == "":
		fmt.Fprintf(w, "new file mode %s\nindex %s..%s\n", f.NewMode, zeroID, f.NewID[:7])
	case f.NewID == "":
		fmt.Fprintf(w, "deleted file mode %s\nindex %s..%s\n", f.OldMode, f.OldID[:7], zeroID)
	case f.OldMode != f.NewMode:
		fmt.Fprintf(w, "old mode %s\nnew mode %s\n", f.OldMode, f.NewMode)
		if f.OldID != f.NewID {
			fmt.Fprintf(w, "index %s..%s\n", f.OldID[:7], f.NewID[:7])
		}
	default:
		fmt.Fprintf(w, "index %s..%s %s\n", f.OldID[:7], f.NewID[:7], f.OldMode)
	}
}

// WritePatch writes the change in git's patch format (what git diff prints,
// and what git apply accepts).
func WritePatch(w io.Writer, f File) {
	WriteHeader(w, f)
	if f.OldID == f.NewID {
		return
	}
	oldName, newName := "a/"+f.Path, "b/"+f.Path
	if f.OldID == "" {
		oldName = "/dev/null"
	}
	if f.NewID == "" {
		newName = "/dev/null"
	}
	if IsBinary(f.Old) || IsBinary(f.New) {
		fmt.Fprintf(w, "Binary files %s and %s differ\n", oldName, newName)
		return
	}
	a, b := SplitLines(f.Old), SplitLines(f.New)
	hunks := Hunks(a, b)
	if len(hunks) == 0 {
		return
	}
	fmt.Fprintf(w, "--- %s\n+++ %s\n", oldName, newName)
	for _, h := range hunks {
		h.write(w, a, b)
	}
}

// IsBinary uses git's rule: a NUL byte in the first 8000 bytes.
func IsBinary(data []byte) bool {
	if len(data) > 8000 {
		data = data[:8000]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// SplitLines splits text into lines, each keeping its "\n" (the last one may
// have none).
func SplitLines(data []byte) []string {
	var lines []string
	s := string(data)
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i+1])
		s = s[i+1:]
	}
	return lines
}

// Hunk is a group of nearby changes with their context lines.
type Hunk struct {
	Edits []Edit
}

// Hunks groups the line changes from a to b into hunks: Context lines
// around each change, and changes closer than 2*Context lines share a hunk.
func Hunks(a, b []string) []Hunk {
	edits := Lines(a, b)
	var hunks []Hunk
	i := 0
	for i < len(edits) {
		if edits[i].Op == Equal {
			i++
			continue
		}
		start := max(i-Context, 0)
		end := i // one past the last change in this hunk
		for end < len(edits) {
			if edits[end].Op != Equal {
				end++
				continue
			}
			gap := end
			for gap < len(edits) && edits[gap].Op == Equal {
				gap++
			}
			if gap == len(edits) || gap-end > 2*Context {
				break
			}
			end = gap
		}
		stop := min(end+Context, len(edits))
		hunks = append(hunks, Hunk{Edits: edits[start:stop]})
		i = stop
	}
	return hunks
}

func (h Hunk) write(w io.Writer, a, b []string) {
	oldStart, newStart := -1, -1
	oldCount, newCount := 0, 0
	for _, e := range h.Edits {
		if e.Op != Insert {
			if oldStart < 0 {
				oldStart = e.A
			}
			oldCount++
		}
		if e.Op != Delete {
			if newStart < 0 {
				newStart = e.B
			}
			newCount++
		}
	}
	// A side with no lines in the hunk is placed after its line before.
	if oldStart < 0 {
		oldStart = h.Edits[0].A
	}
	if newStart < 0 {
		newStart = h.Edits[0].B
	}
	header := fmt.Sprintf("@@ -%s +%s @@", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))
	if fn := funcName(a, oldStart); fn != "" {
		header += " " + fn
	}
	fmt.Fprintln(w, header)

	// Within each run of changes git prints all removed lines, then all added.
	for i := 0; i < len(h.Edits); {
		if h.Edits[i].Op == Equal {
			writeLine(w, ' ', a[h.Edits[i].A])
			i++
			continue
		}
		j := i
		for j < len(h.Edits) && h.Edits[j].Op != Equal {
			j++
		}
		for _, e := range h.Edits[i:j] {
			if e.Op == Delete {
				writeLine(w, '-', a[e.A])
			}
		}
		for _, e := range h.Edits[i:j] {
			if e.Op == Insert {
				writeLine(w, '+', b[e.B])
			}
		}
		i = j
	}
}

func hunkRange(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start)
	case 1:
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, count)
}

func writeLine(w io.Writer, mark byte, line string) {
	if strings.HasSuffix(line, "\n") {
		fmt.Fprintf(w, "%c%s", mark, line)
		return
	}
	fmt.Fprintf(w, "%c%s\n\\ No newline at end of file\n", mark, line)
}

// funcName is git's default hunk-header context: the nearest line above the
// hunk that starts with a letter, '_' or '$', trailing space cut, at most 80 bytes.
func funcName(lines []string, before int) string {
	for i := before - 1; i >= 0; i-- {
		l := lines[i]
		if l == "" {
			continue
		}
		c := l[0]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$' {
			if len(l) > 80 {
				l = l[:80]
			}
			return strings.TrimRight(l, " \t\r\n\v\f")
		}
	}
	return ""
}
