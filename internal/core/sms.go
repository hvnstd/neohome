package core

// SMS (WS-1.2): the phone as a real device profile. Spec §15 lists Phone
// among the household devices and §209 says a phone is "just another
// default profile" — so the phone here is a battery-powered pocket
// computer with a terminal, a message spool that lives in its filesystem,
// and one honestly non-obvious property: SMS rides the cellular radio, not
// your Wi-Fi, so it keeps working when the router (and the whole home
// internet) does not. That distinction is real and the world respects it.
//
// The shape of SMS is owned by this file; World only carries the pointer.

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// SMSCfg is the world's phone registry: which number belongs to which
// phone device, and the batteries those phones are running on.
type SMS struct {
	Numbers map[string]string // phone number -> device id
	Battery map[string]int    // device id -> percent
	Seq     map[string]int    // device id -> next message number
}

// SMSSpoolDir is where messages live on a phone's filesystem.
const SMSSpoolDir = "/var/spool/sms"

// Well-known numbers. The bank's is in every contact list.
const (
	SMSBankNumber = "555-0100"
)

// SMSMsg is one message as it sits in a spool.
type SMSMsg struct {
	Num  int
	From string
	Date string
	Text string
}

func smsSpoolPath(d *Device, num int) string {
	return fmt.Sprintf("%s/%04d.txt", SMSSpoolDir, num)
}

// seedSMS registers the world's phones, writes the contact lists, and
// seeds a small truthful conversation: the bank's enablement notice and
// mara's pointer at the same router story every other channel tells.
func seedSMS(w *World) {
	alexPhone := w.Devices["phone-alex"]
	maraPhone := w.Devices["phone-mara"]
	if alexPhone == nil || maraPhone == nil {
		return
	}
	w.SMS = &SMS{
		Numbers: map[string]string{
			"555-0010":    alexPhone.ID,
			"555-0001":    maraPhone.ID,
			SMSBankNumber: "bank", // the bank sends; it does not receive
		},
		Battery: map[string]int{
			alexPhone.ID: 84,
			maraPhone.ID: 62,
		},
		Seq: map[string]int{},
	}
	alexPhone.FS.Write("/home/alex/.contacts",
		"mara 555-0001\nmira-9 555-0002\ndaemon42 555-0003\nbank "+SMSBankNumber+"\n",
		0600, "alex", "alex")
	maraPhone.FS.Write("/home/mara/.contacts",
		"alex 555-0010\n",
		0600, "mara", "mara")
	stamp := w.Sim.Add(-3 * time.Hour).Format("2006-01-02 15:04")
	w.smsDeliver(alexPhone, "bank", "neohome mobile: SMS notifications enabled for your account", stamp)
	stamp2 := w.Sim.Add(-2 * time.Hour).Format("2006-01-02 15:04")
	w.smsDeliver(alexPhone, "mara", "saw you on the bbs. if your resolver dies again it is the resolv-file on your gateway, same as always", stamp2)
}

// PhoneFor returns the phone device owned by name, or nil.
func (w *World) PhoneFor(owner string) *Device {
	for _, id := range w.Order {
		d := w.Devices[id]
		if d != nil && d.Profile == "phone" && d.Owner == owner {
			return d
		}
	}
	return nil
}

// smsDeliver writes one message into a phone's spool as a real file owned
// by the phone's owner, with the evidence trail every delivery has.
func (w *World) smsDeliver(d *Device, from, text, stamp string) error {
	if d == nil {
		return fmt.Errorf("no phone")
	}
	if w.SMS == nil {
		return fmt.Errorf("no phone network")
	}
	if w.SMS.Battery[d.ID] <= 0 {
		return fmt.Errorf("phone switched off")
	}
	if stamp == "" {
		stamp = w.Sim.Format("2006-01-02 15:04")
	}
	w.SMS.Seq[d.ID]++
	num := w.SMS.Seq[d.ID]
	msg := fmt.Sprintf("From: %s\nDate: %s\n\n%s\n", from, stamp, strings.TrimRight(text, "\n"))
	d.FS.Write(smsSpoolPath(d, num), msg, 0600, d.Owner, d.Owner)
	d.Logf("info", "sms", "message %d from %s", num, from)
	return nil
}

