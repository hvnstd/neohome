package core

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Black market (Phase 2) — the bazaar
//
// The BBS market board is classifieds: posts with prices, no execution. The
// bazaar is the trade: structured listings, atomic swaps, a fee, and evidence.
// Two goods, both real state:
//
//	credentials — a working login the seller proves by surviving one real
//	  login probe from the market host (logged on the target, like any other
//	  attempt). The buyer gets the password; what they do with it is their
//	  own evidence trail. A seller who rotates the password after listing
//	  learns what the buy-time re-probe is for.
//	dead-drop files — bytes already sitting on the bazaar host (put there
//	  with the same ftp/scp verbs as everything else). The market pins the
//	  hash at list time, flips the file world-readable on sale, and records
//	  who bought it. Fraud (re-listing someone else's drop) is possible and
//	  permanently attributed — the game answers fraud with evidence, not
//	  prevention.
//
// Money moves through the same Transfer every other payment uses, so both
// sides' bank histories carry the trade. Every listing and every sale files
// evidence under its own kind, which is what makes "buy a password" heat the
// world the same way "guess a password" does.
// ---------------------------------------------------------------------------

// bazaarHost is the market's address. bazaarPort is its daemon.
const bazaarHost = "bazaar.neohome.example"

const bazaarPort = 8444

// marketFeeBps is the operator's cut in basis points, with a floor so small
// trades still pay for the probe that verified them.
const marketFeeBps = 500

const marketFeeMin = 25 // cents

// marketOperator is the bank account the fee accrues to (created on first
// trade by Transfer, like every other new payee).
const marketOperator = "bazaar"

// MarketState is the bazaar's memory: what is for sale and what changed
// hands. Shape owned by this file; the pointer lives on World.
type MarketState struct {
	BazaarID string
	Seq      int
	Fees     int64 // lifetime fees collected, in cents
	Listings []*Listing
}

// Listing is one thing for sale. Sold records stay: the market remembers who
// bought what, which is exactly what an investigator hopes to seize.
type Listing struct {
	ID      string
	Kind    string // cred | file
	Seller  string // owner name, as the bank knows them
	Price   int64  // cents
	Fee     int64  // cents, taken on sale
	Created time.Time
	// cred goods
	Host    string // device id the account lives on
	Account string
	Pass    string // the secret itself: seizing the market seizes this
	Proto   string // ssh | ftp
	Port    int
	// file goods
	Path string // path on the bazaar host
	Hash string // sha256 at list time
	Size int
	// sale record
	Sold   bool
	Buyer  string
	SoldAt time.Time
}

// marketFee splits a price into seller proceeds and operator fee.
func marketFee(price int64) (proceeds, fee int64) {
	fee = price * marketFeeBps / 10000
	if fee < marketFeeMin {
		fee = marketFeeMin
	}
	if fee >= price {
		fee = price - 1
		if fee < 0 {
			fee = 0
		}
	}
	return price - fee, fee
}

// Market is the world's bazaar, creating it on first use so an older save
// without one never panics.
func (w *World) Market() *MarketState {
	if w.Bazaar == nil {
		w.Bazaar = &MarketState{}
	}
	return w.Bazaar
}

// marketFindDevice resolves what a player typed as a host: a hostname the
// world knows, or an address in its map.
func (w *World) marketFindDevice(host string) *Device {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	for _, id := range w.Order {
		if d := w.Devices[id]; d != nil && (d.Hostname == host || d.ID == host) {
			return d
		}
	}
	if id, ok := w.IPMap[host]; ok {
		return w.Devices[id]
	}
	return nil
}

// marketFrontAddr is the address a credential is actually verified (and
// attacked) through: a NATed box with a published forward is reached through
// the router's front door, the way the scanner and every buyer reaches it.
// Without a forward the box's own address is used, which the internet
// correctly cannot route to.
func (w *World) marketFrontAddr(dst *Device, port int) string {
	if dst == nil {
		return ""
	}
	if ip := dst.WANIP(); ip != "" {
		return ip
	}
	lan := dst.FirstLANIP()
	if lan == "" || w == nil {
		return lan
	}
	for _, id := range w.Order {
		r := w.Devices[id]
		if r == nil || r.Profile != "router" {
			continue
		}
		front := r.WANIP()
		if front == "" {
			continue
		}
		for _, red := range r.Redirects() {
			if !red.Enabled || red.DstIP != lan {
				continue
			}
			dp := red.DPort
			if dp == 0 {
				dp = red.WPort
			}
			if red.DMZ || red.WPort == 0 || dp == port || red.DPort == -1 {
				return front
			}
		}
	}
	return lan
}

