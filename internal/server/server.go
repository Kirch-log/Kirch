package server

import (
	"context"
	"log"
	"net"
	"net/http"
)

// Server ties the pieces together: repositories on disk, kirch:// over
// TCP, and (with a database) the indexer and HTTP API.
type Server struct {
	Repos       Repos
	DatabaseURL string // empty = no database: push / pull only
	CORSOrigin  string
	Log         *log.Logger

	tcp  *TCPServer
	http *http.Server
}

// Start connects to the database (if any). Call before serving.
func (s *Server) Start(ctx context.Context) error {
	s.tcp = &TCPServer{Repos: s.Repos, Log: s.Log}
	return nil
}

// HasAPI reports whether the HTTP API is available (it needs the database).
func (s *Server) HasAPI() bool { return false }

// ServeTCP serves kirch:// on ln (already TLS) until ln closes.
func (s *Server) ServeTCP(ln net.Listener) error { return s.tcp.Serve(ln) }

// ServeHTTP serves the API on ln until Shutdown.
func (s *Server) ServeHTTP(ln net.Listener) error { return http.ErrServerClosed }

// Shutdown stops the HTTP API, letting requests in flight finish.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http != nil {
		return s.http.Shutdown(ctx)
	}
	return nil
}

// Close releases the database.
func (s *Server) Close() {}
