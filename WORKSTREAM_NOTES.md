# Workstream notes — scheduler (cron) + VM + TLS + mail/IMAP + BBS

Ownership: `internal/core/cron.go`, `internal/core/crond.go`,
`internal/shell/cron_cmds.go`, `tests/cron_test.go`.
No other file is edited by this workstream (and no git commands were run).

## The one integration hook, and why it does not need a shell.go edit

`World.CronTick()` lives in `core` and must run a real game command line, but
`internal/shell` imports `neohome/internal/core`, so `core` cannot import
`shell`. The dependency is inverted with a one-function registration, declared
in `core` (this workstream's file) and satisfied in `shell` (this workstream's
file) — so **no change to `internal/shell/shell.go` was needed**:

```go
// internal/core/cron.go
var CronExecFn func(w *World, d *Device, u *User, line string) (int, string) // rc + captured output
func SetCronExec(fn func(w *World, d *Device, u *User, line string) (int, string))

// internal/shell/cron_cmds.go
func init() { core.SetCronExec(execCronLine) }
```

`execCronLine` builds a real `Shell` with `NewShell`, runs the line through the
package's own unexported `execOne` (same builtins, same VFS permission checks,
same `>`/`>>` redirects, same `&&`/`;` grammar as a player's session) and
returns the numeric exit status. No host process, no goroutine, no real clock.

If a future refactor wants this without the registration indirection, the
equivalent hook inside `shell` would be one exported wrapper:

```go
func RunAsJob(w *core.World, d *core.Device, u *core.User, line string) (int, string)
```

Nothing else in the workstream depends on it.

## What the scheduler does

* `seedCron(w)` (called by `world_init.go`) registers a real cron daemon unit on
  every device — `crond` on BusyBox images, `cron` on Debian-family ones —
  starts the ones a real box of that type runs at boot (household PC, router,
  NAS, assistant node, NPC router), and plants three jobs in real spool files
  under `/var/spool/cron/crontabs/<user>`.
* `World.CronTick()` (called once per `World.Tick()`, i.e. once per 30
  simulated seconds) reloads the spools, notices daemons that have just come up,
  and fires every entry whose next minute `World.Sim` has passed. No wall-clock
  sleeping anywhere; `tests/cron_test.go` proves firing by calling `Tick()`.
* A job only fires while its device's daemon is `running`. `service cron stop`,
  `systemctl stop crond` and a crashed daemon therefore genuinely stop scheduled
  execution, and starting one re-arms from now (no backlog stampede).
* Every run appends to the device's `/var/log/syslog` (so `logread cron` /
  `journalctl -u cron` show the command, the exit code and the first line of its
  output) and to `/var/log/cron.log`, and world events get the first run of
  each job.
* `crontab -l | FILE | -r | -e [-u user]` operate on the real spool file;
  a bad schedule is refused with its line number and nothing is installed.
  `crond` starts/loads the scheduler and is honest on an image that ships none.

## Seeded jobs (all world-visible, all diagnosable)

| device | user | schedule | command | why it matters |
|---|---|---|---|---|
| `pc-alex` | alex | `0 * * * *` | `curl -s -o/home/alex/upstream-probe.log http://mirror.neohome.example/debian/Release` | FAILS while the planted DNS fault is active ("could not resolve host") and succeeds after the router is repaired — the scheduler hands the player the first clue |
| `nas-alex` | root | `0 3 * * *` | `curl -s -o/srv/data/nightly-mirror.log http://mirror.neohome.example/debian/Release` | "the nightly backup stopped" is a real, traceable failure |
| `asst-alex` | assistant | `*/30 * * * *` | `echo assistant-heartbeat >> /home/assistant/jobs.log` | a real file that really grows; visible with `assist tasks` |

## Two fidelity bugs found in other files — and fixed

Both were found by this workstream but live in files it does not own. They were
fixed independently in `2f53472` (the cron commit itself), so the entries are
kept here only as history — **do not re-fix them**:

1. `internal/shell/net.go` `httpFetch` only understood the attached `-oFILE`
   form; `curl -s -o /path URL` and `wget -O /path URL` mis-parsed the path as
   the URL. **Fixed**: the arg scan now handles the separated forms too — see
   `internal/shell/net.go:349-362` (`-o`/`-O`/`--output` take the NEXT arg;
   `-oFILE`/`-OFILE` stay attached).
