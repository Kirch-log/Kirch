// kirch-server is the Kirch platform server: kirch:// push / pull over
// TCP + TLS (port 9418) and the HTTP API the website reads (port 8080).
//
// Settings come from environment variables:
//
//	KIRCH_DATA_DIR      where repositories live (default ./data)
//	KIRCH_TCP_ADDR      kirch:// listen address (default :9418)
//	KIRCH_HTTP_ADDR     HTTP API listen address (default :8080)
//	KIRCH_DATABASE_URL  Postgres connection string (empty = no database, no HTTP API)
//	KIRCH_TLS_CERT      TLS certificate file \ both empty = self-signed cert
//	KIRCH_TLS_KEY       TLS key file         / kept in <data>/tls/
//	KIRCH_TLS_HOSTS     extra host names for the self-signed cert, comma-separated
//	KIRCH_CORS_ORIGIN   allowed website origin for the API (default *)
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Kirch-log/kirch/internal/server"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	logger := log.New(os.Stderr, "kirch-server ", log.LstdFlags)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Fatal(err)
	}
}

func run(ctx context.Context, logger *log.Logger) error {
	dataDir := env("KIRCH_DATA_DIR", "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	var hosts []string
	for _, h := range strings.Split(os.Getenv("KIRCH_TLS_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	cert, certPath, err := server.LoadOrCreateCert(dataDir, os.Getenv("KIRCH_TLS_CERT"), os.Getenv("KIRCH_TLS_KEY"), hosts)
	if err != nil {
		return err
	}

	srv := &server.Server{
		Repos:       server.Repos{DataDir: dataDir},
		DatabaseURL: os.Getenv("KIRCH_DATABASE_URL"),
		CORSOrigin:  env("KIRCH_CORS_ORIGIN", "*"),
		Log:         logger,
	}
	if err := srv.Start(ctx); err != nil {
		return err
	}
	defer srv.Close()

	tcpLn, err := tls.Listen("tcp", env("KIRCH_TCP_ADDR", ":9418"), &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	logger.Printf("kirch:// listening on %s (TLS cert %s)", tcpLn.Addr(), certPath)

	var httpLn net.Listener
	if srv.HasAPI() {
		if httpLn, err = net.Listen("tcp", env("KIRCH_HTTP_ADDR", ":8080")); err != nil {
			tcpLn.Close()
			return err
		}
		logger.Printf("HTTP API listening on %s", httpLn.Addr())
	} else {
		logger.Printf("no KIRCH_DATABASE_URL: HTTP API and indexing are off (push / pull still work)")
	}

	errc := make(chan error, 2)
	go func() { errc <- srv.ServeTCP(tcpLn) }()
	if httpLn != nil {
		go func() { errc <- srv.ServeHTTP(httpLn) }()
	}

	select {
	case <-ctx.Done():
		logger.Printf("shutting down")
	case err = <-errc:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tcpLn.Close()
	srv.Shutdown(shutdownCtx)
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}
