package core

// BBS (WS-0.7): a community bulletin board as real world state. Spec §19
// demands IRC / BBS / Chat "不只是装饰" — boards carry tasks, trades and
// intelligence that is actually true of the world, and §27 lists the BBS as
// a recon source. So the truth lives in files on the BBS host's filesystem
// (/srv/bbs/<board>/<NNNN>.txt), reachable over the network only while the
// bbsd service really runs, and the NPC posters are the same "people on the
// network" the IRC channel has: they react to what is true right now, and
// they answer a player's post within a few ticks.
//
// The shape of BBS is owned by this file; World only carries the pointer.

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// BBSBoards are the boards the world ships. Every board must have seeded
// content and every post must be true of the world (see seedBBS).
var BBSBoards = []string{"general", "market", "intel", "hacker"}

// BBS holds the subsystem's identity and the counters posts need. The posts
// themselves are files on the BBS host; nothing about a post is stored here.
type BBS struct {
	DeviceID string
	// next post number per board (files are /srv/bbs/<board>/<NNNN>.txt)
	NextPost map[string]int
	// one scheduled NPC reply to a player's post, deterministic: it fires on
	// the first tick at or after ReplyDue. Zero means nothing is pending.
	ReplyDue     int
	ReplyFrom    string
	ReplyBoard   string
	ReplySubject string
	ReplyText    string
	// lastAmbient rotates the periodic NPC chatter so it does not repeat.
	lastAmbient int
}

// BBSPost is one rendered post.
type BBSPost struct {
	Num     int
	Board   string
	From    string
	Date    string
	Subject string
	Body    string
}

const bbsRoot = "/srv/bbs"

func bbsBoardDir(board string) string { return bbsRoot + "/" + board }

func bbsPostPath(board string, num int) string {
	return fmt.Sprintf("%s/%03d.txt", bbsBoardDir(board), num)
}

// BBSDevice returns the BBS host, or nil — an old saved world has no bbs
// device, and the client must be told that honestly.
func (w *World) BBSDevice() *Device {
	if w.BBS == nil {
		return nil
	}
	return w.Devices[w.BBS.DeviceID]
}

// seedBBS creates the board directories and the opening threads. Called from
// world_init; the device and its service are created by the device seeding.
func seedBBS(w *World) {
	d := w.Devices["bbs"]
	if d == nil {
		return
	}
	w.BBS = &BBS{DeviceID: d.ID, NextPost: map[string]int{}}
	for _, b := range BBSBoards {
		d.FS.MkdirAll(bbsBoardDir(b), 0755, "bbs", "bbs")
		w.BBS.NextPost[b] = 1
	}
	stamp := w.Sim.Add(-90 * time.Minute).Format("2006-01-02 15:04")
	threads := []struct{ board, from, subject, body string }{
		{"general", "sysmods", "welcome to NeoBBS",
			"boards: general, market, intel, hacker.\nposts are files on this box; be civil, search before asking,\nand put things in the right board or mira-9 will move them."},
		{"general", "daemon42", "who is still around here?",
			"quiet week. the usual crowd is on #local if this board is too slow for you."},
		{"market", "mira-9", "WTS: mini PC, 8GB, quiet, runs NeoOS fine",
			"price in coins, bank transfer only.\nmail me here or on the network, first come first served."},
		{"market", "daemon42", "cron/backup setup for coins",
			"you have a machine that should be doing something every night and is not?\ni wire the schedule, you pay to my wallet. bank transfer, no credit."},
		{"intel", "mira-9", "ISP maintenance window tonight",
			"transit work after 02:00 again. if a site feels slow, it is not you.\nprobably."},
		{"intel", "sysmods", "power blips: check your router afterwards",
			"after an outage, dnsmasq config does not always survive intact.\nif everything SERVFAILs, look at resolv-file in /etc/dnsmasq.conf first."},
		{"hacker", "mara-bot", "old consumer routers still ship telnet on the LAN",
			"port 23, no banner, admin/admin. check your own gateway —\nyours probably does too. do not ask how I know."},
		{"hacker", "daemon42", "scans are logged on the target. every time.",
			"every port you touch leaves a line in their syslog.\nact accordingly."},
	}
	for _, th := range threads {
		w.BBSPost(th.board, th.from, th.subject, th.body, stamp)
	}
}

// BBSPost writes one post as a real file on the BBS host and leaves the same
// evidence every other subsystem leaves: a device log line and a world event.
// A stamp of "" means now. An unknown board is refused — a post to a board
// that does not exist must not silently vanish.
func (w *World) BBSPost(board, from, subject, body, stamp string) (*BBSPost, error) {
	b := w.BBS
	if b == nil || w.BBSDevice() == nil {
		return nil, fmt.Errorf("bbs: no board host in this world")
	}
	if !validBoard(board) {
		return nil, fmt.Errorf("bbs: no such board %q", board)
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(subject) == "" {
		return nil, fmt.Errorf("bbs: post needs a From and a Subject")
	}
	if stamp == "" {
		stamp = w.Sim.Format("2006-01-02 15:04")
	}
	num := b.NextPost[board]
	b.NextPost[board] = num + 1
	p := &BBSPost{Num: num, Board: board, From: from, Date: stamp, Subject: subject, Body: body}
	content := fmt.Sprintf("From: %s\nDate: %s\nSubject: %s\n\n%s\n", from, stamp, subject, strings.TrimRight(body, "\n"))
	d := w.BBSDevice()
	d.FS.Write(bbsPostPath(board, num), content, 0644, "bbs", "bbs")
	d.Logf("info", "bbsd", "post %d in %s from %s: %s", num, board, from, subject)
	w.AddEvent(d.ID, "info", "bbs", "post %d in %s from %s", num, board, from)
	// the board is a social system: a player's post gets an NPC answer
	w.scheduleNPCReply(board, from, subject, body)
	return p, nil
}

// BBSList returns a board's posts, oldest first, parsed from the files — a
// deleted file is a deleted post, nothing else pretends otherwise.
func (w *World) BBSList(board string) []BBSPost {
	var out []BBSPost
	d := w.BBSDevice()
	if d == nil || !validBoard(board) {
		return out
	}
	names := d.FS.List(bbsBoardDir(board))
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		data, ok := d.FS.Read(name)
		if !ok {
			continue
		}
		out = append(out, parseBBSPost(board, name, string(data)))
	}
	return out
}