2. `internal/shell/shell.go` `commandExists` gated BusyBox applets on
   `s.Dev.OS.Shell == "/bin/ash"`, but `OSInfo.Shell` is written as `"ash"`, so
   `busyboxHas` was dead. **Fixed**: the check now goes through
   `core.IsBusyboxShell(s.Dev.OS.Shell)` (`internal/shell/shell.go:334`), which
   accepts both spellings via the shared `bbSet` applet list.

## Not implemented on purpose

* `@reboot` / `@daily` and friends: refused by the parser with a clear error
  rather than half-supported. A spec the scheduler cannot honour is never armed.
* `-` (Vixie overlap guard) parses and stays armed: a job here completes inside
  the tick that fires it, so there is no previous run to overlap with.
* `MAILTO` / job mail: the game's "mail" is world state, so job output goes to
  the device log and `/var/log/cron.log` instead of inventing a mailbox.

## Status (updated 2026-10-03)

Verified against `aaa2990` (the state-aware-assistant + MCP tip):

* All three seeded jobs are exercised by `tests/cron_test.go`
  (`TestSeededCronJobsAreRealAndDiagnosable` and the `TestCron*` family), and
  the whole suite is green: `go test ./tests/` → 93 tests pass.
* The two fidelity bugs above are no longer open — both were fixed in
  `2f53472`. Re-check with: `grep -n 'IsBusyboxShell' internal/shell/shell.go`
  and `sed -n '349,364p' internal/shell/net.go`.
* Live chain: `bash tools/all_verify.sh` runs the 12 end-to-end scripts
  (live, ssh, wan, live_full, vm, power, dhcp, key, persistence, history, tmux,
  perm). Note both `all_verify.sh` and the individual scripts hardcode
  `cd /workspace/neohome` — fine here (the repo is cloned at that path), but a
  clone elsewhere needs the `cd` overridden first.

---

# TLS workstream — certificate authorities and enforced https

Ownership: `internal/core/tls.go`, `internal/shell/tls_cmds.go`,
`tests/tls_test.go`, `tools/tls_verify.sh`, plus the seed/persistence hooks in
`world.go` (the `World.TLS` pointer and `Service.TLSCert`),
`world_init.go` (`seedTLS` call), `resolver.go` (backward-compatible load) and
the client side of `internal/shell/net.go` (`fetchURL` scheme handling).

## What exists

* `World.TLS` (`internal/core/tls.go`) holds every CA and issued leaf as real
  crypto: x509 DER + PKCS#8 key bytes, validity judged against `World.Sim`,
  never the wall clock. `GenerateCA` / `IssueCert` / `Verify` are the world's
  own PKI; leaves carry the domain as CN and DNS SAN.
* Seed: `seedTLS` creates `neohome-root-ca`, drops its material on the
  assistant node (`/etc/ssl/{certs,private}`, key 0600), installs the CA into
  the trust stores of the household devices, and issues leaves for the four
  public https hosts (mirror, nova panel, bank, jobs). A web service with a
  leaf carries `Service.TLSCert`; `Dial` answers on 443 for such units — one
  unit, two sockets — so `systemctl stop nginx` takes https down with http.
