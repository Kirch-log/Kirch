package cmd

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kirch-log/kirch/internal/server"
	"github.com/spf13/cobra"
)

func init() {
	var addr, root string
	serveCmd := &cobra.Command{
		Use:   "serve [--addr :8080] [--root folder]",
		Short: "Run a small Kirch server so others can clone, pull and push over the network",
		Long: `Serves every repository under --root at http://<this computer>:<port>/<owner>/<repo>.
A push to a repository that doesn't exist yet creates it.
There are no logins: use it on your own computer or a network you trust.
(The Kirch platform runs the same push / pull code with logins and tokens.)

  kirch serve --root ~/kirch-server
  kirch remote add origin http://localhost:8080/ana/servo-board
  kirch push`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			abs, err := filepath.Abs(root)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return err
			}
			out := c.OutOrStdout()
			s := &server.Server{Root: abs, AutoCreate: true, Log: log.New(out, "", log.LstdFlags)}
			host := addr
			if strings.HasPrefix(addr, ":") {
				host = "localhost" + addr
			}
			fmt.Fprintf(out, "Kirch server for %s on http://%s/<owner>/<repo>\n", abs, host)
			for _, name := range s.Repos() {
				fmt.Fprintf(out, "  http://%s/%s\n", host, name)
			}
			srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
			return srv.ListenAndServe()
		},
	}
	serveCmd.Flags().StringVar(&addr, "addr", ":8080", "address to listen on")
	serveCmd.Flags().StringVar(&root, "root", "kirch-server", "folder that holds the repositories (<owner>/<repo>)")
	rootCmd.AddCommand(serveCmd)
}
