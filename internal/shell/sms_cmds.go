package shell

// sms and phone — the pocket computer's surface. `sms` only speaks on a
// phone; `phone` shows its battery and does the one physical thing a
// pocket computer needs: charging. Messages are files in the phone's own
// spool, and delivery rides the cellular radio, not your Wi-Fi.

import (
	"fmt"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["sms"] = cmdSms
	builtinTable["phone"] = cmdPhone
}

// requirePhone guards both commands: a message spool only exists on a
// phone, and pretending otherwise would lie about the device.
func requirePhone(s *Shell, tool string) *core.Device {
	if s.Dev.Profile != "phone" {
		s.errf("%s: this is not a phone (ssh to it first: ssh %s@phone)", tool, s.User.Name)
		return nil
	}
	return s.Dev
}

func cmdSms(s *Shell, args []string) int {
	d := requirePhone(s, "sms")
	if d == nil {
		return 1
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list":
		msgs, err := s.W.SMSInbox(d, s.User)
		if err != nil {
			s.errf("sms: %v", err)
			return 1
		}
		if len(msgs) == 0 {
			fmt.Fprintln(s.Out, "no messages")
			return 0
		}
		for _, m := range msgs {
			fmt.Fprintf(s.Out, "%3d  %s  %-10s %s\n", m.Num, m.Date, m.From, firstLine(m.Text))
		}
		fmt.Fprintf(s.Out, "\nread one: sms read N | reply: sms send WHO TEXT\n")
		return 0
	case "read":
		if len(args) < 2 {
			s.errf("usage: sms read N")
			return 1
		}
		msgs, err := s.W.SMSInbox(d, s.User)
		if err != nil {
			s.errf("sms: %v", err)
			return 1
		}
		for _, m := range msgs {
			if fmt.Sprint(m.Num) == args[1] {
				fmt.Fprintf(s.Out, "From: %s\nDate: %s\n\n%s\n", m.From, m.Date, m.Text)
				return 0
			}
		}
		s.errf("sms: no such message: %s", args[1])
		return 1
	case "send":
		if len(args) < 3 {
			s.errf("usage: sms send WHO TEXT")
			return 1
		}
		if err := s.W.SMSSend(d, s.User, args[1], strings.Join(args[2:], " ")); err != nil {
			s.errf("sms: %v", err)
			return 1
		}
		fmt.Fprintf(s.Out, "sent to %s\n", args[1])
		return 0
	}
	s.errf("usage: sms [list|read N|send WHO TEXT]")
	return 1
}

func cmdPhone(s *Shell, args []string) int {
	// Docking a phone is hands, not a shell command: it works from any of
	// the owner's machines, because a dead phone whose sshd is stopped would
	// otherwise be a one-way door with no way back in. Reading its status
	// stays on the phone itself — you cannot read a screen you cannot reach.
	if s.Dev.Profile != "phone" {
		if len(args) == 1 && args[0] == "charge" {
			d := s.W.PhoneFor(s.User.Name)
			if d == nil {
				s.errf("phone: no phone for %s", s.User.Name)
				return 1
			}
			s.W.PhoneCharge(d)
			fmt.Fprintf(s.Out, "docked %s: battery 100%%\n", d.Hostname)
			return 0
		}
		requirePhone(s, "phone")
		return 1
	}
	d := requirePhone(s, "phone")
	if d == nil {
		return 1
	}
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "status":
		pct := s.W.SMS.Battery[d.ID]
		state := "on battery"
		if s.W.SMS.Battery[d.ID] <= 0 {
			state = "powered off"
		}
		fmt.Fprintf(s.Out, "%s (%s)\nbattery: %d%% (%s)\nmessages: spool at %s\n",
			d.Hostname, d.OS.Distro, pct, state, core.SMSSpoolDir)
		return 0
	case "charge":
		s.W.PhoneCharge(d)
		fmt.Fprintln(s.Out, "docked: battery 100%")
		return 0
	}
	s.errf("usage: phone [status|charge]")
	return 1
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
