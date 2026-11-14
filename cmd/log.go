package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var maxCount int
	var oneline bool
	logCmd := &cobra.Command{
		Use:   "log [branch or commit]",
		Short: "Show the commit history, newest first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			rev := "HEAD"
			if len(args) == 1 {
				rev = args[0]
			}
			entries, err := r.Log(rev, maxCount)
			if err != nil {
				return err
			}
			labels, err := decorations(r)
			if err != nil {
				return err
			}
			printLog(c.OutOrStdout(), entries, labels, oneline)
			return nil
		},
	}
	logCmd.Flags().IntVarP(&maxCount, "max-count", "n", 0, "show at most this many commits")
	logCmd.Flags().BoolVar(&oneline, "oneline", false, "one line per commit: short id and subject")
	rootCmd.AddCommand(logCmd)
}

// decorations maps commit ids to the branch labels git log shows next to
// them: "HEAD -> <current>" first, then other branches in reverse name order.
func decorations(r *repo.Repo) (map[string][]string, error) {
	current, _, err := r.Refs().Head()
	if err != nil {
		return nil, err
	}
	branches, err := r.Refs().Branches()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(branches))
	for name := range branches {
		names = append(names, name)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	labels := map[string][]string{}
	if id := branches[current]; id != "" {
		labels[id] = []string{"HEAD -> " + current}
	}
	for _, name := range names {
		if name != current {
			labels[branches[name]] = append(labels[branches[name]], name)
		}
	}
	return labels, nil
}

// printLog writes entries in git log's default ("medium") or --oneline format.
func printLog(w io.Writer, entries []repo.LogEntry, labels map[string][]string, oneline bool) {
	for i, e := range entries {
		decor := ""
		if l := labels[e.ID]; len(l) > 0 {
			decor = " (" + strings.Join(l, ", ") + ")"
		}
		lines := messageLines(e.Message)
		if oneline {
			fmt.Fprintf(w, "%s%s %s\n", e.ID[:7], decor, subject(lines))
			continue
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "commit %s%s\n", e.ID, decor)
		if len(e.Parents) > 1 {
			short := make([]string, len(e.Parents))
			for j, p := range e.Parents {
				short[j] = p[:7]
			}
			fmt.Fprintf(w, "Merge: %s\n", strings.Join(short, " "))
		}
		fmt.Fprintf(w, "Author: %s <%s>\n", e.Author.Name, e.Author.Email)
		fmt.Fprintf(w, "Date:   %s\n\n", e.Author.When.Format("Mon Jan 2 15:04:05 2006 -0700"))
		for _, line := range lines {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
}

// messageLines splits a commit message into lines without trailing spaces,
// dropping blank lines at the start and end.
func messageLines(msg string) []string {
	lines := strings.Split(msg, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// subject is the first paragraph of a message, joined into one line.
func subject(lines []string) string {
	var parts []string
	for _, line := range lines {
		if line == "" {
			break
		}
		parts = append(parts, strings.TrimSpace(line))
	}
	return strings.Join(parts, " ")
}
