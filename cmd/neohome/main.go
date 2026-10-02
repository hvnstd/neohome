package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"neohome/internal/core"
	"neohome/internal/shell"
)

func main() {
	w, err := core.LoadWorld("world.gob")
	if err != nil {
		w = core.NewWorld()
	}
	log.Printf("world: %d devices, %d players", len(w.Devices), len(w.Players))

	// background engine: advances sim, runs assistant, saves periodically.
	go engine(w)

	ln, err := net.Listen("tcp", ":2024")
	if err != nil {
		log.Fatal(err)
	}
	log.Println("neohome telnet entry listening on :2024")
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("accept:", err)
			continue
		}
		go handle(conn, w)
	}
}

func engine(w *core.World) {
	for {
		w.Lock()
		w.Tick()
		w.Unlock()
		time.Sleep(3 * time.Second)
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
	if p == nil || p.Pass != pass {
		fmt.Fprintln(conn, "auth failed")
		return
	}
	w.Lock()
	pc := w.Devices[p.PC]
	w.Unlock()
	if pc == nil {
		fmt.Fprintln(conn, "no device")
		return
	}
	u := pc.FindUser("alex")
	if u == nil {
		u = &core.User{Name: "alex", UID: 1000, Home: "/home/alex", Shell: "/bin/bash"}
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
