package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/spf13/cobra"
)

// Low-level commands, same names and output as git's, so results can be
// compared side by side with real git.

func init() {
	var write, stdin bool
	hashObject := &cobra.Command{
		Use:   "hash-object [file]",
		Short: "Print the object id of a file (and store it with -w)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var data []byte
			var err error
			switch {
			case stdin:
				data, err = io.ReadAll(c.InOrStdin())
			case len(args) == 1:
				data, err = os.ReadFile(args[0])
			default:
				return fmt.Errorf("give a file or --stdin")
			}
			if err != nil {
				return err
			}
			if !write {
				fmt.Fprintln(c.OutOrStdout(), objects.Hash(objects.Blob, data))
				return nil
			}
			root, err := findRepo()
			if err != nil {
				return err
			}
			id, err := objects.NewStore(filepath.Join(root, dataDir, "objects")).Write(objects.Blob, data)
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), id)
			return nil
		},
	}
	hashObject.Flags().BoolVarP(&write, "write", "w", false, "store the object in .kirch/objects")
	hashObject.Flags().BoolVar(&stdin, "stdin", false, "read content from standard input")

	var showType, showSize, pretty bool
	catFile := &cobra.Command{
		Use:   "cat-file (-t | -s | -p) <object>",
		Short: "Show an object's type, size or content",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			root, err := findRepo()
			if err != nil {
				return err
			}
			typ, content, err := objects.NewStore(filepath.Join(root, dataDir, "objects")).Read(args[0])
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			switch {
			case showType:
				fmt.Fprintln(out, typ)
			case showSize:
				fmt.Fprintln(out, len(content))
			case pretty:
				_, err = out.Write(content)
				return err
			default:
				return fmt.Errorf("choose one of -t, -s or -p")
			}
			return nil
		},
	}
	catFile.Flags().BoolVarP(&showType, "type", "t", false, "show the object type")
	catFile.Flags().BoolVarP(&showSize, "size", "s", false, "show the content size in bytes")
	catFile.Flags().BoolVarP(&pretty, "print", "p", false, "print the content")
	catFile.MarkFlagsMutuallyExclusive("type", "size", "print")

	rootCmd.AddCommand(hashObject, catFile)
}