// MarketListCred lists a working login. The password is proven with one real
// probe from the bazaar host before anything is listed: an unverifiable
// credential is refused, not warehoused.
func (w *World) MarketListCred(seller, account, host string, proto string, port int, pass string, price int64) (*Listing, error) {
	m := w.Market()
	bazaar := w.Devices[m.BazaarID]
	if bazaar == nil {
		return nil, fmt.Errorf("the bazaar is gone")
	}
	if price <= 0 {
		return nil, fmt.Errorf("a price must be positive")
	}
	if pass == "" {
		return nil, fmt.Errorf("no password supplied: nothing to sell")
	}
	if proto != "ssh" && proto != "ftp" {
		return nil, fmt.Errorf("protocol must be ssh or ftp")
	}
	dst := w.marketFindDevice(host)
	if dst == nil {
		return nil, fmt.Errorf("no such host: %s", host)
	}
	if dst.FindUser(account) == nil {
		return nil, fmt.Errorf("no account %s on %s", account, dst.Hostname)
	}
	// reachability first: the probe below is a real login, so it must only
	// happen where a real login could — a NATed box with no forward refuses
	// the listing instead of absorbing a ghost attempt
	if _, _, msg := Dial(bazaar, w.marketFrontAddr(dst, port), port); msg != "connected" {
		return nil, fmt.Errorf("cannot verify %s@%s: %s from the bazaar (unreachable credentials cannot be listed)", account, dst.Hostname, msg)
	}
	if !AttackLogin(w, bazaar, dst, account, pass, proto, port) {
		return nil, fmt.Errorf("verification failed: that password does not work on %s@%s — no listing", account, dst.Hostname)
	}
	m.Seq++
	l := &Listing{ID: fmt.Sprintf("M-%d", m.Seq), Kind: "cred", Seller: seller,
		Price: price, Created: w.Sim, Host: dst.ID, Account: account, Pass: pass,
		Proto: proto, Port: port}
	_, l.Fee = marketFee(price)
	m.Listings = append(m.Listings, l)
	bazaar.Logf("info", "marketd", "listed %s: %s@%s (%s/%d) for %d cents by %s",
		l.ID, account, dst.Hostname, proto, port, price, seller)
	w.Record("market", seller, bazaar.SvcAddr(), m.BazaarID,
		fmt.Sprintf("listed credential %s@%s for %d cents", account, dst.Hostname, price), 1)
	w.AddEvent(m.BazaarID, "info", "market", "%s listed %s@%s", seller, account, dst.Hostname)
	return l, nil
}

// MarketListFile lists bytes already on the bazaar host. The hash is pinned
// at list time; a file that changes before sale delists instead of selling
// something the buyer never saw.
func (w *World) MarketListFile(seller, path string, price int64) (*Listing, error) {
	m := w.Market()
	bazaar := w.Devices[m.BazaarID]
	if bazaar == nil {
		return nil, fmt.Errorf("the bazaar is gone")
	}
	if price <= 0 {
		return nil, fmt.Errorf("a price must be positive")
	}
	data, ok := bazaar.FS.Read(path)
	if !ok {
		return nil, fmt.Errorf("no such file on the bazaar: %s (put it there first)", path)
	}
	if bazaar.FS.IsDir(path) {
		return nil, fmt.Errorf("%s is a directory, not goods", path)
	}
	sum := sha256.Sum256(data)
	m.Seq++
	l := &Listing{ID: fmt.Sprintf("M-%d", m.Seq), Kind: "file", Seller: seller,
		Price: price, Created: w.Sim, Path: path,
		Hash: fmt.Sprintf("%x", sum), Size: len(data)}
	_, l.Fee = marketFee(price)
	m.Listings = append(m.Listings, l)
	bazaar.Logf("info", "marketd", "listed %s: %s (%d bytes, sha256 %.12s) for %d cents by %s",
		l.ID, path, len(data), l.Hash, price, seller)
	w.Record("market", seller, bazaar.SvcAddr(), m.BazaarID,
		fmt.Sprintf("listed file %s for %d cents", path, price), 1)
	return l, nil
}