// SMSSend sends one message from a phone session: the recipient is a
// contact name resolved through the sender's own contact list, or a bare
// number. Delivery rides the cellular radio — no Dial, no router — but a
// switched-off phone receives nothing, and every hop leaves evidence.
func (w *World) SMSSend(from *Device, fu *User, to, text string) error {
	if from == nil || fu == nil || w.SMS == nil {
		return fmt.Errorf("no phone network")
	}
	// a dead phone cannot send any more than it can receive
	if w.SMS.Battery[from.ID] <= 0 {
		return fmt.Errorf("phone switched off")
	}
	number := to
	if !strings.HasPrefix(to, "555-") {
		contacts, ok := from.FS.Read(path.Join(fu.Home, ".contacts"))
		if !ok {
			return fmt.Errorf("no contact list on this phone")
		}
		found := ""
		for _, line := range strings.Split(string(contacts), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == to {
				found = f[1]
			}
		}
		if found == "" {
			return fmt.Errorf("no such contact: %s", to)
		}
		number = found
	}
	dstID, ok := w.SMS.Numbers[number]
	if !ok {
		return fmt.Errorf("no such number on the network: %s", number)
	}
	var dst *Device
	if dstID != "bank" {
		dst = w.Devices[dstID]
	}
	if dst == nil {
		return fmt.Errorf("no such number on the network: %s", number)
	}
	if err := w.smsDeliver(dst, fu.Name, text, ""); err != nil {
		return err
	}
	from.Logf("info", "sms", "sent to %s (%s)", to, number)
	// the person on the other end is real: they answer, state-aware
	w.smsNPCReply(from, dst, fu, text)
	return nil
}

// smsNPCReply delivers an immediate, keyword-driven answer from the
// phone's owner when that owner is an NPC — the same convention the IRC
// channel and the BBS follow.
func (w *World) smsNPCReply(from, dst *Device, fu *User, text string) {
	owner := dst.Owner
	isNPC := false
	for _, n := range w.NPCNames {
		if n == owner {
			isNPC = true
		}
	}
	if !isNPC {
		return
	}
	low := lower(text)
	reply := "k"
	switch {
	case containsAny(low, "dns", "wifi", "router", "resolv"):
		reply = "gateway again? check the resolv-file. it is always the resolv-file"
	case containsAny(low, "lock", "door", "camera"):
		reply = "your IoT ships admin/admin and it is on the bbs. change it"
	case containsAny(low, "job", "money", "work"):
		reply = "the job board pays. i do not"
	case containsAny(low, "hi", "hey", "hello"):
		reply = "what do you want"
	}
	w.smsDeliver(from, owner, reply, "")
}

// SMSInbox lists a phone's spool, oldest first, parsed from the files.
func (w *World) SMSInbox(d *Device, u *User) ([]SMSMsg, error) {
	if d == nil || w.SMS == nil {
		return nil, fmt.Errorf("no phone")
	}
	if !d.FS.CanRead(SMSSpoolDir, u) {
		return nil, fmt.Errorf("permission denied")
	}
	var out []SMSMsg
	for _, p := range d.FS.List(SMSSpoolDir) {
		data, ok := d.FS.Read(p)
		if !ok {
			continue
		}
		m := SMSMsg{}
		fmt.Sscanf(path.Base(p), "%d", &m.Num)
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "From: ") {
				m.From = strings.TrimPrefix(line, "From: ")
			}
			if strings.HasPrefix(line, "Date: ") {
				m.Date = strings.TrimPrefix(line, "Date: ")
			}
			if line == "" {
				break
			}
		}
		if i := strings.Index(string(data), "\n\n"); i >= 0 {
			m.Text = strings.TrimRight(string(data)[i+2:], "\n")
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Num < out[j].Num })
	return out, nil
}

// BankSMS texts a deposit receipt to the account owner's phone, the way a
// real bank does for incoming money. No phone on file, no SMS.
func (w *World) BankSMS(owner, text string) {
	p := w.PhoneFor(owner)
	if p == nil {
		return
	}
	w.smsDeliver(p, "bank", text, "")
}

// SMSTick drains the batteries: a phone at zero powers off — its sshd
// really stops, its spool stops receiving — until someone physically
// charges it. A phone at 15% warns once on the way down.
func (w *World) SMSTick() {
	if w.SMS == nil {
		return
	}
	if w.TickCount%480 != 0 {
		return
	}
	for id, pct := range w.SMS.Battery {
		if pct <= 0 {
			continue
		}
		w.SMS.Battery[id] = pct - 1
		d := w.Devices[id]
		if d == nil {
			continue
		}
		switch w.SMS.Battery[id] {
		case 15:
			d.Logf("warning", "battery", "%d%% — find a charger", w.SMS.Battery[id])
		case 0:
			if svc := d.Svc("sshd"); svc != nil && svc.State == "running" {
				d.StopService("sshd")
				d.Logf("err", "battery", "phone powered off")
				w.AddEvent(d.ID, "err", "battery", "phone powered off: battery empty")
			}
		}
	}
}

// PhoneCharge is the physical act: dock the phone, battery full, terminal
// reachable again.
func (w *World) PhoneCharge(d *Device) {
	if d == nil || w.SMS == nil {
		return
	}
	w.SMS.Battery[d.ID] = 100
	if svc := d.Svc("sshd"); svc != nil && svc.State != "running" {
		d.StartService("sshd")
	}
	d.Logf("notice", "battery", "phone charged to 100%%")
}
