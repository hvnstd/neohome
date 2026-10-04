package core

// Virtual mail subsystem (WS-0). This closes the Phase-0 deliverable
// "1 个 Mail 系统" (PROJECT.md §47) honestly: mail is real world state with a
// real reachable port, not a text file written by a helper.
//
// Design constraints (PROJECT.md §51, AGENTS.md "Hard constraint"):
//   - nothing here executes host code or touches the host network;
//   - every message read/written goes through the VFS, so permissions,
//     ownership and `ls`/`find` behave like any other file;
//   - an mbox is a *spool directory plus a per-user file*, exactly the shape a
//     real box has: /var/spool/mail/<user> (Maildir-ish spool) is what the
//     daemon owns, /var/mail/<user> is the user-readable inbox.
//
// Only `World.Mail` is persisted (a pointer on World); everything else is
// derived from VFS contents, so a save/load round trip cannot desync spool
// state from the filesystem.

import (
	"fmt"
	"sort"
	"strings"
)

// MailBox is the persisted handle for the subsystem. Its fields are all
// gob-encodable; the per-message truth lives in the VFS spool files.
type MailBox struct {
	// Domain is the mail domain this world uses for local delivery.
	Domain string
	// Queue holds messages accepted from a client but not yet delivered.
	// A real MTA would retry; here delivery is synchronous inside one tick,
	// so the queue is only non-empty if a delivery target is down.
	Queue []MailMsg
	// Delivered counts every message this world has accepted, for status text.
	Delivered int
}

// MailMsg is one accepted message. Body is stored separately in the mbox.
type MailMsg struct {
	At      string // world-clock stamp at acceptance
	From    string // envelope sender, e.g. alex@pc-alex.local
	To      string // envelope recipient, e.g. alex@pc-alex.local
	Subject string
	Body    string
	Device  string // device id the message was accepted on
}

// MailSpoolDir is the daemon-owned spool. A user cannot read another user's
// spool entry here; it exists so the daemon has somewhere real to write.
const MailSpoolDir = "/var/spool/mail"

// MailInboxDir is the user-readable mailbox directory.
const MailInboxDir = "/var/mail"

// MailboxPath returns the inbox file for a user on a device.
func MailboxPath(user string) string { return MailInboxDir + "/" + user }

// SeedMail installs the spool/inbox directories on a device and returns its
// MailBox. Called from world_init for every device that runs a mail daemon.
func SeedMail(d *Device) *MailBox {
	d.FS.MkdirAll(MailSpoolDir, 01777, "root", "mail")
	d.FS.MkdirAll(MailInboxDir, 01777, "root", "mail")
	// A real box has the mail group; keep it visible so `id`/`ls -l` are honest.
	return &MailBox{Domain: "neohome.example"}
}

// MailAddr builds the canonical address for a device-local account.
func MailAddr(user, hostname string) string {
	return fmt.Sprintf("%s@%s.local", user, hostname)
}

// AppendToInbox writes one RFC-ish message into a user's inbox mbox. The write
// goes through the VFS as the recipient so a read-only mailbox really refuses.
func AppendToInbox(d *Device, user, from, subject, body, stamp string) error {
	mbox := MailboxPath(user)
	old, _ := d.FS.Read(mbox)
	msg := fmt.Sprintf("From %s %s\nSubject: %s\nDate: %s\n\n%s\n\n---\n",
		from, stamp, subject, stamp, strings.TrimRight(body, "\n"))
	// Owner is the recipient: the mailbox is theirs to read and delete.
	d.FS.Write(mbox, string(old)+msg, 0600, user, "mail")
	return nil
}

// ReadInbox returns the raw mbox bytes for a user, honouring permissions.
// existence and permission are reported separately: "you may not read this"
// and "there is no mailbox here" are different findings, and a caller that
// conflates them lies about the state of the world.
func ReadInbox(d *Device, user string, u *User) (data []byte, exists, permitted bool) {
	return d.FS.ReadPathAs(MailboxPath(user), u)
}

// World.ReadInbox reads the *calling user's* mailbox on the device they are
// logged into. This is the shell-facing entry point: `mail` with no arguments
// must read the mailbox of the session's own account, as the real binary does,
// not "whichever player owns this PC".
func (w *World) ReadInbox(d *Device, user string, u *User) (data []byte, exists, permitted bool) {
	if d == nil {
		return nil, false, false
	}
	return ReadInbox(d, user, u)
}