// MarketBuy is the atomic swap: re-verify, move money, deliver, record. A
// good that fails re-verification delists with no charge — the market does
// not sell what it no longer holds.
func (w *World) MarketBuy(buyer, id string) (*Listing, string, error) {
	m := w.Market()
	bazaar := w.Devices[m.BazaarID]
	if bazaar == nil {
		return nil, "", fmt.Errorf("the bazaar is gone")
	}
	var l *Listing
	for _, x := range m.Listings {
		if x.ID == id && !x.Sold {
			l = x
		}
	}
	if l == nil {
		return nil, "", fmt.Errorf("no live listing %s", id)
	}
	if l.Seller == buyer {
		return nil, "", fmt.Errorf("you cannot buy your own listing")
	}
	// full price up front: the swap moves money twice (seller, then fee),
	// and a buyer short of the total must be refused before anything moves
	if acc := w.Bank.Accts[buyer]; acc == nil || acc.Balance < l.Price {
		return nil, "", fmt.Errorf("insufficient funds: %s costs %d cents", l.ID, l.Price)
	}
	proceeds, fee := marketFee(l.Price)
	switch l.Kind {
	case "cred":
		dst := w.Devices[l.Host]
		if dst == nil {
			return nil, "", fmt.Errorf("the machine behind %s is gone — delisted", l.ID)
		}
		if _, _, msg := Dial(bazaar, w.marketFrontAddr(dst, l.Port), l.Port); msg != "connected" {
			w.marketDelist(m, l, "re-verify unreachable")
			return nil, "", fmt.Errorf("%s went dark before the sale: delisted, no charge", l.ID)
		}
		if !AttackLogin(w, bazaar, dst, l.Account, l.Pass, l.Proto, l.Port) {
			w.marketDelist(m, l, "password no longer works")
			return nil, "", fmt.Errorf("%s is stale (password rotated?): delisted, no charge", l.ID)
		}
		if err := w.Transfer(buyer, l.Seller, proceeds, "bazaar "+l.ID+" credential"); err != nil {
			return nil, "", err
		}
		if fee > 0 {
			if err := w.Transfer(buyer, marketOperator, fee, "bazaar "+l.ID+" fee"); err != nil {
				return nil, "", err
			}
		}
		l.Sold, l.Buyer, l.SoldAt = true, buyer, w.Sim
		m.Fees += fee
		secret := l.Pass
		bazaar.Logf("info", "marketd", "%s sold to %s (%d cents, fee %d)", l.ID, buyer, l.Price, fee)
		w.Record("market", buyer, bazaar.SvcAddr(), l.Host,
			fmt.Sprintf("bought credential %s@%s (%s) for %d cents", l.Account, dst.Hostname, l.ID, l.Price), 4)
		w.AddEvent(m.BazaarID, "warn", "market", "%s bought %s (%s@%s)", buyer, l.ID, l.Account, dst.Hostname)
		return l, secret, nil
	case "file":
		data, ok := bazaar.FS.Read(l.Path)
		if !ok {
			w.marketDelist(m, l, "file gone")
			return nil, "", fmt.Errorf("%s is gone from the bazaar: delisted, no charge", l.ID)
		}
		sum := sha256.Sum256(data)
		if fmt.Sprintf("%x", sum) != l.Hash {
			w.marketDelist(m, l, "hash mismatch")
			return nil, "", fmt.Errorf("%s changed since listing: delisted, no charge", l.ID)
		}
		if err := w.Transfer(buyer, l.Seller, proceeds, "bazaar "+l.ID+" file"); err != nil {
			return nil, "", err
		}
		if fee > 0 {
			if err := w.Transfer(buyer, marketOperator, fee, "bazaar "+l.ID+" fee"); err != nil {
				return nil, "", err
			}
		}
		// the drop opens: world-readable, so the buyer fetches it with the
		// same ftp verbs as everything else — and so can anyone else who
		// looks, which is why buying quietly matters
		if n, has := bazaar.FS.Get(l.Path); has {
			n.Mode = 0444
		}
		l.Sold, l.Buyer, l.SoldAt = true, buyer, w.Sim
		m.Fees += fee
		bazaar.Logf("info", "marketd", "%s sold to %s (%d cents, fee %d): %s is now readable",
			l.ID, buyer, l.Price, fee, l.Path)
		w.Record("market", buyer, bazaar.SvcAddr(), m.BazaarID,
			fmt.Sprintf("bought file %s (%s) for %d cents", l.Path, l.ID, l.Price), 3)
		return l, l.Path, nil
	}
	return nil, "", fmt.Errorf("unknown goods: %s", l.Kind)
}

