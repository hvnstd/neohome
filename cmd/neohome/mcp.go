package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"neohome/internal/core"
	"neohome/internal/mcpserver"
)

func startMCP(w *core.World, addr string) error {
	token := os.Getenv("NEOHOME_MCP_TOKEN")
	if len(token) < 32 {
		return errors.New("NEOHOME_MCP_TOKEN must contain at least 32 characters")
	}
	allowedOrigin := os.Getenv("NEOHOME_MCP_ORIGIN")
	if allowedOrigin != "" {
		u, err := url.Parse(allowedOrigin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.User != nil {
			return errors.New("NEOHOME_MCP_ORIGIN must be an exact http(s) origin without a path")
		}
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid NEOHOME_MCP_ADDR: %w", err)
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); (ip == nil || !ip.IsLoopback()) && !strings.EqualFold(host, "localhost") {
		log.Printf("MCP listener is exposed on %s; protect it with a firewall and TLS-terminating reverse proxy", addr)
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	server, err := mcpserver.New(w, token, allowedOrigin)
	if err != nil {
		_ = listener.Close()
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.Handler())
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Printf("NeoHome MCP Streamable HTTP listening on %s/mcp", listener.Addr())
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("MCP HTTP server stopped: %v", err)
		}
	}()
	return nil
}