* Client side (`fetchURL` + `World.Handshake`): `curl https://…` fails closed
  with the real failure modes — no certificate presented (35), hostname
  mismatch, untrusted issuer (60), expired/not-yet-valid — all judged against
  the simulated clock. Trust is files on the client device
  (`/etc/ssl/certs/*.pem` must contain the issuing CA's exact certificate), so
  deleting or planting trust material really changes what is trusted.
* `openssl` builtin (`internal/shell/tls_cmds.go`): `s_client -connect
  HOST[:PORT]` performs the same handshake and prints the chain and verify
  result; `x509 -in FILE -noout -subject|-issuer|-dates|-text` reads the PEM
  views on disk.

## Verified

* `tests/tls_test.go`: seed files, duplicate-CA refusal, issue/verify, clock
  gating (expired leaf refuses verify, expired CA refuses issuance),
  save/load round trip, and the live-path https behaviours (fail-closed on
  untrusted/stopped/expired, hostname check, openssl output).
* Live: `bash tools/tls_verify.sh` — chains the seeded DNS fault, the
  player-style router repair, working https to mirror + bank, the openssl
  chain, and the fail-closed case after `sudo rm` of the trust anchor.

## Not implemented on purpose

* Player-side CA creation (`openssl req`) — the world's PKI is seeded; giving
  players issuance is a follow-up once a storyline needs it.
* `Verify` checks a two-level chain (leaf → root); intermediate CAs are not
  modelled.

---

# IMAP workstream (WS-0.6) — the reading half of the mail system

Ownership: `internal/core/imap.go`, `internal/shell/mutt.go`,
`tests/imap_test.go`, plus the `imapd` seed in `world_init.go` and the
mboxo escape in `mail.go`'s `AppendToInbox`.

## The shape

* SMTP (WS-0.2–0.5) delivers; IMAP reads. A mailbox is still nothing but the
  mbox file in the VFS — `internal/core/imap.go` parses it on demand
  (`ParseMbox`) and authenticates against the target host's real account
  records (`World.IMAPSelect`): the account must exist on the mail host and
  the password must match, the same simulated check ssh and sudo perform.
  The failure never says which one was wrong.
* `imapd` is a real service on the two mail hosts (pc-alex, asst-alex), port
  143, scope lan, conf at `/etc/mail/imapd.conf` — stopping it really closes
  remote mailbox access, and its scope keeps it off the WAN.
* The client is `mutt` (`internal/shell/mutt.go`): `mutt -f
  imap://user[:pass]@host[:port]/INBOX [N]` opens a remote mailbox through
  DNS/Dial (a missing password prompts like ssh does); `mutt -f /var/mail/u
  [N]` reads a local mbox under plain file rules; bare `mutt` is the session
  account's own inbox. Successful and failed logins are logged on the mail
  host, so reads are evidence.
* mbox records now escape body lines starting with `From ` as `>From `
  (mboxo), so one message can never swallow the next; the parser undoes it.

## Verified

* `tests/imap_test.go`: service seeding, a cross-device read from the NAS
  with real credentials, body round-trip through the mboxo escape, failed
  auth (and its leak-free error), service-state gating, local mbox reads.

## Not implemented on purpose

* Folders beyond INBOX: the world has one mailbox per account; mutt says so
  rather than pretending.
* IMAP over TLS (port 993): the PKI exists, but no storyline needs it yet.

---

# BBS workstream (WS-0.7) — the board as a social system

Ownership: `internal/core/bbs.go`, `internal/shell/bbs_cmds.go`,
`tests/bbs_test.go`, plus the `bbs` device/bbsd service in `world_init.go`
and the `BBSTick` call in `engine.go`.

## Why it is not decoration (spec §19, §27, §33)

* The board is a real host (`bbs.neohome.example`, infra, its own public IP
  and zone record) running `bbsd` on port 2323, scope any — the port answers
  only while the service runs, and `scan` sees it like anything else.
* Every post is a real file on that box (`/srv/bbs/<board>/<NNNN>.txt`,
  0644 bbs:bbs): get a shell there and the raw threads are readable, delete
  a file and the post is gone. `bbs read` only formats what is on disk.
* The seeded threads are true of the world: the hacker board points at the
  consumer router's real legacy telnetd, warns that scans are logged (they
  are), the intel board's seeded thread explains the exact dnsmasq failure
  mode the planted fault produces.
* NPC posters are the IRC crowd (mira-9, daemon42, sysmods, mara-bot):
  a player post earns a keyword-driven, state-aware reply within a few
  ticks — while the DNS fault is active the answer names the broken
  resolv-file; after the repair it does not. Ambient posts land every 80
  ticks and are also fault-aware (the mirror-curl cron clue appears on the
  board while the fault lasts).
* Market threads reference real rails (coins, bank transfer, NPC mail), so
  交易 has somewhere to go.

## Client surface

`bbs [boards] | bbs read <board> [N] | bbs post <board> <subject>` — the
client resolves bbs.neohome.example, dials 2323 through the normal stack
and fails honestly (DNS fault → resolution error; stopped bbsd → refusal).

## Verified

* `tests/bbs_test.go`: seeding (service, conf, files, zone record), network
  reads with service-state gating, player post → file + syslog + NPC reply
  (fault-aware and healthy variants), ambient fault-aware post, save/load
  round trip with post numbering carried over.

## Not implemented on purpose

* Login/registration: reading and posting is open, as on many small boards;
  accounts per board user are a follow-up if a storyline needs private mail.
* Reply threading (Reply-To chains): posts are flat per board; quoting via
  "re: <subject>" is the convention.