// DeviceForPlayer returns the device a player logs into, or nil. Mail delivery
// needs it because a mailbox belongs to an account on a machine, not to a
// player record in the abstract.
func DeviceForPlayer(p *Player) string {
	if p == nil {
		return ""
	}
	return p.PC
}

// LocalUserFor resolves an address to a local account on a device. It accepts
// the forms a real MTA accepts: bare "alex", "alex@hostname", or a fully
// qualified "alex@hostname.local". A recipient on a *different* device is not
// local here and is reported as such, so callers do not silently deliver
// cross-host mail into the wrong mailbox.
func LocalUserFor(d *Device, addr string) (user string, local bool) {
	name := addr
	if i := strings.Index(addr, "@"); i >= 0 {
		host := addr[i+1:]
		name = addr[:i]
		if host != d.Hostname && host != d.ID && host != "localhost" {
			return "", false
		}
	}
	if name == "" {
		return "", false
	}
	if d.FindUser(name) == nil {
		return "", false
	}
	return name, true
}

// --- exported helpers used by shell + init ---

// seedMail installs the mail directories on every device the world already
// built. Called from world_init after the device graph exists, because the
// directories are part of a device's filesystem, not of a subsystem object.
func seedMail(w *World) {
	w.mail = &MailBox{Domain: "neohome.example"}
	for _, d := range w.Devices {
		SeedMail(d)
	}
}

// Mail returns the world's mail subsystem, creating it on first use.
func (w *World) Mail() *MailBox {
	if w.mail == nil {
		w.mail = &MailBox{Domain: "neohome.example"}
	}
	return w.mail
}

// MailDomain exposes the local delivery domain.
func (w *World) MailDomain() string { return w.Mail().Domain }

// DeliverLocal accepts a message for local delivery and writes it into the
// recipient's inbox on the given device. It is the single entry point every
// other subsystem (jobs, NPCs, an smtpd handler) goes through, so delivery
// side effects are always the same and always traceable.
//
// Returns an error a real MTA would return: unknown local user, or a mailbox
// the daemon cannot write. Nothing is silently dropped.
func (w *World) DeliverLocal(d *Device, from, toUser, subject, body string) error {
	u := d.FindUser(toUser)
	if u == nil {
		return fmt.Errorf("unknown local user %q on %s", toUser, d.Hostname)
	}
	stamp := w.Sim.Format("2006-01-02 15:04")
	if err := AppendToInbox(d, toUser, from, subject, body, stamp); err != nil {
		return err
	}
	mb := w.Mail()
	mb.Delivered++
	mb.Queue = append(mb.Queue, MailMsg{At: stamp, From: from, To: toUser + "@" + d.Hostname, Subject: subject, Body: body, Device: d.ID})
	// Traceable, world-visible consequences: a device log line, an event, and
	// the world-wide mail log. All three already exist for other subsystems.
	d.Logf("info", "smtpd", "accepted %dB message from %s for %s", len(body), from, toUser)
	w.AddEvent(d.ID, "info", "mail", "mail from %s to %s delivered on %s", from, toUser, d.Hostname)
	w.MailLog = append(w.MailLog, fmt.Sprintf("%s %s -> %s@%s: %s", stamp, from, toUser, d.Hostname, subject))
	if len(w.MailLog) > 200 {
		w.MailLog = w.MailLog[len(w.MailLog)-200:]
	}
	return nil
}

// InboxSubjects returns the subjects in a user's inbox, oldest first — the
// real content of /var/mail/<user>, parsed, not a shadow list.
func InboxSubjects(d *Device, user string) []string {
	data, ok := d.FS.Read(MailboxPath(user))
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Subject: ") {
			out = append(out, strings.TrimPrefix(line, "Subject: "))
		}
	}
	return out
}

