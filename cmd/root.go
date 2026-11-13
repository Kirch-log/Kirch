// Package cmd holds the Cobra commands. They only parse arguments and print;
// all real work is in internal/ so the agent can call the same functions.
package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const dataDir = ".kirch"

var rootCmd = &cobra.Command{
	Use:           "kirch",
	Short:         "Version control for hardware circuits",
	SilenceUsage:  true,
	SilenceErrors: false,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

var errNoRepo = errors.New("not a kirch repository (no .kirch folder here or above)")

// findRepo walks up from the current folder to the one containing .kirch.
// Temporary until `kirch init` (step 2) adds the real repo package.
func findRepo() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, dataDir)); err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errNoRepo
		}
		dir = parent
	}
}
