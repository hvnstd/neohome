package shell

// mutt — the mail user agent. `mail` reads the mailbox of the account you
// are logged in as; mutt opens *any* mailbox you can authenticate to, which
// is what the imapd service exists for: a mailbox on another machine over
// the network, through the same gates (DNS, route, power, service state)
// every other connection obeys.
//
//   mutt                                        the session account's inbox
//   mutt -f /var/mail/alex [N]                  a local mbox, file rules apply
//   mutt -f imap://user[:pass]@host[:port]/INBOX [N]   a mailbox over IMAP
//
// N opens one message; without it mutt prints the index, the way opening a
// mailbox in the real client does.

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["mutt"] = cmdMutt
}

func cmdMutt(s *Shell, args []string) int {
	mailbox := ""
	msgno := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f":
			if i+1 >= len(args) {
				s.errf("mutt: -f needs a mailbox")
				return 1
			}
			i++
			mailbox = args[i]
		default:
			n, err := strconv.Atoi(args[i])
			if err != nil {
				s.errf("mutt: unknown argument %q", args[i])
				return 1
			}
			msgno = n
		}
	}
	if mailbox == "" {
		mailbox = core.MailboxPath(s.User.Name)
	}
	if strings.HasPrefix(mailbox, "imap://") {
		return muttIMAP(s, mailbox, msgno)
	}
	return muttMbox(s, mailbox, msgno)
}

// muttIMAP opens a remote mailbox: parse the URL, dial the imapd through the
// real network stack, then authenticate against the target host's account
// records. Both ends log the session, so reads and failed logins are
// evidence, exactly like an ssh session.
func muttIMAP(s *Shell, mailbox string, msgno int) int {
	rest := strings.TrimPrefix(mailbox, "imap://")
	folder := "INBOX"
	if i := strings.Index(rest, "/"); i >= 0 {
		folder = rest[i+1:]
		rest = rest[:i]
	}
	user, pass := s.User.Name, ""
	hostport := rest
	if i := strings.Index(rest, "@"); i >= 0 {
		hostport = rest[i+1:]
		user = rest[:i]
		if j := strings.Index(user, ":"); j >= 0 {
			pass = user[j+1:]
			user = user[:j]
		}
	}
	port := 143
	host := hostport
	if i := strings.Index(hostport, ":"); i >= 0 {
		host = hostport[:i]
		n, err := strconv.Atoi(hostport[i+1:])
		if err != nil || n <= 0 {
			s.errf("mutt: bad port in %q", mailbox)
			return 1
		}
		port = n
	}
	if host == "" || user == "" {
		s.errf("usage: mutt -f imap://user[:pass]@host[:port]/INBOX [N]")
		return 1
	}
	if !strings.EqualFold(folder, "INBOX") {
		// one mailbox per account in this world; saying otherwise would lie
		s.errf("mutt: no such mailbox %q on %s (only INBOX exists)", folder, host)
		return 1
	}
	if pass == "" {
		fmt.Fprintf(s.Out, "%s@%s's password: ", user, host)
		pass = s.ReadPasswordLine("")
	}
	ip, ok, how := core.DNSAnswer(s.Dev, host)
	if !ok {
		s.errf("mutt: resolve %s: %s", host, how)
		return 1
	}
	svc, dst, msg := core.Dial(s.Dev, ip, port)
	if svc == nil {
		s.errf("mutt: connect to %s (%s): %s", host, ip, msg)
		return 1
	}
	msgs, err := s.W.IMAPSelect(dst, user, pass)
	if err != nil {
		dst.Logf("notice", "imapd", "login failed for %s from %s", user, s.Dev.SourceIPFor(dst))
		s.errf("mutt: %s", err)
		return 1
	}
	dst.Logf("info", "imapd", "%s opened INBOX from %s (%d messages)", user, s.Dev.SourceIPFor(dst), len(msgs))
	return muttShow(s, msgs, msgno)
}

// muttMbox opens a local mbox file; plain file permissions decide, the same
// as cat would.
func muttMbox(s *Shell, mailbox string, msgno int) int {
	p := s.abs(mailbox)
	vfs, p2, rerr := s.ResolveVFS(p)
	if vfs == nil {
		s.errf("mutt: %s: %s", mailbox, rerr)
		return 1
	}
	data, exists, allowed := vfs.ReadPathAs(p2, s.User)
	if !exists {
		s.errf("mutt: %s: No such file or directory", mailbox)
		return 1
	}
	if !allowed {
		s.errf("mutt: %s: Permission denied", mailbox)
		return 1
	}
	return muttShow(s, core.ParseMbox(data), msgno)
}

func muttShow(s *Shell, msgs []core.IMAPMessage, msgno int) int {
	if msgno > 0 {
		for _, m := range msgs {
			if m.Seq == msgno {
				fmt.Fprintf(s.Out, "From: %s\nDate: %s\nSubject: %s\n\n%s\n",
					m.From, m.Date, m.Subject, strings.TrimRight(m.Body, "\n"))
				return 0
			}
		}
		s.errf("mutt: no such message: %d", msgno)
		return 1
	}
	if len(msgs) == 0 {
		fmt.Fprintln(s.Out, "INBOX is empty")
		return 0
	}
	fmt.Fprintf(s.Out, "%d messages in INBOX:\n", len(msgs))
	for _, m := range msgs {
		fmt.Fprintf(s.Out, "%3d  %s  %-24s  %s\n", m.Seq, m.Date, m.From, m.Subject)
	}
	return 0
}
