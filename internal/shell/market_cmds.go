package shell

import (
	"fmt"
	"strconv"
	"strings"

	"neohome/internal/core"
)

// The bazaar: structured listings and atomic swaps on top of the same bank,
// ftp and evidence every other trade uses. Like the BBS and IRC clients, the
// connection itself is the gate — resolve, dial, and a stopped marketd means
// no market.

func init() {
	builtinTable["market"] = cmdMarket
}

// bazaarPort is the daemon's port on bazaar.neohome.example (see core).
const bazaarPort = 8444

func dialBazaar(s *Shell) int {
	ip, ok, how := core.DNSAnswer(s.Dev, "bazaar.neohome.example")
	if !ok {
		s.errf("market: resolve bazaar.neohome.example: %s", how)
		return 1
	}
	svc, _, msg := core.Dial(s.Dev, ip, bazaarPort)
	if svc == nil {
		s.errf("market: connect to bazaar.neohome.example (%s): %s", ip, msg)
		return 1
	}
	return 0
}

func cmdMarket(s *Shell, args []string) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "", "list", "ls":
		return marketList(s)
	case "info", "show":
		if len(args) < 2 {
			s.errf("usage: market info ID")
			return 1
		}
		return marketInfo(s, args[1])
	case "sell-cred":
		return marketSellCred(s, args[1:])
	case "sell-file":
		return marketSellFile(s, args[1:])
	case "buy":
		if len(args) < 2 {
			s.errf("usage: market buy ID")
			return 1
		}
		return marketBuy(s, args[1])
	}
	s.errf("usage: market [list|info ID|sell-cred USER@HOST PRICE [--ftp]|sell-file BAZAAR-PATH PRICE|buy ID]  (prices in $)")
	return 1
}

func marketList(s *Shell) int {
	if dialBazaar(s) != 0 {
		return 1
	}
	live := s.W.MarketLive()
	if len(live) == 0 {
		fmt.Fprintln(s.Out, "the board is empty")
		return 0
	}
	fmt.Fprintf(s.Out, "%-6s %-5s %-28s %10s  %s\n", "ID", "WHAT", "GOODS", "PRICE", "SELLER")
	for _, l := range live {
		goods := ""
		switch l.Kind {
		case "cred":
			dst := s.W.Devices[l.Host]
			host := l.Host
			if dst != nil {
				host = dst.Hostname
			}
			goods = fmt.Sprintf("%s@%s (%s/%d)", l.Account, host, l.Proto, l.Port)
		case "file":
			goods = fmt.Sprintf("%s (%d bytes)", l.Path, l.Size)
		}
		fmt.Fprintf(s.Out, "%-6s %-5s %-28s $%-9d %s\n",
			l.ID, l.Kind, goods, l.Price/100, l.Seller)
	}
	fmt.Fprintf(s.Out, "\noperator fee 5%% on every sale; credentials are re-verified before money moves\n")
	return 0
}

func marketInfo(s *Shell, id string) int {
	if dialBazaar(s) != 0 {
		return 1
	}
	for _, l := range s.W.MarketLive() {
		if l.ID != id {
			continue
		}
		fmt.Fprintf(s.Out, "%s  %s  $%d (fee $%d)  seller %s  listed %s\n",
			l.ID, l.Kind, l.Price/100, l.Fee/100, l.Seller, l.Created.Format("2006-01-02 15:04"))
		switch l.Kind {
		case "cred":
			dst := s.W.Devices[l.Host]
			host := l.Host
			if dst != nil {
				host = dst.Hostname
			}
			fmt.Fprintf(s.Out, "account: %s@%s over %s/%d\n", l.Account, host, l.Proto, l.Port)
			fmt.Fprintf(s.Out, "the password is revealed on purchase only\n")
		case "file":
			fmt.Fprintf(s.Out, "path: %s  sha256: %s\n", l.Path, l.Hash)
			fmt.Fprintf(s.Out, "fetch it after purchase with: ftp -A bazaar.neohome.example\n")
		}
		return 0
	}
	s.errf("market: no live listing %s", id)
	return 1
}

func marketSellCred(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: market sell-cred USER@HOST PRICE [--ftp]")
		return 1
	}
	if dialBazaar(s) != 0 {
		return 1
	}
	proto, port := "ssh", 22
	rest := args[:0]
	for _, a := range args {
		if a == "--ftp" {
			proto, port = "ftp", 21
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) < 2 {
		s.errf("usage: market sell-cred USER@HOST PRICE [--ftp]")
		return 1
	}
	who := rest[0]
	price, err := marketDollars(rest[1])
	if err != nil {
		s.errf("market: bad price %q (whole dollars)", rest[1])
		return 1
	}
	u, host, ok := splitUserHost(who)
	if !ok {
		s.errf("market: want USER@HOST, got %q", who)
		return 1
	}
	fmt.Fprintf(s.Out, "Password for %s: ", who)
	pass := s.ReadPasswordLine("")
	if pass == "" {
		s.errf("market: no password supplied: nothing to sell")
		return 1
	}
	l, err := s.W.MarketListCred(s.User.Name, u, host, proto, port, pass, price)
	if err != nil {
		s.errf("market: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "listed %s: %s@%s (%s/%d) for $%d — the bazaar probed it once (that attempt is on the target's logs)\n",
		l.ID, u, host, proto, port, price/100)
	return 0
}

func marketSellFile(s *Shell, args []string) int {
	if len(args) < 2 {
		s.errf("usage: market sell-file BAZAAR-PATH PRICE")
		return 1
	}
	if dialBazaar(s) != 0 {
		return 1
	}
	price, err := marketDollars(args[1])
	if err != nil {
		s.errf("market: bad price %q (whole dollars)", args[1])
		return 1
	}
	l, err := s.W.MarketListFile(s.User.Name, args[0], price)
	if err != nil {
		s.errf("market: %v", err)
		return 1
	}
	fmt.Fprintf(s.Out, "listed %s: %s (%d bytes, sha256 %.12s) for $%d\n",
		l.ID, l.Path, l.Size, l.Hash, price/100)
	return 0
}

func marketBuy(s *Shell, id string) int {
	if dialBazaar(s) != 0 {
		return 1
	}
	l, secret, err := s.W.MarketBuy(s.User.Name, id)
	if err != nil {
		s.errf("market: %v", err)
		return 1
	}
	switch l.Kind {
	case "cred":
		fmt.Fprintf(s.Out, "bought %s for $%d (fee $%d)\n", l.ID, l.Price/100, l.Fee/100)
		fmt.Fprintf(s.Out, "credential %s: %s\n", l.Account, secret)
		fmt.Fprintf(s.Out, "it worked when the bazaar last probed it seconds ago — what you do with it is your own trail now\n")
	case "file":
		fmt.Fprintf(s.Out, "bought %s for $%d (fee $%d)\n", l.ID, l.Price/100, l.Fee/100)
		fmt.Fprintf(s.Out, "drop is now readable: ftp -A bazaar.neohome.example, get %s\n", secret)
	}
	return 0
}

// marketDollars reads whole dollars into the cents the bank counts in.
func marketDollars(v string) (int64, error) {
	v = strings.TrimSpace(strings.TrimSuffix(v, ".00"))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("bad price")
	}
	return int64(n) * 100, nil
}

func splitUserHost(who string) (user, host string, ok bool) {
	i := strings.LastIndexByte(who, '@')
	if i <= 0 || i == len(who)-1 {
		return "", "", false
	}
	return who[:i], who[i+1:], true
}
