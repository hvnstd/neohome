package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"neohome/internal/core"
	"neohome/internal/webtty"
)

// The browser front. telnet and ssh are doors into the world; this is the
// third one, and the one a player can open in a tab. It is a real HTTP server
// on a real port, and every tab is a real shell session on the player's own
// machine — visible in `who`, in the machine's syslog, and in the world's own
// auth-failure counter, exactly like an ssh login.
//
// NEOHOME_WEB_ADDR picks the address (default 0.0.0.0:8080); the values "off",
// "none" or "-" switch the door off. NEOHOME_WEB_ORIGIN optionally names one
// extra origin allowed on /login and /ws (useful behind a reverse proxy).

const defaultWebAddr = ":8080"

// webAddr resolves the listener address from the environment.
func webAddr() string {
	v, set := os.LookupEnv("NEOHOME_WEB_ADDR")
	if !set {
		return defaultWebAddr
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "off", "none", "-":
		return ""
	}
	return v
}

func startWeb(w *core.World, addr string) error {
	if addr == "" {
		return nil
	}
	console := webtty.NewConsole(w)
	console.Origin = strings.TrimSpace(os.Getenv("NEOHOME_WEB_ORIGIN"))
	console.Log = log.Default()
	if u, err := parseOriginFlag(console.Origin); err != nil {
		return err
	} else {
		_ = u
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", console)
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		// no WriteTimeout: a terminal session is as long as the player wants it
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 16 << 10,
	}
	log.Printf("NeoHome web terminal listening on %s (open it in a browser)", ln.Addr())
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("web terminal stopped: %v", err)
		}
	}()
	return nil
}

// parseOriginFlag validates NEOHOME_WEB_ORIGIN: one exact http(s) origin.
func parseOriginFlag(origin string) (string, error) {
	if origin == "" {
		return "", nil
	}
	if !strings.HasPrefix(origin, "http://") && !strings.HasPrefix(origin, "https://") {
		return "", errors.New("NEOHOME_WEB_ORIGIN must start with http:// or https://")
	}
	if strings.ContainsAny(origin, " \t") || strings.HasSuffix(origin, "/") {
		return "", errors.New("NEOHOME_WEB_ORIGIN must be an origin without a trailing slash")
	}
	return origin, nil
}
