package core

import (
	"fmt"
	"regexp"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Organisations / clans (Phase 2) — a name, a roster and a real treasury
//
// An org is joint action with a ledger: members contribute to a treasury only
// money can fill, the owner alone spends it, and contracts posted against it
// escrow real funds — whoever completes the work collects from the hold, not
// from freshly minted payouts. Combined with the trophy verifier below, this
// is also the bounty board: post proof-of-intrusion work and pay whoever
// brings the tag home.
// ---------------------------------------------------------------------------

// Org is a crew: founder-owned, invite-only, with a bank treasury and holds
// escrowed per posted contract. Shape owned by this file.
type Org struct {
	Name     string
	Founder  string
	Members  map[string]string // player -> role (owner|member)
	Invites  map[string]bool   // player -> invited, consumes on join
	Treasury int64             // cents, in the org bank account
	Hold     map[string]int64  // job id -> escrowed cents
	Created  time.Time
}

var orgNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{2,15}$`)

// orgTreasury is the bank account behind an org's treasury.
func orgTreasury(name string) string { return "org:" + name }

// Orgs lists every organisation, oldest first.
func (w *World) Orgs() []*Org {
	var out []*Org
	for _, o := range w.orgMap() {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].Name < out[j].Name
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out
}

func (w *World) orgMap() map[string]*Org {
	if w.Clans == nil {
		w.Clans = map[string]*Org{}
	}
	return w.Clans
}

// CreateOrg founds an organisation: the founder is its first and owning
// member, and its treasury opens empty — money only arrives through
// contributions, never by creation.
func (w *World) CreateOrg(founder, name string) (*Org, error) {
	if !orgNameRe.MatchString(name) {
		return nil, fmt.Errorf("bad org name %q (lowercase letters, digits, hyphens, 3-16 chars)", name)
	}
	if _, taken := w.orgMap()[name]; taken {
		return nil, fmt.Errorf("org %s already exists", name)
	}
	if !w.isPerson(founder) {
		return nil, fmt.Errorf("no such player: %s", founder)
	}
	o := &Org{Name: name, Founder: founder, Members: map[string]string{founder: "owner"},
		Invites: map[string]bool{}, Hold: map[string]int64{}, Created: w.Sim}
	w.orgMap()[name] = o
	if w.Bank.Accts[orgTreasury(name)] == nil {
		w.Bank.Accts[orgTreasury(name)] = &Account{Owner: orgTreasury(name), Name: name + " treasury"}
	}
	w.AddEvent("world", "info", "org", "%s founded org %s", founder, name)
	return o, nil
}

// isPerson reports whether a name can hold crew membership: a player, or a
// known NPC handle. NPCs never run sessions, so their membership is a roster
// fact (and a treasury they cannot spend) rather than agency.
func (w *World) isPerson(name string) bool {
	if w.Players[name] != nil {
		return true
	}
	for _, n := range w.NPCNames {
		if n == name {
			return true
		}
	}
	return false
}

// OrgInvite vouches a player: owner-only, consumed by joining.
func (w *World) OrgInvite(by, org, who string) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if o.Members[by] != "owner" {
		return fmt.Errorf("only the owner of %s can invite", org)
	}
	if w.Players[who] == nil {
		return fmt.Errorf("no such player: %s", who)
	}
	if _, ok := o.Members[who]; ok {
		return fmt.Errorf("%s is already in %s", who, org)
	}
	o.Invites[who] = true
	w.AddEvent("world", "info", "org", "%s invited %s to %s", by, who, org)
	return nil
}

// OrgJoin takes up an invitation; leaving needs none.
func (w *World) OrgJoin(who, org string) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if _, ok := o.Members[who]; ok {
		return fmt.Errorf("%s is already in %s", who, org)
	}
	if !o.Invites[who] {
		return fmt.Errorf("%s has no invitation to %s", who, org)
	}
	delete(o.Invites, who)
	o.Members[who] = "member"
	w.AddEvent("world", "info", "org", "%s joined %s", who, org)
	return nil
}

// OrgLeave walks away; the owner cannot abandon a crew with members in it.
func (w *World) OrgLeave(who, org string) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if _, ok := o.Members[who]; !ok {
		return fmt.Errorf("%s is not in %s", who, org)
	}
	if o.Members[who] == "owner" && len(o.Members) > 1 {
		return fmt.Errorf("the owner cannot leave %s while members remain (kick them first)", org)
	}
	delete(o.Members, who)
	w.AddEvent("world", "info", "org", "%s left %s", who, org)
	return nil
}

// OrgKick removes a member: owner-only, never the owner themselves.
func (w *World) OrgKick(by, org, who string) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if o.Members[by] != "owner" {
		return fmt.Errorf("only the owner of %s can kick", org)
	}
	if o.Members[who] == "owner" {
		return fmt.Errorf("cannot kick the owner of %s", org)
	}
	if _, ok := o.Members[who]; !ok {
		return fmt.Errorf("%s is not in %s", who, org)
	}
	delete(o.Members, who)
	w.AddEvent("world", "info", "org", "%s kicked %s from %s", by, who, org)
	return nil
}

// OrgContribute moves personal money into the treasury. Any member can fund
// the crew; only the ledger decides what leaves it.
func (w *World) OrgContribute(who, org string, amount int64) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if _, ok := o.Members[who]; !ok {
		return fmt.Errorf("%s is not in %s", who, org)
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if err := w.Transfer(who, orgTreasury(org), amount, "org "+org+" contribution from "+who); err != nil {
		return err
	}
	o.Treasury += amount
	return nil
}

// OrgWithdraw takes treasury money out: owner-only, on the record.
func (w *World) OrgWithdraw(by, org string, amount int64) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if o.Members[by] != "owner" {
		return fmt.Errorf("only the owner of %s can withdraw", org)
	}
	if amount <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if o.Treasury < amount {
		return fmt.Errorf("treasury holds %d cents", o.Treasury)
	}
	if err := w.Transfer(orgTreasury(org), by, amount, "org "+org+" withdrawal to "+by); err != nil {
		return err
	}
	o.Treasury -= amount
	return nil
}

// seedOrgs plants the one crew that already exists: midnight, daemon42's
// outfit, with a treasury and an open proof-of-intrusion contract on the
// board. The target is reachable today (anonymous ftp to darkden), the tag
// file is not there yet, and removing it un-completes the work — the whole
// bounty loop, playable from seed.
func seedOrgs(w *World) {
	o, err := w.CreateOrg("daemon42", "midnight")
	if err != nil {
		return
	}
	o.Members["mira-9"] = "member"
	o.Treasury = 20000
	w.Bank.Accts[orgTreasury(o.Name)] = &Account{Owner: orgTreasury(o.Name),
		Name: "midnight treasury", Balance: 20000,
		Tx: []Tx{{At: w.Sim, Amount: 20000, Memo: "crew funds", Balance: 20000}}}
	_, _ = w.OrgPost("daemon42", "midnight", "tag the drop box", 10000,
		"trophy npc-pc /tmp/pwned",
		"leave your tag in /tmp/pwned on darkden (anonymous ftp reaches it), then collect")
}

// nextContractID allocates J-110 and up: contracts share the board with
// seeded jobs, so the numbering must never collide with them.
func (w *World) nextContractID() string {
	n := 110
	for _, j := range w.Jobs.List {
		var k int
		if _, err := fmt.Sscanf(j.ID, "J-%d", &k); err == nil && k >= n {
			n = k + 1
		}
	}
	return fmt.Sprintf("J-%d", n)
}

// OrgPost puts a contract on the board, escrowing the full pay from the
// treasury up front: whoever completes it collects from the hold, which is
// why a broke crew cannot post. Owner-only — spending shared money is the
// owner's signature.
func (w *World) OrgPost(by, org, title string, pay int64, verify, help string) (*Job, error) {
	o := w.orgMap()[org]
	if o == nil {
		return nil, fmt.Errorf("no such org: %s", org)
	}
	if o.Members[by] != "owner" {
		return nil, fmt.Errorf("only the owner of %s can post contracts", org)
	}
	if pay <= 0 {
		return nil, fmt.Errorf("pay must be positive")
	}
	if o.Treasury < pay {
		return nil, fmt.Errorf("treasury holds %d cents, contract needs %d", o.Treasury, pay)
	}
	if verify == "" {
		return nil, fmt.Errorf("a contract needs a verifier")
	}
	id := w.nextContractID()
	// escrow: the counter moves now, the bank moves at payout. The treasury
	// account keeps the funds meanwhile, so the hold is always covered and
	// the ledger (debit memo here, payout memo there) tells the whole story.
	o.Treasury -= pay
	o.Hold[id] = pay
	if acc := w.Bank.Accts[orgTreasury(org)]; acc != nil {
		acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: 0, Memo: fmt.Sprintf("org %s escrow %s (%d cents held)", org, id, pay), Balance: acc.Balance})
	}
	j := &Job{ID: id, Title: title, Client: org, Pay: pay, Tier: 2, Help: help,
		Verify: verify, Target: "org:" + org, Org: org}
	w.Jobs.List = append(w.Jobs.List, j)
	w.AddEvent("world", "info", "org", "%s posted %s for %s (%d cents escrowed)", by, id, org, pay)
	return j, nil
}

// OrgCancel pulls an unaccepted contract off the board and refunds the hold
// to the treasury. Taken work cannot be cancelled out from under its worker.
func (w *World) OrgCancel(by, org, id string) error {
	o := w.orgMap()[org]
	if o == nil {
		return fmt.Errorf("no such org: %s", org)
	}
	if o.Members[by] != "owner" {
		return fmt.Errorf("only the owner of %s can cancel contracts", org)
	}
	j := w.Job(id)
	if j == nil || j.Org != org {
		return fmt.Errorf("no contract %s on %s", id, org)
	}
	if j.Accepted != "" {
		return fmt.Errorf("%s is taken by %s", id, j.Accepted)
	}
	if hold, ok := o.Hold[id]; ok {
		o.Treasury += hold
		delete(o.Hold, id)
		if acc := w.Bank.Accts[orgTreasury(org)]; acc != nil {
			acc.Tx = append(acc.Tx, Tx{At: w.Sim, Amount: hold, Memo: "org " + org + " refund " + id, Balance: acc.Balance})
		}
	}
	j.Done = true // off the board without paying: cancelled, not completed
	j.Cancelled = true
	w.AddEvent("world", "info", "org", "%s cancelled %s (hold refunded)", by, id)
	return nil
}
