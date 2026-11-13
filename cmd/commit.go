package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/Kirch-log/kirch/internal/config"
	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var message string
	commitCmd := &cobra.Command{
		Use:   "commit -m <message>",
		Short: "Save the staged files as a new commit on the current branch",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			author, err := authorFor(r)
			if err != nil {
				return err
			}
			res, err := r.Commit(message, author)
			if err != nil {
				return err
			}
			root := ""
			if res.Parent == "" {
				root = " (root-commit)"
			}
			fmt.Fprintf(c.OutOrStdout(), "[%s%s %s] %s\n", res.Branch, root, res.ID[:7], firstLine(message))
			return nil
		},
	}
	commitCmd.Flags().StringVarP(&message, "message", "m", "", "commit message")
	_ = commitCmd.MarkFlagRequired("message")

	var global bool
	configCmd := &cobra.Command{
		Use:   "config [--global] <key> [value]",
		Short: "Show or set user.name / user.email",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			key := args[0]
			repoFile := ""
			if r, err := currentRepo(); err == nil {
				repoFile = config.RepoFile(r.Dir)
			} else if !global && len(args) == 2 {
				return fmt.Errorf("%w; use --global to set it for every repository", err)
			}
			if len(args) == 1 {
				v, ok, err := config.Get(repoFile, key)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%s is not set", key)
				}
				fmt.Fprintln(c.OutOrStdout(), v)
				return nil
			}
			file := repoFile
			if global {
				var err error
				if file, err = config.GlobalFile(); err != nil {
					return err
				}
			}
			return config.Set(file, key, args[1])
		},
	}
	configCmd.Flags().BoolVar(&global, "global", false, "use ~/.kirchconfig (all repositories)")

	rootCmd.AddCommand(commitCmd, configCmd)
}

var errNoAuthor = errors.New(`who are you? Set your name and email once:
  kirch config --global user.name "Your Name"
  kirch config --global user.email "you@example.com"`)

func authorFor(r *repo.Repo) (objects.Signature, error) {
	file := config.RepoFile(r.Dir)
	name, okName, err := config.Get(file, "user.name")
	if err != nil {
		return objects.Signature{}, err
	}
	email, okEmail, err := config.Get(file, "user.email")
	if err != nil {
		return objects.Signature{}, err
	}
	if !okName || !okEmail {
		return objects.Signature{}, errNoAuthor
	}
	return objects.Signature{Name: name, Email: email, When: time.Now()}, nil
}

func firstLine(s string) string {
	for i, ch := range s {
		if ch == '\n' {
			return s[:i]
		}
	}
	return s
}
