package main

// live_drive is an end-to-end interaction harness: it speaks the real telnet
// protocol to a running neohome server, logs in as alex, and walks the full
// fault chain the way a player would — then prints exactly what the world did.

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"time"
)

func main() {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:2024", 10*time.Second)
	if err != nil {
		fmt.Println("CONNECT FAILED:", err)
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)

	readFor := func(d time.Duration) string {
		var b strings.Builder
		conn.SetReadDeadline(time.Now().Add(d))
		for {
			ch, err := r.ReadByte()
			if err != nil {
				break
			}
			b.WriteByte(ch)
		}
		return b.String()
	}
	send := func(s string) { conn.Write([]byte(s + "\r\n")) }

	fmt.Println("========== neohome live session ==========")
	fmt.Print(readFor(1200 * time.Millisecond))
	send("alex")
	time.Sleep(300 * time.Millisecond)
	send("alex123")
	fmt.Print(readFor(1500 * time.Millisecond))

	fails := 0
	step := func(cmd string, wait time.Duration) string {
		time.Sleep(320 * time.Millisecond)
		send(cmd)
		out := readFor(wait)
		// a bare "$ " echo with no output means the command produced nothing
		fmt.Printf("\n$ %s\n%s", cmd, indent(out))
		if strings.Contains(out, "command not found") {
			fails++
		}
		return out
	}

	// ---- 1. observe: the symptom is real and split (DNS broken, IP fine) ----
	step("hostname", 500*time.Millisecond)
	step("fastfetch", 600*time.Millisecond)
	step("ip a", 500*time.Millisecond)
	step("dig mirror.neohome.example", 600*time.Millisecond)
	step("cat /etc/resolv.conf", 500*time.Millisecond)
	step("jobs", 600*time.Millisecond)
	step("job show J-101", 600*time.Millisecond)
	step("job accept J-101", 500*time.Millisecond)

	// ---- 2. diagnose and repair the ROUTER (the actual fault) ----
	step("ssh root@10.77.1.1", 700*time.Millisecond)
	send("admin")
	fmt.Printf("%s\n", indent(readFor(900*time.Millisecond)))
	step("cat /etc/dnsmasq.conf | tail -6", 600*time.Millisecond)
	step("logread | grep dnsmasq | tail -3", 600*time.Millisecond)
	step("echo nameserver 10.0.0.3 > /etc/dnsmasq.upstream", 450*time.Millisecond)
	step("sed -i s|/var/run/dnsmasq/resolv.conf|/etc/dnsmasq.upstream| /etc/dnsmasq.conf", 450*time.Millisecond)
	step("cat /etc/dnsmasq.conf | tail -6", 600*time.Millisecond)
	step("/etc/init.d/dnsmasq restart", 700*time.Millisecond)
	step("exit", 600*time.Millisecond)

	// ---- 3. verify: the world genuinely changed ----
	step("dig mirror.neohome.example", 700*time.Millisecond)
	step("curl -s http://mirror.neohome.example/ | head -3", 700*time.Millisecond)

	// ---- 4. paid against verified world state ----
	step("job pay J-101", 800*time.Millisecond)
	step("balance", 600*time.Millisecond)

	// ---- 5. the assistant is a real agent with its own node ----
	step("assist status", 700*time.Millisecond)
	step("job delegate J-102", 700*time.Millisecond)
	step("assist tasks", 700*time.Millisecond)
	step("ss -tlnp", 600*time.Millisecond)
	step("exit", 600*time.Millisecond)

	fmt.Println("\n========== session end ==========")
	fmt.Printf("commands with 'command not found': %d\n", fails)
}

func indent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("    | " + l + "\n")
	}
	return b.String()
}
