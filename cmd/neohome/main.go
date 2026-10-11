package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"neohome/internal/core"
	"neohome/internal/mcpserver"
	"neohome/internal/shell"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "mcp" {
		if len(os.Args) == 3 && os.Args[2] == "stdio" {
			endpoint := os.Getenv("NEOHOME_MCP_URL")
			if endpoint == "" {
				endpoint = "http://127.0.0.1:8765/mcp"
			}
			if err := mcpserver.RunStdio(os.Stdin, os.Stdout, endpoint, os.Getenv("NEOHOME_MCP_TOKEN")); err != nil {
				log.Fatal(err)
			}
			return
		}
		log.Fatal("usage: neohome mcp stdio")
	}

	path := worldPath()
	w := loadOrCreate(path)
	log.Printf("world: %d devices, %d players (%s)", len(w.Devices), len(w.Players), path)

	// background engine: advances sim, runs assistant, and really commits the
	// world to disk — the save is what makes any of this persist.
	go engine(w, path)
	if addr := os.Getenv("NEOHOME_MCP_ADDR"); addr != "" {
		if err := startMCP(w, addr); err != nil {
			log.Printf("MCP entry unavailable: %v", err)
		}
	}

	// Commit on shutdown. This must run in its own goroutine: the listeners below
	// block forever, so anything placed after them would never be reached and a
	// clean exit would silently lose the world.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		w.Lock()
		err := w.Save(path)
		w.Unlock()
		if err != nil {
			log.Println("final save failed:", err)
		} else {
			log.Println("world saved to", path)
		}
		os.Exit(0)
	}()

	// Each entry binds independently: a taken telnet port must not stop the SSH
	// door (or the reverse), and neither may take the world down.
	live := 0
	telnetLn, terr := net.Listen("tcp", ":2024")
	if terr != nil {
		log.Println("telnet entry unavailable:", terr)
	} else {
		log.Println("neohome telnet entry listening on :2024")
		go acceptLoop(telnetLn, func(c net.Conn) { handle(c, w) })
		live++
	}
	if err := sshEntry(w, ":2222"); err != nil {
		log.Println("ssh entry unavailable:", err)
	} else {
		live++
	}
	// the browser front: the same world, the same accounts, over HTTP+WebSocket
	if addr := webAddr(); addr != "" {
		if err := startWeb(w, addr); err != nil {
			log.Println("web terminal unavailable:", err)
		} else {
			live++
		}
	}
	// Every entry is down (or in use): keep the world running headless so the
	// engine still ticks and state still persists.
	if live == 0 {
		log.Println("no live entry; world continues headless")
	}

	select {}
}

// acceptLoop serves connections until the listener dies.
func acceptLoop(ln net.Listener, serve func(net.Conn)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("accept:", err)
			return
		}
		go serve(conn)
	}
}

func handle(conn net.Conn, w *core.World) {
	defer conn.Close()
	bufrd := bufio.NewReader(conn)
	fmt.Fprint(conn, "\r\n=== NeoHome terminal (telnet) ===\r\n\n")
	fmt.Fprint(conn, "login: ")
	login, _ := bufrd.ReadString('\n')
	login = strings.TrimSpace(login)
	fmt.Fprint(conn, "password: ")
	pass, _ := bufrd.ReadString('\n')
	pass = strings.TrimSpace(pass)

	p := w.Players[login]
	if p == nil || p.MCPOnly || p.Pass == "" || p.Pass != pass {
		fmt.Fprintln(conn, "auth failed")
		return
	}
	w.Lock()
	pc, u, note, err := w.LandPlayer(login)
	w.Unlock()
	if err != nil {
		fmt.Fprintf(conn, "%v\r\n", err)
		return
	}
	if note != "" {
		fmt.Fprintf(conn, "%s\r\n", note)
	}
	fmt.Fprintf(conn, "Welcome, %s. Type `help` or just start typing.\r\n", login)
	fmt.Fprintf(conn, "Your DNS is %s — try `dig mirror.neohome.example`.\r\n",
		must(pc.DNSAnswer("mirror.neohome.example")))

	s := shell.NewShell(w, pc, u, conn, conn.RemoteAddr().String(), "xterm-256color")
	s.RunLoop(bufrd)
}

func must(ip string, ok bool, _ string) string {
	if ok {
		return ip
	}
	return "(unresolved)"
}

// ---- tmux / screen persistence across reconnects ----

var (
	sessionsMu sync.Mutex
	sessions   = map[string]*shell.Shell{}
)