// marketDelist removes a listing that failed re-verification, saying why on
// the record. Nothing is charged; the stale entry simply stops being for sale.
func (w *World) marketDelist(m *MarketState, l *Listing, why string) {
	keep := m.Listings[:0]
	for _, x := range m.Listings {
		if x != l {
			keep = append(keep, x)
		}
	}
	m.Listings = keep
	if bazaar := w.Devices[m.BazaarID]; bazaar != nil {
		bazaar.Logf("warn", "marketd", "delisted %s: %s", l.ID, why)
	}
	w.AddEvent(m.BazaarID, "info", "market", "%s delisted: %s", l.ID, why)
}

// MarketLive returns the listings still for sale, oldest first.
func (w *World) MarketLive() []*Listing {
	m := w.Market()
	var out []*Listing
	for _, l := range m.Listings {
		if !l.Sold {
			out = append(out, l)
		}
	}
	return out
}

// seedMarket builds the bazaar: one shady host on the public internet with a
// daemon, an anonymous drop box, and two opening listings. It runs with the
// other workstream seeds, after the NPC neighbour exists (one seeded listing
// is hers to lose).
func seedMarket(w *World) {
	bazaar := w.addDevice("bazaar", "bazaar", "infra", "", OSInfo{"Debian", "12", "6.1.0", "x86_64", "bash"},
		Hardware{"Bulk VPS", 2, 2400, 2048, 40960, 1000, false, false}, "")
	bazaar.Ifaces = append(bazaar.Ifaces, &Iface{Name: "eth1", IP: w.allocPublicFor("infra"),
		MAC: macFor("bazaar-wan"), Zone: "wan", Up: true, GW: "10.0.0.1"})
	w.IPMap[bazaar.Ifaces[len(bazaar.Ifaces)-1].IP] = bazaar.ID
	bazaar.Notes = "A dead-drop host on the public internet. No questions, 5%."
	mkUsers(bazaar, map[string]*User{
		"root": {Name: "root", UID: 0, Groups: []string{"root"}, Home: "/root", Shell: "/bin/bash"},
		"ftp":  {Name: "ftp", UID: 21, Groups: []string{"ftp"}, Home: "/srv/bazaar", Shell: "/usr/sbin/nologin"},
	})
	seedFS(bazaar, "infra")
	bazaar.FS.Write("/etc/motd", "Leave money. Take goods. No names.\n", 0644, "root", "root")
	bazaar.Services["marketd"] = &Service{Name: "marketd", Desc: "bazaar listings and escrow", Port: bazaarPort,
		Proto: "tcp", Scope: "any", State: "running", Handler: "marketd", Banner: "bazaar/1.0"}
	// the drop box: anonymous FTP, writable, the way every dead drop works.
	// Sellers put files here with the same ftp verbs as everything else, then
	// list the path; the market pins the hash and opens the file on sale.
	bazaar.Services["vsftpd"] = &Service{Name: "vsftpd", Desc: "FTP drop box", Port: 21, Proto: "tcp",
		Scope: "any", State: "running", Handler: "npc-ftp", Banner: "220 (vsFTPd 3.0.5)", Conf: "/etc/vsftpd.conf"}
	bazaar.FS.Write("/etc/vsftpd.conf",
		"# dead drops — do not touch\n"+
			"listen=YES\n"+
			"anonymous_enable=YES\n"+
			"anon_root=/srv/bazaar/drops\n"+
			"anon_upload_enable=YES\n"+
			"anon_mkdir_write_enable=YES\n"+
			"local_enable=YES\n"+
			"write_enable=YES\n", 0644, "root", "root")
	bazaar.FS.MkdirAll("/srv/bazaar/drops", 0777, "nobody", "nogroup")
	w.Records = append(w.Records, DNSRecord{Name: bazaarHost, IP: wanIP(bazaar)})

	w.Bazaar = &MarketState{BazaarID: bazaar.ID}
	// Opening listings, onboarded before the story starts (so no probe —
	// the operator met these sellers off-screen, the way every market does).
	// One is live ammunition with a shelf life: the neighbour's weak ftp
	// password, which her own hardening rotates the moment heat rises.
	if npc := w.Devices["npc-pc"]; npc != nil {
		if u := npc.FindUser("devops"); u != nil && u.Pass != "" {
			w.Bazaar.Seq++
			w.Bazaar.Listings = append(w.Bazaar.Listings, &Listing{
				ID: "M-1", Kind: "cred", Seller: "devops", Price: 5000,
				Created: w.Sim, Host: npc.ID, Account: "devops", Pass: u.Pass,
				Proto: "ftp", Port: 21,
			})
			_, w.Bazaar.Listings[0].Fee = marketFee(5000)
			bazaar.Logf("info", "marketd", "listed M-1: devops@%s (ftp/21) for 5000 cents by devops", npc.Hostname)
		}
	}
	// The other is flavour with a price tag: daemon42's client list, every
	// line of it true in this world (compare the BBS market board).
	clients := "温哥华 config, 2 sites — paid\nmira-9 dns retainer — paid\nolduser laptop refresh — pending\n"
	bazaar.FS.MkdirAll("/srv/bazaar/drops", 0755, "root", "root")
	bazaar.FS.Write("/srv/bazaar/drops/clients.txt", clients, 0600, "root", "root")
	if data, ok := bazaar.FS.Read("/srv/bazaar/drops/clients.txt"); ok {
		sum := sha256.Sum256(data)
		w.Bazaar.Seq++
		l := &Listing{ID: "M-2", Kind: "file", Seller: "daemon42", Price: 3000,
			Created: w.Sim, Path: "/srv/bazaar/drops/clients.txt",
			Hash: fmt.Sprintf("%x", sum), Size: len(data)}
		_, l.Fee = marketFee(3000)
		w.Bazaar.Listings = append(w.Bazaar.Listings, l)
		bazaar.Logf("info", "marketd", "listed M-2: /srv/bazaar/drops/clients.txt for 3000 cents by daemon42")
	}
}

