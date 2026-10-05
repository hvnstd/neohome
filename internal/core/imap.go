package core

// IMAP (WS-0.6): reading a mailbox that lives on another machine, over the
// network, with real authentication. SMTP (mail.go) delivers; this file is
// the reading half of the mail subsystem and owns the IMAP service's
// semantics: what a mailbox looks like on the wire and who may open it.
//
// Like the rest of the mail subsystem there is no hidden state: the mailbox
// is the mbox file in the VFS, parsed on demand, and access is decided by
// the account records and file modes that already exist.

import (
	"fmt"
	"strings"
)

// IMAPMessage is one message as an IMAP session sees it.
type IMAPMessage struct {
	Seq     int    // 1-based, oldest first
	From    string // envelope sender as written in the mbox "From " line
	Subject string
	Date    string
	Body    string
}

// ParseMbox splits an mbox into its messages. Records start at a line
// beginning with "From " (the mboxo convention this world writes; body lines
// that really start with "From " are escaped as ">From " on delivery and
// unescaped here). A mailbox that does not parse cleanly still yields the
// records found in it — a truncated tail must not hide delivered mail.
func ParseMbox(data []byte) []IMAPMessage {
	var out []IMAPMessage
	var cur *IMAPMessage
	var body []string
	flush := func() {
		if cur != nil {
			cur.Body = unescapeMboxo(strings.Join(body, "\n"))
			out = append(out, *cur)
		}
		body = nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "From ") {
			flush()
			fields := strings.Fields(line)
			from := ""
			when := ""
			if len(fields) >= 2 {
				from = fields[1]
			}
			if len(fields) >= 3 {
				when = strings.Join(fields[2:], " ")
			}
			cur = &IMAPMessage{Seq: len(out) + 1, From: from, Date: when}
			continue
		}
		if cur == nil {
			continue // bytes before the first record are not a message
		}
		if strings.HasPrefix(line, "Subject: ") && cur.Subject == "" {
			cur.Subject = strings.TrimPrefix(line, "Subject: ")
			continue
		}
		if strings.HasPrefix(line, "Date: ") && cur.Date == "" {
			cur.Date = strings.TrimPrefix(line, "Date: ")
			continue
		}
		body = append(body, line)
	}
	flush()
	return out
}

func escapeMboxo(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "From ") {
			lines[i] = ">" + l
		}
	}
	return strings.Join(lines, "\n")
}

func unescapeMboxo(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ">From ") {
			lines[i] = strings.TrimPrefix(l, ">")
		}
	}
	return strings.Join(lines, "\n")
}

// IMAPSelect is the server side of "open this mailbox" — the whole
// authentication model of the world's imapd: the account must exist on the
// mail host and the password must match it, the same simulated check ssh and
// sudo perform. The failure never says which of the two was wrong, the way
// a real server does not. Reading goes through the VFS as the account, so a
// box that account cannot read really fails to open.
func (w *World) IMAPSelect(d *Device, user, pass string) ([]IMAPMessage, error) {
	if d == nil {
		return nil, fmt.Errorf("no such mail host")
	}
	u := d.FindUser(user)
	if u == nil || u.Pass == "" || pass != u.Pass {
		return nil, fmt.Errorf("login failed")
	}
	data, exists, permitted := ReadInbox(d, user, u)
	if exists && !permitted {
		return nil, fmt.Errorf("mailbox is not readable")
	}
	return ParseMbox(data), nil
}
