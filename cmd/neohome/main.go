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
	"neohome/internal/shell"
)

func main() {
	path := worldPath()
	w := loadOrCreate(path)
	log.Printf("world: %d devices, %d players (%s)", len(w.Devices), len(w.Players), path)

	// background engine: advances sim, runs assistant, and really commits the
	// world to disk — the save is what makes any of this persist.
	go engine(w, path)

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
	telnetLn, terr := net.Listen("tcp", ":2024")
	if terr != nil {
		log.Println("telnet entry unavailable:", terr)
	} else {
		log.Println("neohome telnet entry listening on :2024")
		go acceptLoop(telnetLn, func(c net.Conn) { handle(c, w) })
	}
	if err := sshEntry(w, ":2222"); err != nil {
		log.Println("ssh entry unavailable:", err)
	}
	// Both entries are down (or in use): keep the world running headless so the
	// engine still ticks and state still persists.
	log.Println("no live entry; world continues headless")

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