// parseBBSPost rehydrates one post file. The number comes from the filename,
// the header fields from the file's own lines; the body is everything after
// the first blank line.
func parseBBSPost(board, file, content string) BBSPost {
	p := BBSPost{Board: board}
	fmt.Sscanf(path.Base(file), "%d", &p.Num)
	lines := strings.Split(content, "\n")
	blank := -1
	for i, line := range lines {
		if line == "" {
			blank = i
			break
		}
		switch {
		case strings.HasPrefix(line, "From: ") && p.From == "":
			p.From = strings.TrimPrefix(line, "From: ")
		case strings.HasPrefix(line, "Date: ") && p.Date == "":
			p.Date = strings.TrimPrefix(line, "Date: ")
		case strings.HasPrefix(line, "Subject: ") && p.Subject == "":
			p.Subject = strings.TrimPrefix(line, "Subject: ")
		}
	}
	if blank >= 0 {
		p.Body = strings.TrimRight(strings.Join(lines[blank+1:], "\n"), "\n")
	}
	return p
}

func validBoard(b string) bool {
	for _, name := range BBSBoards {
		if name == b {
			return true
		}
	}
	return false
}

// BBSTick runs once per World.Tick: it fires a scheduled NPC reply to a
// player's post and, every 80 ticks (40 sim minutes), drops one ambient post
// that reflects what is actually true right now.
func (w *World) BBSTick() {
	b := w.BBS
	if b == nil {
		return
	}
	if b.ReplyDue > 0 && w.TickCount >= b.ReplyDue {
		if _, err := w.BBSPost(b.ReplyBoard, b.ReplyFrom, b.ReplySubject, b.ReplyText, ""); err == nil {
			b.ReplyDue = 0
		}
		return
	}
	if w.TickCount%80 == 40 {
		b.lastAmbient++
		switch b.lastAmbient % 3 {
		case 1:
			if w.FaultDNSActive() {
				w.BBSPost("intel", "mira-9", "is anyone else's DNS dead?",
					"every name SERVFAILs since the power blip this morning.\nIPs still ping, so it is the resolver, not the line.\nmy cron job that curls the mirror agrees.", "")
			} else {
				w.BBSPost("general", "mira-9", "evening",
					"board has been quiet. say hi if you are new.", "")
			}
		case 2:
			w.BBSPost("market", "daemon42", "still trading",
				"coins, bank transfer, no credit. see the earlier posts.", "")
		case 0:
			w.BBSPost("hacker", "mara-bot", "weekly reminder",
				"your gateway's logs remember more than you do. so does mine.", "")
		}
	}
}

// scheduleNPCReply is called when a player writes a post: an NPC answers
// within a few ticks, keyword-driven and state-aware, exactly like the IRC
// channel's NPCRespond — cheap, deterministic, grounded in world truth.
func (w *World) scheduleNPCReply(board, from, subject, text string) {
	b := w.BBS
	if b == nil {
		return
	}
	if _, isPlayer := w.Players[from]; !isPlayer {
		return // NPCs do not reply to themselves
	}
	if b.ReplyDue > 0 {
		return // one pending reply is enough; nobody types that fast
	}
	low := lower(subject + " " + text)
	b.ReplyDue = w.TickCount + 3
	b.ReplyFrom = "mira-9"
	b.ReplyBoard = board
	b.ReplySubject = "re: " + subject
	switch {
	case containsAny(low, "dns", "resolv", "nameserver"):
		b.ReplyFrom = "sysmods"
		if w.FaultDNSActive() {
			b.ReplyText = "known right now: the gateway's dnsmasq lost its upstream. resolv-file in /etc/dnsmasq.conf points at a file that is not there."
		} else {
			b.ReplyText = "resolver chain looks healthy from here. check your own /etc/resolv.conf first."
		}
	case containsAny(low, "job", "work", "hire", "money", "coins"):
		b.ReplyFrom = "daemon42"
		b.ReplyText = "the real job board is at jobs.hiring.example — the neighbour job pays the same day if you actually fix it."
	case containsAny(low, "buy", "sell", "trade", "price"):
		b.ReplyText = "market board is for offers, not chatter. state a price or the thread dies."
	case containsAny(low, "hack", "exploit", "scan"):
		b.ReplyFrom = "mara-bot"
		b.ReplyText = "careful. scans are logged on the target, and heat is not a metaphor."
	default:
		b.ReplyText = "welcome. put things in the right board and someone will answer."
	}
}