// BazaarTick is the market's own clock: listings expire, and one NPC buyer
// with money and curiosity walks the board on a fixed schedule — never a
// dice roll, always the cheapest affordable listing first.
func (w *World) BazaarTick() {
	m := w.Bazaar
	if m == nil {
		return
	}
	// expiry: thirty sim-days on the board, then gone with a log line
	keep := m.Listings[:0]
	for _, l := range m.Listings {
		if !l.Sold && w.Sim.Sub(l.Created) > 30*24*time.Hour {
			if bazaar := w.Devices[m.BazaarID]; bazaar != nil {
				bazaar.Logf("info", "marketd", "expired %s (unsold for 30 days)", l.ID)
			}
			continue
		}
		keep = append(keep, l)
	}
	m.Listings = keep
	// the collector: every six sim-hours, mara buys the cheapest player
	// listing she can afford — she pokes at boxes, and this is her budget
	// for it. Same rules as any buyer: re-verify, pay, record.
	if w.TickCount%720 != 0 {
		return
	}
	var pick *Listing
	for _, l := range w.MarketLive() {
		if l.Seller == "mara" {
			continue
		}
		if pick == nil || l.Price < pick.Price {
			pick = l
		}
	}
	if pick == nil {
		return
	}
	if acc := w.Bank.Accts["mara"]; acc == nil || acc.Balance < pick.Price {
		return
	}
	if _, _, err := w.MarketBuy("mara", pick.ID); err != nil {
		return
	}
	if bazaar := w.Devices[m.BazaarID]; bazaar != nil {
		bazaar.Logf("info", "marketd", "mara bought %s off the board", pick.ID)
	}
}
