package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var short bool
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show staged, changed and untracked files",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			st, err := r.Status()
			if err != nil {
				return err
			}
			if short {
				printShortStatus(c.OutOrStdout(), st)
			} else {
				printLongStatus(c.OutOrStdout(), st)
			}
			return nil
		},
	}
	statusCmd.Flags().BoolVarP(&short, "short", "s", false, `one line per file, "XY path" (same as git status --porcelain)`)
	statusCmd.Flags().BoolVar(&short, "porcelain", false, "same as --short")
	rootCmd.AddCommand(statusCmd)
}

// printShortStatus writes git's porcelain v1 format: two status letters
// (staged, unstaged) and the path; untracked files as "?? path".
func printShortStatus(w io.Writer, st *repo.Status) {
	for _, f := range st.Files {
		fmt.Fprintf(w, "%c%c %s\n", f.Staged, f.Unstaged, quotePath(f.Path))
	}
	for _, p := range st.Untracked {
		fmt.Fprintf(w, "?? %s\n", quotePath(p))
	}
}

func printLongStatus(w io.Writer, st *repo.Status) {
	fmt.Fprintf(w, "On branch %s\n", st.Branch)
	if st.Head == "" {
		fmt.Fprintf(w, "\nNo commits yet\n")
	}
	names := map[byte]string{repo.Added: "new file:", repo.Modified: "modified:", repo.Deleted: "deleted:"}
	section := func(title string, pick func(repo.FileStatus) byte) bool {
		printed := false
		for _, f := range st.Files {
			change := pick(f)
			if change == repo.Unchanged {
				continue
			}
			if !printed {
				fmt.Fprintf(w, "\n%s\n", title)
				printed = true
			}
			fmt.Fprintf(w, "\t%-11s %s\n", names[change], f.Path)
		}
		return printed
	}
	staged := section("Changes to be committed:", func(f repo.FileStatus) byte { return f.Staged })
	unstaged := section("Changes not staged for commit (use \"kirch add\"):", func(f repo.FileStatus) byte { return f.Unstaged })
	if len(st.Untracked) > 0 {
		fmt.Fprintf(w, "\nUntracked files (use \"kirch add\" to track):\n")
		for _, p := range st.Untracked {
			fmt.Fprintf(w, "\t%s\n", p)
		}
	}

	fmt.Fprintln(w)
	switch {
	case staged:
	case unstaged:
		fmt.Fprintln(w, "no changes added to commit")
	case len(st.Untracked) > 0:
		fmt.Fprintln(w, "nothing added to commit but untracked files present")
	case st.Head == "":
		fmt.Fprintln(w, "nothing to commit (create files and use \"kirch add\" to track)")
	default:
		fmt.Fprintln(w, "nothing to commit, working tree clean")
	}
}

// quotePath quotes a path the way git status --porcelain does: in double
// quotes, with C escapes and octal bytes, when it has a space, quote,
// backslash, control or non-ASCII character.
func quotePath(p string) string {
	needs := false
	for i := 0; i < len(p); i++ {
		if c := p[i]; c <= ' ' || c == '"' || c == '\\' || c >= 0x7f {
			needs = true
			break
		}
	}
	if !needs {
		return p
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\a':
			b.WriteString(`\a`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c < ' ' || c >= 0x7f {
				fmt.Fprintf(&b, "\\%03o", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
