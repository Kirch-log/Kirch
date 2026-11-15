package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Kirch-log/kirch/internal/kicad"
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var outDir string
	importCmd := &cobra.Command{
		Use:   "import",
		Short: "Turn a design from another tool into Kirch's circuit files",
	}
	kicadCmd := &cobra.Command{
		Use:   "kicad <design.kicad_sch | design.kicad_pro | netlist.net>",
		Short: "Import a KiCad design: writes netlist.json and parts.json",
		Long: `Reads a KiCad design and writes Kirch's circuit files:

  netlist.json  which pins are connected (pins by number: U1.9)
  parts.json    every part: type, value, symbol, footprint, pin names (9 = GPIO9)

A .kicad_sch or .kicad_pro is exported with KiCad's own kicad-cli (KiCad 7+).
Without KiCad installed, export the netlist in KiCad (File → Export →
Netlist → KiCad) and import the .net file.

Run it again after changing the design, then kirch diff shows the wiring changes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			out := c.OutOrStdout()
			// Inside a repository: write at its top and remember the design,
			// so kirch add keeps it up to date from now on.
			if r, err := currentRepo(); err == nil && outDir == "" {
				res, err := r.ImportKiCad(args[0])
				if err != nil {
					return err
				}
				printKiCad(out, res, filepath.Base(args[0]))
				if res.Schematic != "" {
					fmt.Fprintf(out, "kirch add will re-import %s whenever it changes\n", res.Schematic)
				}
				return nil
			}
			n, err := kicad.Load(args[0])
			if err != nil {
				return err
			}
			netlist, parts, err := n.Files()
			if err != nil {
				return err
			}
			dir := outDir
			if dir == "" {
				dir = "."
			}
			for name, data := range map[string][]byte{"netlist.json": netlist, "parts.json": parts} {
				path := filepath.Join(dir, name)
				old, _ := os.ReadFile(path)
				if bytes.Equal(old, data) {
					fmt.Fprintf(out, "unchanged %s\n", name)
					continue
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote     %s\n", name)
			}
			connected := 0
			for _, net := range n.Nets {
				if len(net.Pins) > 1 {
					connected++
				}
			}
			fmt.Fprintf(out, "%d parts, %d nets from %s\n", len(n.Parts), connected, filepath.Base(args[0]))
			return nil
		},
	}
	kicadCmd.Flags().StringVar(&outDir, "out", "", "folder to write the files to (default: top of the repository)")
	importCmd.AddCommand(kicadCmd)
	rootCmd.AddCommand(importCmd)
}

func printKiCad(out io.Writer, res *repo.KiCadResult, from string) {
	written := map[string]bool{}
	for _, f := range res.Written {
		written[f] = true
	}
	for _, f := range []string{"netlist.json", "parts.json"} {
		if written[f] {
			fmt.Fprintf(out, "wrote     %s\n", f)
		} else {
			fmt.Fprintf(out, "unchanged %s\n", f)
		}
	}
	fmt.Fprintf(out, "%d parts, %d nets from %s\n", res.Parts, res.Nets, from)
}
