// Command kirch is version control for hardware circuits.
package main

import (
	"os"

	"github.com/Kirch-log/kirch/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
