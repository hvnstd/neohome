package shell

// bbs — the client for the community bulletin board. Reading a board is a
// network operation like any other: the name resolves, the bbsd service on
// the host really answers, and if the box is down the board is down. Posting
// writes real files on the BBS host — and the board's residents answer.

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["bbs"] = cmdBBS
}

// bbsHost is the community board everyone knows the address of — spec §33
// lists the BBS as one of the public entry points into the world.
const bbsHost = "bbs.neohome.example"

const bbsPort = 2323

// dialBBS does the name resolution and the real connection to the board.
// The connection itself is the gate: Dial refuses honestly when the name
// does not resolve, the route is gone, or the bbsd service is not running.
func dialBBS(s *Shell) int {
	ip, ok, how := core.DNSAnswer(s.Dev, bbsHost)
	if !ok {
		s.errf("bbs: resolve %s: %s", bbsHost, how)
		return 1
	}
	svc, _, msg := core.Dial(s.Dev, ip, bbsPort)
	if svc == nil {
		s.errf("bbs: connect to %s (%s): %s", bbsHost, ip, msg)
		return 1
	}
	return 0
}

func cmdBBS(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "boards":
		return bbsBoards(s)
	case "read":
		if len(args) < 2 {
			s.errf("usage: bbs read <board> [N]")
			return 1
		}
		return bbsRead(s, args[1], args[2:])
	case "post":
		if len(args) < 3 {
			s.errf("usage: bbs post <board> <subject>  (body on following lines until '.')")
			return 1
		}
		return bbsPost(s, args[1], strings.Join(args[2:], " "))
	case "mail":
		if len(args) < 3 {
			s.errf("usage: bbs mail <user> <subject>  (body on following lines until '.')")
			return 1
		}
		return bbsMail(s, args[1], strings.Join(args[2:], " "))
	case "inbox", "mailbox", "mail-list":
		return bbsInbox(s)
	case "readmail", "mail-read":
		if len(args) < 2 {
			s.errf("usage: bbs readmail N")
			return 1
		}
		return bbsReadmail(s, args[1])
	}
	s.errf("usage: bbs [boards|read <board> [N]|post <board> <subject>|mail <user> <subject>|inbox|readmail N]")
	return 1
}

func bbsBoards(s *Shell) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	fmt.Fprintf(s.Out, "%s — the community board\n", bbsHost)
	for _, b := range core.BBSBoards {
		fmt.Fprintf(s.Out, "  %-10s %d posts\n", b, len(s.W.BBSList(b)))
	}
	fmt.Fprintf(s.Out, "usage: bbs read <board> [N] | bbs post <board> <subject> | bbs mail <user> <subject> | bbs inbox | bbs readmail N\n")
	return 0
}

func bbsRead(s *Shell, board string, rest []string) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	posts := s.W.BBSList(board)
	if len(posts) == 0 {
		s.errf("bbs: no such board or empty board: %s", board)
		return 1
	}
	if len(rest) > 0 {
		n := 0
		if _, err := fmt.Sscanf(rest[0], "%d", &n); err != nil || n <= 0 {
			s.errf("bbs: bad message number %q", rest[0])
			return 1
		}
		for _, p := range posts {
			if p.Num == n {
				fmt.Fprintf(s.Out, "board %s / post %d\nFrom: %s\nDate: %s\nSubject: %s\n\n%s\n",
					p.Board, p.Num, p.From, p.Date, p.Subject, p.Body)
				return 0
			}
		}
		s.errf("bbs: no such post: %d", n)
		return 1
	}
	fmt.Fprintf(s.Out, "%s (%d posts)\n", board, len(posts))
	for _, p := range posts {
		fmt.Fprintf(s.Out, "%3d  %s  %-10s  %s\n", p.Num, p.Date, p.From, p.Subject)
	}
	return 0
}

func bbsPost(s *Shell, board, subject string) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	fmt.Fprintln(s.Out, "enter body, end with a single '.':")
	var lines []string
	for {
		l, err := s.bufrd.ReadString('\n')
		if err != nil {
			break
		}
		l = strings.TrimRight(l, "\r\n")
		if l == "." {
			break
		}
		lines = append(lines, l)
	}
	p, err := s.W.BBSPost(board, s.User.Name, subject, strings.Join(lines, "\n"), "")
	if err != nil {
		s.errf("bbs: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "posted %s/%d — the board reads it within minutes\n", p.Board, p.Num)
	return 0
}

// bbsMail sends a private letter: same body convention as posting, but the
// letter lands in the addressee's mailbox, not on a board.
func bbsMail(s *Shell, to, subject string) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	fmt.Fprintln(s.Out, "enter body, end with a single '.':")
	var lines []string
	for {
		l, err := s.bufrd.ReadString('\n')
		if err != nil {
			break
		}
		l = strings.TrimRight(l, "\r\n")
		if l == "." {
			break
		}
		lines = append(lines, l)
	}
	if err := s.W.BBSMail(s.User.Name, to, subject, strings.Join(lines, "\n"), ""); err != nil {
		s.errf("bbs: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "letter to %s posted\n", to)
	return 0
}

// bbsInbox lists the session user's own letters. The server filters by the
// session account — anyone else's mail is simply not listed.
func bbsInbox(s *Shell) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	msgs := s.W.BBSInbox(s.User.Name)
	if len(msgs) == 0 {
		fmt.Fprintf(s.Out, "no letters for %s\n", s.User.Name)
		return 0
	}
	fmt.Fprintf(s.Out, "mailbox %s (%d letters)\n", s.User.Name, len(msgs))
	for _, p := range msgs {
		fmt.Fprintf(s.Out, "%3d  %s  %-10s  %s\n", p.Num, p.Date, p.From, p.Subject)
	}
	return 0
}

func bbsReadmail(s *Shell, arg string) int {
	if rc := dialBBS(s); rc != 0 {
		return rc
	}
	n := 0
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || n <= 0 {
		s.errf("bbs: bad letter number %q", arg)
		return 1
	}
	p, ok := s.W.BBSMailRead(s.User.Name, n)
	if !ok {
		s.errf("bbs: no such letter: %d", n)
		return 1
	}
	fmt.Fprintf(s.Out, "letter %d\nFrom: %s\nDate: %s\nSubject: %s\n\n%s\n",
		p.Num, p.From, p.Date, p.Subject, p.Body)
	return 0
}
