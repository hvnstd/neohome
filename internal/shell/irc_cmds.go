package shell

// irc — the client for the community network (#local, #help). It is a network
// operation like the board is (§19): the name resolves through the same
// resolver everything else uses, the ircd really answers on its port, and if
// the server is down the channel is down. What people say is world state, so
// NPC replies stay grounded in the world as it actually is.

import (
	"fmt"
	"sort"
	"strings"

	"neohome/internal/core"
)

func init() {
	builtinTable["irc"] = cmdIRC
}

// ircHost and ircPort name the public community server every resident knows.
const ircHost = "irc.neohome.example"

const ircPort = 6667

// dialIRC is the honest gate: resolve, connect, and only then talk.
func dialIRC(s *Shell) int {
	ip, ok, how := core.DNSAnswer(s.Dev, ircHost)
	if !ok {
		s.errf("irc: resolve %s: %s", ircHost, how)
		return 1
	}
	svc, _, msg := core.Dial(s.Dev, ip, ircPort)
	if svc == nil {
		s.errf("irc: connect to %s (%s): %s", ircHost, ip, msg)
		return 1
	}
	return 0
}

func cmdIRC(s *Shell, args []string) int {
	if dialIRC(s) != 0 {
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintf(s.Out, "channels: ")
		var chs []string
		for c := range s.W.Chat.Channels {
			chs = append(chs, c)
		}
		sort.Strings(chs)
		fmt.Fprintln(s.Out, strings.Join(chs, "  "))
		fmt.Fprintln(s.Out, "online:   "+strings.Join(s.W.IRCNicks(), "  "))
		fmt.Fprintln(s.Out, "\nusage: irc read [#chan] | irc say <message> [#chan]")
		return 0
	}
	switch args[0] {
	case "read", "log", "tail":
		ch := "#local"
		if len(args) > 1 {
			ch = args[1]
		}
		for _, m := range s.W.Chat.History {
			if m.Chan != ch {
				continue
			}
			fmt.Fprintf(s.Out, "[%s] <%s> %s\n", m.At.Format("15:04"), m.Nick, m.Text)
		}
		return 0
	case "say", "msg":
		if len(args) < 2 {
			s.errf("usage: irc say MESSAGE [#chan]")
			return 1
		}
		ch := "#local"
		body := args[1:]
		if len(body) > 1 && strings.HasPrefix(body[len(body)-1], "#") {
			ch = body[len(body)-1]
			body = body[:len(body)-1]
		}
		s.W.IRCSend(s.User.Name, ch, strings.Join(body, " "))
		for _, m := range s.W.Chat.History[len(s.W.Chat.History)-3:] {
			fmt.Fprintf(s.Out, "[%s] <%s> %s\n", m.At.Format("15:04"), m.Nick, m.Text)
		}
		return 0
	}
	s.errf("usage: irc [read|say]")
	return 1
}
