package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/Kirch-log/kirch/internal/db"
	"github.com/Kirch-log/kirch/internal/indexer"
)

// Server ties the pieces together: repositories on disk, kirch:// over
// TCP, and (with a database) the indexer and HTTP API.
type Server struct {
	Repos       Repos
	DatabaseURL string // empty = no database: push / pull only
	CORSOrigin  string
	Log         *log.Logger

	DB   *db.DB
	tcp  *TCPServer
	http *http.Server
}

func (s *Server) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// Start connects to the database (if any), migrates it and indexes every
// repository folder. Call before serving.
func (s *Server) Start(ctx context.Context) error {
	s.tcp = &TCPServer{Repos: s.Repos, Log: s.Log}
	if s.DatabaseURL == "" && s.DB == nil {
		return nil
	}
	if s.DB == nil {
		d, err := db.Open(ctx, s.DatabaseURL)
		if err != nil {
			return err
		}
		s.DB = d
	}
	if err := s.DB.Migrate(ctx); err != nil {
		return err
	}
	n, err := indexer.IndexAll(ctx, s.DB, s.Repos)
	if err != nil {
		s.logf("indexing at start: %v", err)
	}
	s.logf("indexed %d repositories", n)
	s.tcp.OnPush = s.index
	return nil
}

// index runs after every push. If it fails (database down), the next
// push or a restart catches up: folders are the source of truth.
func (s *Server) index(ctx context.Context, owner, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := indexer.IndexRepo(ctx, s.DB, s.Repos, owner, name)
	if err == nil {
		s.logf("indexed %s/%s: %d new commits", owner, name, st.NewCommits)
	}
	return err
}

// HasAPI reports whether the HTTP API is available (it needs the database).
func (s *Server) HasAPI() bool { return s.DB != nil }

// ServeTCP serves kirch:// on ln (already TLS) until ln closes.
func (s *Server) ServeTCP(ln net.Listener) error { return s.tcp.Serve(ln) }

// ServeHTTP serves the API on ln until Shutdown.
func (s *Server) ServeHTTP(ln net.Listener) error {
	s.http = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	err := s.http.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the HTTP API, letting requests in flight finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

// Close releases the database.
func (s *Server) Close() {
	if s.DB != nil {
		s.DB.Close()
	}
}