// MailboxUsers lists every account with an inbox file on a device, sorted.
func MailboxUsers(d *Device) []string {
	var out []string
	for _, name := range d.FS.List(MailInboxDir) {
		if d.FS.Exists(MailInboxDir + "/" + name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ---- routing (WS-0.4) ----

// RouteResult reports what a delivery attempt actually did. A caller that only
// gets "ok" cannot distinguish "delivered to a mailbox" from "handed to a queue
// that will never drain", and those are very different worlds.
type RouteResult struct {
	Delivered  bool   // the message is in a mailbox now
	Queued     bool   // accepted by an MTA that is not currently reachable
	Via        string // "local", "relay:<device>", "npc", "bounce"
	Recipient  string // resolved user, when Delivered
	Diagnostic string // why it bounced or was queued
}

// RouteMail is the single outbound entry point: it decides where a message goes
// and performs the delivery through the same gates a real MTA obeys.
//
// Routing rules, in order:
//  1. local user on this device  -> deliver into that mailbox;
//  2. account on another device  -> that device's MTA must be running and the
//     sender must be able to reach it, otherwise the message queues (real
//     deferred-delivery behaviour) instead of silently vanishing;
//  3. an NPC                    -> the NPC's own mail handling;
//  4. anything else             -> bounce with a diagnostic.
func (w *World) RouteMail(from *Device, fromUser, to, subject, body string) RouteResult {
	fromAddr := MailAddr(fromUser, from.Hostname)

	// 1: the recipient is on *this* machine. A local delivery never touches
	// the network, so it is decided before anything else — and this also makes
	// an unqualified "alex" mean the sender's own account, as it does on a
	// real host.
	if from != nil {
		if user, local := LocalUserFor(from, to); local {
			if err := w.DeliverLocal(from, fromAddr, user, subject, body); err != nil {
				return RouteResult{Diagnostic: err.Error(), Via: "bounce"}
			}
			return RouteResult{Delivered: true, Via: "local", Recipient: user}
		}
	}

	// 2: another machine in the world. Walk w.Order, not the map: with the
	// same account name present on several hosts (a VM guest often mirrors its
	// owner's name), map iteration order would make the recipient — and
	// therefore whether the message is delivered at all — non-deterministic.
	for _, id := range w.Order {
		d := w.Devices[id]
		if d == nil || (from != nil && d.ID == from.ID) {
			continue
		}
		user, local := LocalUserFor(d, to)
		if !local {
			continue
		}
		// Remote host: the MTA there must genuinely accept. A down MTA is a
		// queue entry, not a silent drop.
		r := w.relayTo(d, from, fromAddr, user, subject, body)
		if r.Delivered {
			return r
		}
		w.Mail().Queue = append(w.Mail().Queue, MailMsg{
			At: w.Sim.Format("2006-01-02 15:04"), From: fromAddr,
			To: user + "@" + d.Hostname, Subject: subject, Body: body, Device: d.ID,
		})
		from.Logf("warning", "smtp", "queued message for %s@%s: %s", user, d.Hostname, r.Diagnostic)
		w.AddEvent(from.ID, "warning", "mail", "mail to %s@%s queued: %s", user, d.Hostname, r.Diagnostic)
		return RouteResult{Queued: true, Via: "relay:" + d.ID, Recipient: user, Diagnostic: r.Diagnostic}
	}

	// 3: NPCs are handled by the world's own mail layer.
	for _, n := range w.NPCNames {
		if n == to || strings.HasPrefix(to, n+"@") {
			w.NPCMail(fromAddr, to, subject, body)
			return RouteResult{Delivered: true, Via: "npc", Recipient: n}
		}
	}

	// 4: bounce.
	diag := fmt.Sprintf("no such recipient: %s", to)
	from.Logf("warning", "smtp", "bounced message from %s: %s", fromAddr, diag)
	w.AddEvent(from.ID, "warning", "mail", "mail from %s bounced: %s", fromAddr, diag)
	return RouteResult{Diagnostic: diag, Via: "bounce"}
}

// relayTo performs the actual cross-host handoff, respecting every gate Dial
// enforces: power, route, firewall and service state.
func (w *World) relayTo(dst *Device, src *Device, from, user, subject, body string) RouteResult {
	mta := dst.Svc("smtpd")
	if mta == nil {
		return RouteResult{Diagnostic: "no mail transfer agent on " + dst.Hostname, Via: "bounce"}
	}
	// The port must really answer before we claim the message was accepted.
	svc, _, msg := Dial(src, dst.FirstLANIP(), mta.Port)
	if svc == nil {
		return RouteResult{Diagnostic: msg, Via: "bounce"}
	}
	// A refused submission must not leave a trace claiming success.
	if err := w.DeliverLocal(dst, from, user, subject, body); err != nil {
		return RouteResult{Diagnostic: err.Error(), Via: "bounce"}
	}
	dst.Logf("info", "smtpd", "relayed message from %s for local delivery to %s", from, user)
	return RouteResult{Delivered: true, Via: "relay:" + dst.ID, Recipient: user}
}
