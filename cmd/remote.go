package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	remoteCmd := &cobra.Command{
		Use:   "remote [add <name> <folder>]",
		Short: "List remotes, or add one (another kirch repository folder)",
		Args:  cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			switch {
			case len(args) == 0:
				m, err := r.Remotes()
				if err != nil {
					return err
				}
				names := make([]string, 0, len(m))
				for n := range m {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					fmt.Fprintf(c.OutOrStdout(), "%s\t%s\n", n, m[n])
				}
				return nil
			case args[0] == "add" && len(args) == 3:
				return r.AddRemote(args[1], args[2])
			}
			return fmt.Errorf("usage: kirch remote [add <name> <folder>]")
		},
	}

	cloneCmd := &cobra.Command{
		Use:   "clone <folder> [new folder]",
		Short: "Copy a repository into a new folder, with the original as remote \"origin\"",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			dest := filepath.Base(filepath.Clean(args[0]))
			if len(args) == 2 {
				dest = args[1]
			}
			fmt.Fprintf(c.OutOrStdout(), "Cloning into '%s'...\n", dest)
			_, err := repo.Clone(args[0], dest)
			return err
		},
	}

	fetchCmd := &cobra.Command{
		Use:   "fetch [remote]",
		Short: "Download the remote's branches (as origin/<branch>) without changing your files",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			remote := argOr(args, 0, "origin")
			ups, err := r.Fetch(remote)
			if err != nil {
				return err
			}
			printFetch(c.OutOrStdout(), r, remote, ups)
			return nil
		},
	}

	pushCmd := &cobra.Command{
		Use:   "push [remote] [branch]",
		Short: "Send a branch (default: current) to the remote",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			remote := argOr(args, 0, "origin")
			up, err := r.Push(remote, argOr(args, 1, ""))
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			if up.Old == up.New {
				fmt.Fprintln(out, "Everything up-to-date")
				return nil
			}
			_, folder, err := r.OpenRemote(remote)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "To %s\n", folder)
			if up.Old == "" {
				fmt.Fprintf(out, " * [new branch]      %s -> %s\n", up.Branch, up.Branch)
			} else {
				fmt.Fprintf(out, "   %s..%s  %s -> %s\n", up.Old[:7], up.New[:7], up.Branch, up.Branch)
			}
			return nil
		},
	}

	pullCmd := &cobra.Command{
		Use:   "pull [remote] [branch]",
		Short: "Fetch from the remote and merge its branch (default: same name as current) into yours",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			author, err := authorFor(r)
			if err != nil {
				return err
			}
			remote := argOr(args, 0, "origin")
			ups, res, err := r.Pull(remote, argOr(args, 1, ""), author)
			out := c.OutOrStdout()
			printFetch(out, r, remote, ups)
			if err != nil {
				return err
			}
			switch {
			case res.UpToDate:
				fmt.Fprintln(out, "Already up to date.")
			case res.FastForward:
				from := "(no commits)"
				if res.From != "" {
					from = res.From[:7]
				}
				fmt.Fprintf(out, "Updating %s..%s\nFast-forward\n", from, res.To[:7])
			case len(res.Conflicts) > 0:
				printConflicts(out, res.Conflicts)
				fmt.Fprintln(out, "Automatic merge failed; fix conflicts, kirch add the files, then kirch commit.")
				return errSilentFail
			default:
				printMergeMade(out, r, res)
			}
			return nil
		},
	}

	rootCmd.AddCommand(remoteCmd, cloneCmd, fetchCmd, pushCmd, pullCmd)
}

func argOr(args []string, i int, def string) string {
	if i < len(args) && args[i] != "" {
		return args[i]
	}
	return def
}

// printFetch lists moved remote-tracking branches like git fetch.
func printFetch(w io.Writer, r *repo.Repo, remote string, ups []repo.RefUpdate) {
	if len(ups) == 0 {
		return
	}
	if _, folder, err := r.OpenRemote(remote); err == nil {
		fmt.Fprintf(w, "From %s\n", folder)
	}
	for _, u := range ups {
		ref := remote + "/" + u.Branch
		switch {
		case u.New == "":
			fmt.Fprintf(w, " - [deleted]         %s\n", ref)
		case u.Old == "":
			fmt.Fprintf(w, " * [new branch]      %s -> %s\n", u.Branch, ref)
		default:
			fmt.Fprintf(w, "   %s..%s  %s -> %s\n", u.Old[:7], u.New[:7], padRight(u.Branch, 10), ref)
		}
	}
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
