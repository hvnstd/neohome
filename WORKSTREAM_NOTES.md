# Workstream notes

One section per workstream, oldest first. Each section owns its files; check
ownership there before touching cross-cutting files (`world.go` pointers,
`engine.go` tick calls, `world_init.go` seeds, `resolver.go` backcompat).

## Index (status 2026-10-06, tip WS-1.9)

| Workstream | Commit | What it added | Where |
|---|---|---|---|
| cron + VM | `8ef6438`/`2f53472` | world scheduler, daemon-gated jobs, VMs | first section below |
| mail (WS-0.2–0.5) | `d2ba08c`/`38350e6` | smtpd daemon, routed delivery, job mail | (see `internal/core/mail.go`) |
| TLS + https | `0cca5f2` | real CAs, issued certs, enforced https, openssl | §"TLS workstream" |
| IMAP (WS-0.6) | `75b7900` | the reading half of mail; mutt | §"IMAP workstream" |
| BBS (WS-0.7) | `3c1ab3e` | the community board as a social system | §"BBS workstream" |
| git (WS-0.8) | `e957d12` | loose-object repositories over https | §"Git workstream" |
| sftp/scp (WS-0.9) | `5a2bdf8` | real transfers, stubs removed — spec §19 complete | §"SFTP workstream" |
| IoT (WS-1.0) | `69c6adf` | camera recordings, smart lock | §"IoT workstream" |
| SMB (WS-1.1) | `9e8835c` | real shares, mount-aware redirects | §"SMB workstream" |
| phone (WS-1.2) | `1363d10` | pocket computer, cellular SMS | §"Phone workstream" |
| usb (WS-1.3) | `160b0a7` | the air gap as a tool | §"USB workstream" |
| live-verify sweep | `ea8eb1d` | home dirs for every account; 13 scripts green | §"Verification status" |
| LAN plan (hardcode removal) | this commit | one owner for household addresses; `AllocLANStatic` replaces the MCP const; `ValidateLAN` panics at boot on duplicates/pool-collisions; the router's dhcp-range renders from the constants | `internal/core/addr.go`, `tests/lan_test.go` |
| FTP + reachability (WS-1.4) | this commit | real FTP sessions over the wire; the seeded drop box is reachable, attackable and defended; a port-forward is now a hole that really forwards | §"FTP workstream (WS-1.4)" |
| coupling patch (WS-1.5) | this commit | `irc` goes through the real ircd service and the resolver; the assistant node is reachable by key, as §24 intends, and `ssh host command` exists | §"Coupling patch (WS-1.5)" |
| package ecosystem (WS-1.6) | this commit | five real package managers on five distributions, repositories as signed files served over HTTP by real hosts, a mirror that syncs on the world's own schedule, dependency-aware install and honest removal, and the third-party supply-chain decision | §"Package ecosystem (WS-1.6)" |
| household network (WS-1.7) | `c130af8` | exposure *is* configuration: `uci` stages then commits, `iptables` on a host, real NAT and filtering in the packet path, UPnP with leases, a DMZ, and the router's own management surface | §"Household network (WS-1.7)" |
| IP system (WS-1.8) | this commit | §13 in full: public/private/CGNAT/ULA/link-local/dynamic/shared/virtual addresses, IPv6 for every device, v6 publication by rule, per-AS `/48`s, and the attribution layer that never names a person | §"IP system (WS-1.8)" |
| VPS lifecycle (WS-1.9) | this commit | §12's control plane: regions that are real networks, power, reboot, reinstall, resize, disks, snapshots, console, rDNS — every one an operation on the machine itself | §"VPS lifecycle (WS-1.9)" |
| 家庭设备与物理层 (WS-1.10) | this commit | §15: the switch as real ports and real PoE, the laptop's lid and battery, the printer's own queue, the backup that needs the NAS | §"家庭设备与物理层 (WS-1.10)" |
| 系统状态 / 资源管理 (WS-1.11) | this commit | §17: CPU sharing, RAM → swap → OOM, a full disk that really fails writes, throughput that decides how long bulk work takes, a finite process table | §"系统状态 / 资源管理 (WS-1.11)" |
| 防守和安全软件 (WS-1.12) | this commit | §33: fail2ban, suricata, aide, clamav, monit, auditd, central logs, restic backup, rkhunter host monitoring, a real firewall posture — every one installed software, configured by a file, acting on real state, and the shell's printf/quoting made honest so a config file can be edited with it | §"防守和安全软件 (WS-1.12)" |
| passwd + 磁盘磨损 (WS-1.14 cont.) | this commit | §36's two open items closed: `passwd` makes the weak credential a choice, and wear-driven disk death runs on power-on/lifetime-writes/overload thresholds with `smartctl` to read it and `fsck` to buy time | §"passwd 与磁盘磨损 (WS-1.14 continued)" |
| 缺口 6:四小件 | this commit | sftp -r 双向树、bbs mail/inbox、私信0600、git 分支+FF合并、imaps:993 证书握手 | §"缺口 6" |
| 缺口 1:身份+家 | this commit | useradd/userdel/usermod/groupadd/groups/chpasswd + /etc/group + GID 规则 + Household 一等实体 | §"缺口 1" |
| P2 剩余:多人/组织/PvP | this commit | Citizens with vouched PCs and bank transfers, orgs with treasuries and escrowed trophy contracts, async PvP proven across two citizens | §"P2 剩余" |
| 复杂任务框架 (Phase 2 + §44) | this commit | Staged missions: ordered Verify predicates with per-stage pay, Requires gates, assistant stage cycles through the same payStage, J-108/J-109 seeded | §"复杂任务框架" |
| 黑市 (Phase 2) | this commit | Phase 2 opens with the only piece that chains existing systems: `bazaar.neohome.example` with verified credential listings, hash-pinned dead drops, atomic swaps, a 5% fee and `market` evidence | §"黑市 (Phase 2)" |
| P1 补完 | this commit | Phase 1's three functional gaps: `vm snapshot/snapshots/restore` on hypervisor guests (plus the save-with-a-guest crash), phone battery wired into `Powered()` with cross-machine `phone charge` as the rescue, and cron skipping dark machines | §"P1 补完" |

Verification status at tip: full `go test ./...` green (~182 tests, 29 files),
`go vet` clean, `gofmt -l` empty across the tree (the pre-existing debt in
`git.go`, `iot.go`, `tls.go`, `sftp_cmds.go` and `world_cmds.go` was paid off
with WS-1.7),
`bash tools/all_verify.sh` — all 14 scripts exit ok, suspicious counts zero
except `live_verify`'s single scripted-fault dnsmasq log (expected).
`tools/ftp_verify.sh` is the one script that finds its own checkout and Go
toolchain, so it also runs from a clone outside `/workspace`.

Design debt, deliberately not built (each has a rationale in its section):
player-side CA issuance (`openssl req`), IMAPS :993, BBS private mail, git
branches/merge, sftp recursion, the §41 air-gapped vault (the USB stick is
its bridge), battery runtime in `Device.Powered()` for phones, PoE switch.

---

# Workstream notes — scheduler (cron) + VM

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

## Status (updated 2026-10-05)

Verified against `ea8eb1d` (the current tip):

* All three seeded jobs are exercised by `tests/cron_test.go`
  (`TestSeededCronJobsAreRealAndDiagnosable` and the `TestCron*` family), and
  the whole suite is green (`go test ./tests/` — 146 tests pass at this tip).
* The two fidelity bugs above are no longer open — both were fixed in
  `2f53472`. Re-check with: `grep -n 'IsBusyboxShell' internal/shell/shell.go`
  and `sed -n '349,364p' internal/shell/net.go`.
* Live chain: `bash tools/all_verify.sh` runs the 13 end-to-end scripts
  (live, ssh, wan, live_full, vm, power, dhcp, key, persistence, history,
  tmux, perm, tls). Note both `all_verify.sh` and the individual scripts
  hardcode `cd /workspace/neohome` — fine here (the repo is cloned at that
  path), but a clone elsewhere needs the `cd` overridden first.

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

---

# Git workstream (WS-0.8) — repositories as real world state

Ownership: `internal/core/git.go`, `internal/shell/git_cmds.go`,
`tests/git_test.go`, plus the `git` device/service/account seeds in
`world_init.go` and the `git.neohome.example` entry in `tls.go`'s
httpsHosts list.

## The model

* A repository is a directory of loose objects — blobs, trees and commits
  named by their SHA-1 — plus a HEAD, on the server under
  `/srv/git/<name>.git` (owned by the `git` daemon user) and locally under
  `<dir>/.git`. Nothing about a repo is stored anywhere else: get a shell
  on the server and the objects are readable; delete one and the history
  is really damaged.
* Transport is dumb-http over the git server's nginx (`http-git` handler,
  port 80, and 443 with a household-CA certificate — seedTLS issues it via
  the shared httpsHosts list, so `git clone https://git.neohome.example/...`
  performs the same handshake curl does). Every gate applies: DNS, route,
  power, service state, TLS.
* The client (`git clone|status|log|commit|pull|push`) works on the session
  CWD, walking up to find the `.git` directory like real git. `commit` is
  whole-tree (`commit -a` semantics; there is no index). `push` is an
  authenticated write against the git server's own account records —
  failures are logged on the server — and both directions are
  fast-forward-only: diverged histories are refused with "fetch first" /
  "diverged", never silently merged.
* Seeded repos: `neohome-scripts` (backup + mirror probe, whose history
  mentions the same syslog/backup story the cron jobs live in) and
  `dotfiles`. Two commits each, real objects, real authors.

## Verified

* `tests/git_test.go`: server seeding (service, TLS cert, object stores,
  zone record), https clone with checkout and history, commit → push →
  fresh clone sees the change, push auth (wrong password refused and
  logged, server HEAD unmoved), pull fast-forward + dirty-tree refusal +
  diverged refusal, status honesty outside a repo.

## Not implemented on purpose

* Branches, merges, rebase, the index/staging area, `git init`: one `main`
  line per repository, whole-tree commits. A second branch is the moment
  merge semantics must exist, and that is not needed by any storyline yet.
* SSH transport: the dumb-http path with TLS covers the story; a git
  protocol daemon would be a second listener for no narrative gain.

---

# SFTP workstream (WS-0.9) — closing spec §19's communication list

Ownership: `internal/core/sftp.go`, `internal/shell/sftp_cmds.go`,
`tests/sftp_test.go`, plus the removal of the old stubs from
`internal/shell/remote.go`.

## What replaced the stub

* `sftp` used to say "use scp for file transfer in this build", and `scp`
  was literally `cmdCp` — a `scp pc-file nas-file` silently wrote a local
  file. Both are real now. This was the last item of spec §19's required
  communication systems (DNS, WHOIS, HTTP, HTTPS, SMTP, IMAP, IRC, BBS,
  FTP, SFTP, Git, Job Board, Banking, Package Repository).
* `core/sftp.go` holds the transfer primitives, two devices and two
  accounts at a time: a fetch reads the remote file *as the remote account*
  (denied reads are logged on the remote) and writes locally *as the
  session account* through WriteChecked; a put mirrors it. Nothing can be
  fetched that the remote account cannot read, and nothing can land where
  the destination account cannot write.
* `sftp [user@]host` is a real interactive session: password prompt, then
  ls/lls/cd/pwd/lpwd/get/put/quit over the session's stdin. `scp` gained
  its real `[user@]host:path` syntax (local-to-local still routes to cp;
  two remote endpoints are refused).
* Authentication is the exact cmdSsh model, shared through `sshLogin`:
  unknown user and wrong password are indistinguishable, failures feed
  fail2ban, `PasswordAuthentication no` is honored, and sessions plus
  transfers are logged on the server.

## Verified

* `tests/sftp_test.go`: a full session (auth, remote listing, fetch with
  byte-identical result, upload owned by the remote account, server-side
  logs), auth refusals feeding fail2ban until the ban trips, honest
  permission failures (/etc/shadow, /root, missing files), scp both
  directions plus the local-cp and two-remote refusals, and sshd service
  gating.

## Not implemented on purpose

* sftp directory recursion (-r), permissions/uid commands, and resumable
  transfers: no storyline needs them; what exists covers the spec's
  "真提供远程 Shell / 文件传输" honestly.

---

# IoT workstream (WS-1.0) — the physical layer's first citizens

Ownership: `internal/core/iot.go`, `internal/shell/iot_cmds.go`,
`tests/iot_test.go`, plus the camera/lock device seeds in `world_init.go`,
the `iot` profile in devseed's router-hosts list, the `IoTTick` call in
`engine.go`, the `World.IoT` pointer, and the `AddEvent` lines added next
to existing target-side logs (scan in `world_cmds.go`).

## What exists

* `cam-alex` ("cam-front", 10.77.1.40) and `lock-alex` ("lock-front",
  10.77.1.41): real LAN devices with real services — rtsp on 554, lockd on
  8899, both LAN-scoped — and the honest IoT default `root`/`admin`.
* The camera records spec §42's "视频 / 证据状态" for real: each tick it
  drains the world event stream for alert-level events on household
  devices and writes a clip file to the NAS's `/srv/recordings/` (§15:
  Camera → NAS). The tape contains only things that really happened:
  port scans, denied fetches, wrong lock PINs, door actuations. The last
  100 clips are kept.
* Every dependency is enforced: rtsp stopped → nothing is recorded; NAS
  dark → clips are dropped and the camera logs "recordings dropped" every
  20 ticks even without events. `camera list|view N` dials the camera and
  reads the NAS's real files.
* The lock: state (locked/battery/PIN/wrong-attempts) on `World.IoT`, PIN
  inside `/etc/lockd.conf` (0600 root on the device — stealing it is a
  real objective). Owner sessions actuate without a code (the phone-app
  model); everyone else needs the PIN; three wrong codes buy a 40-tick
  lockout and leave evidence (device log, world event, heat via `Record`).
  A dead battery refuses actuation but not a physical battery swap.

## Verified

* `tests/iot_test.go`: device/service/credential/PIN seeding, scan → clip
  with the real event text, info-level silence, rtsp/NAS dependency
  enforcement including the drop notice, owner/PIN/lockout/battery lock
  flows with evidence on every wrong attempt.

## Not implemented on purpose

* A PoE switch between the camera and the router (§15's example chain):
  the dependency that matters (camera → NAS storage, both → router for
  reachability) is enforced; a switch device adds nothing yet.
* Video: the world is text; a clip is a structured, honest rendering of
  the events it recorded.

---

# SMB workstream (WS-1.1) — the Windows half of file sharing

Ownership: `internal/core/smb.go`, `internal/shell/smb_cmds.go`,
`tests/smb_test.go`, plus the smbd service and smb.conf on the NAS
(`world_init.go`/`devseed.go`), the rewritten `cmdMount`, and the mount-
aware redirect fix in `shell.go`.

## What was wrong before

* `cmdMount` already had a smb/cifs branch — but no smbd existed anywhere,
  so it was a dead branch, and a "successful" mount would have been an
  unauthenticated one with no share semantics at all (`//host/share` was
  not even parseable).
* Shell redirects (`>`, `>>`) bypassed the mounts entirely: `echo x >>
  /mnt/data/f` silently wrote a *local* file instead of the server's.
  Redirects now resolve through `ResolveVFS` like every other write, with
  the local path keeping the guest disk-full gate.

## What exists now

* The NAS runs `smbd` on 445 (LAN), and its shares are exactly what
  `/etc/samba/smb.conf` says — parsed on demand by `core.SMBShares`, never
  shadowed: `[data]` (guest, the same content NFS exports) and `[media]`
  (`guest ok = no`, `valid users = alex`), backed by real files.
* `mount -t cifs //nas/share /mnt/x [-o user=NAME]`: the share must exist
  in the server's configuration; non-guest shares prompt for a password
  and authenticate against the server's account records with the share's
  valid-users ACL on top (root is a real NAS account and is still refused
  on the media share). After the mount, the existing machinery does the
  work: ls/cat/cp/redirects resolve through the mount, the smbd state is
  re-checked on every access (stop the daemon → "Stale file handle"), and
  writes land on the NAS through the server's own permission checks.
* `smbclient -L HOST` lists what the server really shares — the recon
  step before the mount.

## Verified

* `tests/smb_test.go`: share parsing, guest mount with real reads/writes
  through the mount, auth and ACL refusals (wrong password, foreign
  account, valid-users), unknown share, smbclient listing, service-state
  gating including the stale-file-handle window.

## Not implemented on purpose

* Per-file operations under the mount act as the session user against the
  server's filesystem — the same simplification the NFS mounts already
  make. Real SMB would pin the mount credentials to every file op; that
  needs the mount user threaded through ResolveVFS and its callers.
* SMB printer shares, DFS, and signing/sealing: no storyline needs them.

---

# Phone workstream (WS-1.2) — the pocket computer

Ownership: `internal/core/sms.go`, `internal/shell/sms_cmds.go`,
`tests/sms_test.go`, plus the phone devices and their profile in
`world_init.go`/`devseed.go`, the `SMSTick` call in `engine.go`, the
`World.SMS` pointer, and the deposit-receipt call in `work.go`'s PayJob.

## The design, and what exists

* Spec §15 lists Phone as a household device; §209 says a phone is "just
  another default profile". So `phone-alex` (and mara's `phone-mara`) are
  real devices: battery hardware, a pocket terminal (`sshd`, Termux-style —
  `ssh alex@phone`), contacts in `~/.contacts`, messages as files in the
  phone's own `/var/spool/sms/`.
* SMS delivery is deliberately NOT a network dial: it rides the cellular
  radio, so it keeps working with the router dark — the one honest
  property that distinguishes it from every other channel in the world.
  The gates that remain are the phone's own battery and the recipient's.
* `sms list|read N|send WHO TEXT` works only on a phone. NPC owners reply
  immediately, keyword-driven, the same convention as IRC and the BBS;
  mara's answers are in character and state-aware.
* Banks text on deposits: `PayJob` sends a receipt to the phone on file
  (deposits only, as real banks do; no phone, no SMS).
* Batteries drain one percent per 480 ticks; 15% warns; 0% really powers
  the phone off — sshd stops, messages bounce ("phone switched off") —
  until `phone charge` (the dock) restores it.

## Verified

* `tests/sms_test.go`: device seeding (contacts, spool, battery hardware),
  send/receive with a real reply, unknown contact/number refusals,
  cellular-outlives-the-router, the deposit receipt end-to-end through
  `PayJob` on job J-101, and the battery/charge lifecycle including the
  dead-phone send/receive refusals.

## Not implemented on purpose

* The phone survives the house losing power for SMS, but `ssh phone`
  during a blackout still fails the Dial power gate — the power model has
  no per-device battery runtime yet (the BMC's UPS is the only precedent).
  Wiring `HW.Battery` into `Device.Powered()` is a power-model change that
  belongs to its own workstream.
* MMS, calls, app stores: the world is text.

---

# USB workstream (WS-1.3) — the air gap, as a tool

Ownership: `internal/core/usb.go`, `internal/shell/usb_cmds.go`,
`tests/usb_test.go`, plus the stick device in `world_init.go`, the
`World.USB` pointer, the vfat branches in `ResolveVFS` and `cmdMount`.

## What exists (as designed, with one correction)

* The stick is a real device (`usb-alex`, profile `usb`) with a filesystem
  and **no network interfaces** — unattached it exists in the world and no
  machine sees it. It starts in a drawer with its old contents intact.
* `usb plug usb-alex` (from any session) attaches it to that machine and
  creates a real, self-describing device node `/dev/sda1` whose content
  names the backing stick. The act lands in the kernel log and the world
  event stream — the front-door camera records a stick being plugged in.
* `mount -t vfat /dev/sda1 /mnt/usb` reads the node, resolves the stick,
  and mounts its filesystem through the standard Mount machinery; every
  access re-checks the attachment, so `usb unplug` while mounted yields an
  honest "Stale file handle", and the stick keeps every byte.
* The physical rules hold: a stick is in one machine at a time (plugging
  it elsewhere is refused until it is unplugged there), one port per
  machine, and the port is reusable after eject.
* Correction to the design note: a fresh VFS root is created root-owned,
  which would have made every stick read-only; since FAT32 has no
  ownership model, `seedUSB` hands the filesystem root to the stick's
  owner. That is the honest representation of a FAT stick.

## Verified

* `tests/usb_test.go`: stick seeding (no interfaces, unattached, contents),
  the full journey — plug, node file, kernel log, mount, read, write,
  unplug-while-mounted staleness, carry to the NAS with the write intact,
  two-machine and one-port refusals — and save/load persistence of both
  the attachment and the files.

## Not implemented on purpose

* The air-gapped vault itself (§41): the spec marks Offline Storage as a
  late-game asset; the stick is its bridge and comes first.
* Hubs, multiple sticks per host, write-protect switches: one port, one
  stick covers every story the world currently tells.

---

# FTP workstream (WS-1.4) — the last of §19, and the box it needed to be real

Ownership: `internal/core/ftp.go`, `internal/shell/ftp_cmds.go`,
`tests/ftp_test.go`, `tools/ftp_verify.sh`, plus seed edits in `devseed.go`
(the neighbour's configuration and the leaked export), `world_init.go` (the
`ftp` account), `pkgs.go` (what `apt install vsftpd` really writes), `vuln.go`
(the two FTP vulns' preconditions and the file they read), `world_cmds.go`
(`applyEffect`'s FTP branches, `recon`, `scan`, `exploit`), `net.go`
(port-forward reachability), `world.go` (`User.CheckPassword`), `bbs.go` (one
hacker-board thread), and the `help` text.

## Why this workstream existed

Spec §19 lists FTP among the systems that must really work ("FTP / SFTP：真传
文件"), and the world already *claimed* to have it in three places that did not
add up: the seeded neighbour runs `vsftpd` behind a port-forward to the
internet, the BBS and the vuln help told players to use `ftp -A host`, and no
`ftp` command existed at all. The two vulns then applied their effects by
writing into the target's VFS and reading the account records directly — an
"exploit" that never touched a service, a port or a permission. Three separate
lies, one workstream.

## What exists now

* `internal/core/ftp.go` owns the protocol: `FTPConfOf` parses the server's
  real `/etc/vsftpd.conf` on demand (never cached anywhere, so editing the file
  changes the next command), `FTPDaemon` finds the unit, `FTPLogin`
  authenticates against the real account records or the anonymous policy, and
  `FTPSession` performs LIST/CWD/RETR/STOR/MKD/DELE/SIZE as the *session
  account* on the *server's* filesystem. Anonymous sessions are chrooted to
  `anon_root` (or the `ftp` account's home), local sessions see `/` unless
  `local_root` says otherwise, and every refusal is logged on the target with
  its reason ("anon_upload_enable=NO") so the player has something to find.
  `Closed()` answers 421 once the daemon stops, so a session cannot outlive its
  service.
* `internal/shell/ftp_cmds.go` is the client: `ftp [-A] [user@]host[:port]`
  then `ls/dir`, `cd`, `pwd`, `get`, `put`, `mkdir`, `delete`, `size`, `user`,
  `open`, `close`, `lcd`, `bye`. It prints the real dialogue (220/331/230,
  150/226 around each transfer, 5xx on refusal) because that dialogue is the
  state of the connection.
* The seeded arc is now true end to end: repair the DNS fault → `whois` your
  own WAN address for the ISP's range → `scan` it and find `21/tcp open
  vsftpd` → `recon` (which follows the forward and names the machine behind it)
  → `ftp -A` → read `/home/devops/deploy/notes.md` → `get
  /home/devops/backup/accounts-2024.csv` → `user mara hunter2` → read her
  files. `exploit` performs the same steps through the same primitives, so the
  shortcut and the manual path can never disagree.

## Reachability bugs found here (all fixed, all with tests)

1. `Dial` applied the router's `matchFwd` and then judged the **pre-DNAT**
   machine: a dark PC behind a powered router still accepted connections, and
   a home PC's default-deny WAN policy filtered every forwarded port — so no
   port-forward in the world could ever work. The connection is now judged on
   the machine that really answers (power, firewall, scope), and a forward is a
   hole the owner opened.
2. `scan` dialled `FirstLANIP()` even when the matched address was a WAN
   address, so scanning a public range reported nothing; it now dials the
   address that matched.
3. `recon`/`exploit` never followed a forward: `exploit <router-ip> …` looked
   for the vuln on the router. `core.ForwardTarget` (plus the recon/exploit
   changes) puts the attack on the host that answers, and recon lists
   `21/tcp -> forwarded to darkden (10.88.1.11)`.
4. `ssh`, `sftp`/`scp` and `telnet` compared passwords with a bare
   `pass != u.Pass`, so an account with no stored password (a service account,
   or root on the neighbour's PC) authenticated when the player pressed enter.
   All credential checks now go through `User.CheckPassword`, the one rule the
   IMAP/SMB/git paths already had.
5. `Dial` logged every accepted connection as "via ssh-session". One honest
   line per connection now.

## Verified

* `tests/ftp_test.go`: seeding (config, account, drop, export), config-driven
  behaviour (anonymous off / upload off / mode bits / overwritten 0600 files),
  a full anonymous session over the port-forward with a byte-identical
  download and the server's own log lines, an upload that lands owned by `ftp`
  and raises an alert-level world event, auth against real accounts with
  fail2ban-style heat and leak-free refusals, the gates (unresolvable name,
  stopped daemon, closed forward, dark host, mid-session 421), the player's own
  packaged daemon (secure default → configured drop → confined anonymous
  root), the exploit chain over the wire (including failure once the forward is
  closed) and a save/load round trip.
* Live: `bash tools/ftp_verify.sh` — 11 steps, exit 0, no suspicious lines: the
  scripted DNS failure, the player-style repair, the whois→scan→recon chain,
  the anonymous read, the credential export verified by a real login, mara's
  account used through the daemon, the evidence trail, and the finale where
  heat 13 makes her close the port-forward and the same address then times out.

## Not implemented on purpose

* `listen_port`: a unit's port is owned by the service record that `ss`, `scan`,
  firewall rules and port-forwards all agree on. Honouring a second number
  would give one fact two owners, so the directive is parsed nowhere and the
  unit's port wins; noted here rather than half-implemented.
* ASCII/binary `TYPE` translation: nothing in the world observes the wire, so
  both modes are byte-exact on disk and a mode toggle would be decoration.
* FTPS (TLS on the data channel), passive port ranges (the control connection's
  gate is the model), `mget`/`mput` wildcards, resumable transfers, and
  `chroot_local_user`: no storyline needs them yet.
* The BBS thread added to the `hacker` board is one hint about the *method*
  (an ISP's customer range), not a walkthrough; it carries no IP, because those
  numbers have an owner.

---

# Coupling patch (WS-1.5) — three places where a service was decoration

Ownership: `internal/shell/irc_cmds.go` (new: the IRC client), `remote.go`
(ssh/telnet session handling), `internal/core/agents.go` (assistant key trust +
its access seed), `tests/irc_test.go` (new), one seed line in `world_init.go`.

Found while auditing the spec against the code, not by a failing test — which
is why each one gets a test now.

## 1. IRC was the one communication system with no service behind it

§19 lists IRC beside BBS, mail and FTP, and the other three really do go
through their daemons (the BBS client dials `bbsd`, mail needs `smtpd`, FTP
needs `vsftpd`). The `irc` client read `World.Chat` directly: the channel
worked with `ircd` stopped, with the box unpowered, and during the scripted DNS
fault. The data was real; the system was not.

`internal/shell/irc_cmds.go` now owns the client, and every use of it resolves
`irc.neohome.example`, dials 6667 and lets `core.Dial` decide — the same gate
the BBS uses. Consequences are the intended ones: during the DNS fault the
channel reports the resolution failure (and comes back when the resolver is
repaired), and stopping `ircd` refuses the connection until it is started
again. The message store, the NPC replies and their grounding in world state
are unchanged.

## 2. SSH into the assistant node was impossible, and its absence hid a panic

§24 says the player can SSH into the assistant's environment. The seed was
half-there: `/home/assistant/.ssh/authorized_keys` trusts the owner's key, but
the node had no `/etc/ssh/sshd_config`, so the client took the password path —
and the password (`assist-pass`) is not the player's to know. The key path in
`remote.go` only triggers when the target's config says
`PasswordAuthentication no`, so it never ran.

`SeedAssistantAccess` writes exactly the directive the world enforces, and
`AssistantKeyTrusted` was tightened from "any device of any player who has an
assistant" to "the session's device belongs to the owner of *this* assistant":
the old rule made the assistant node's key a skeleton key for every session in
the world, including a stranger's box. The test asserts the boundary as well as
the owner's access.

## 3. `ssh host command` did not exist, and the interactive path panicked without a terminal

`tests/wan_test.go` already called `ssh deploy@192.0.2.1 hostname` — the extra
argument was silently ignored and the test only passed because that address
never got as far as a nested shell. On a session with no input stream,
`cmdSsh` called `RunLoop(nil)` and panicked (`bufio.NewReader(nil)`), which is
what turned up when the assistant test first ran headless.

`openRemoteSession` now implements both real shapes: with a command it runs it
on the target and returns its status (so scripts, cron and tests can use remote
machines honestly), and without one it hands the terminal over — sharing the
session's buffered reader instead of wrapping it in a second `bufio.Reader`,
which would have starved on bytes the outer reader had already buffered. The
same helper drives telnet, which had the identical nil-reader crash.

## Verified

* `tests/irc_test.go`: the DNS fault really blocks the channel and the repair
  restores it; stopping `ircd` refuses honestly and starting it recovers;
  saying something mutates the log and draws a state-grounded NPC answer;
  the owner reaches the assistant node's workspace by key (non-interactive
  command form *and* interactive session), a command's remote exit status is
  returned, and a session from a machine that does not own the assistant is
  refused with `Permission denied (publickey)`.
* Full gate after the patch: `go vet ./...` clean, `go test ./...` green.

## Still open (recorded, not fixed here)

The audit that found these also found the larger blocks. WS-1.6 closed the
§9–§11 block (see the section below); what remains is: player-side
firewall/port-forward/DMZ controls (§14), IPv6/CGNAT/dynamic-WAN (§13), VPS
lifecycle beyond create+ssh (§12), and the §33 defence tools beyond the
fail2ban counter. Phase 2 (multiplayer, §31/§34/§35) has not been started and
needs a household-boundary decision first.

---

# Package ecosystem (WS-1.6) — §9 软件包系统, §10 repository/mirror, §11 软件安全

Ownership: `internal/core/distro.go`, `internal/core/pkgfiles.go`,
`internal/core/pkgnet.go`, `internal/core/pkgapply.go`,
`internal/core/pkgs.go`, `internal/shell/pkg_cmds.go`, `tests/pkgs_test.go`.
Touched, with their owners' sections updated here: `internal/core/engine.go`
(install bookkeeping + `MirrorTick` in `Tick`), `internal/core/world.go`
(`Device.InstalledFrom`), `internal/core/net.go` (a device that runs its own
resolver asks itself), `internal/core/vfs.go` (`MkdirAll` cannot spin on a
relative path), `internal/core/world_init.go` + `devseed.go` + `npc.go` +
`vm.go` + `addr.go` (archive/cdn hosts, per-distro images), `internal/shell/
net.go` (http is served from the host's real files) and `internal/shell/
world_cmds.go` (`vps create PLAN [hostname] [image]`).

## The model

A package is not a Go struct that appears on a device. It is:

    catalogue (Go)  →  files on the archive host  →  files on the mirror host
                    →  fetched + verified by the client  →  state on the device

* **Files, not memory.** Each repository renders real index files, real
  payload files and a real signed release file onto the host that publishes
  it (`RenderTree`/`WriteTree`). `curl`, `grep` and `nano` on the mirror see
  exactly what the package managers read.
* **Five families, five layouts.** Debian (`dists/<suite>/InRelease`,
  `binary-amd64/Packages`, pool `.vpkg`), Alpine (`APKINDEX` + `.apk`),
  Arch (`<repo>/os/x86_64/<repo>.db`, `.pkg.tar.zst`), Fedora (`repodata`,
  `.rpm`), OpenWrt (`packages/<arch>/Packages`, `.ipk`). Each family has its
  own sources file, keyring, list cache and command semantics; the family
  fixes the index layout, so a wrong path is a file that is not there.
* **The client really fetches.** `FetchHTTP` resolves the mirror's name
  through the device's own resolver chain, dials port 80 through the real
  firewall and NAT rules, and reads the file the serving host has on disk. A
  stopped nginx, a powered-off mirror or a broken resolver all fail the
  install, with the reason.
* **Verification is byte-level.** The release file names its signing key; the
  device must have that key in its keyring or the update is refused with
  `NO_PUBKEY` and a hint naming the keyring directory. Every index is checked
  against the hash the release file publishes for it, and every payload
  against the hash its index entry publishes. A half-finished sync, a deleted
  index or an edited index is therefore *detectable*, not asserted.
* **Mirror state is derived from the served bytes.** `World.RepoIntegrity`
  runs the client's verification on the mirror host. `CORRUPTED` means the
  release file does not describe the files next to it; `BEHIND` means the
  last successful sync is older than the threshold (and, if the crontab's
  `mirror-sync <tree>` line is commented out, the status says so);
  `PARTIAL` means upstream no longer carries a component. A sync that dies
  after writing bytes that still match its release file is not called
  corruption.

## What exists

* **Managers**: `apt`, `apk`, `pacman`, `dnf`, `opkg` — one command per
  family, gated on the device's distribution. The wrong manager on a box is
  `command not found`; a non-root user gets that manager's real lock refusal.
  `update` fetches and verifies, `install` reads the cached lists, `remove`
  refuses to break a dependency, `search`/`list`/`list --installed` read the
  served index and the device's own database.
* **Dependency engine**: `PlanInstall` (topological, dependency-first,
  reported failure), `RemoveCheck` (reverse dependencies, real refusal),
  `RemovePkg` (stops and deregisters services, removes the files the package
  owns, clears the record, keeps files another package still provides).
* **Provenance**: `Device.InstalledFrom[name]` records the repository a
  package came from, chosen from the sources that device actually has, so
  `apt list --installed` and `dnf list installed` can show `origin=debian`
  or `@fedora` instead of guessing from the catalogue.
* **Mirror lifecycle**: the world's `archive` host publishes every tree; the
  `mirror` host copies indexes *and* the pool it points at, verifies the
  release against the bytes it just wrote, and is driven by the world's own
  cron (`*/15 * * * * mirror-sync <tree>`, seeded by `seedMirrorSchedule`).
  The debian line is commented out after a disk incident, which is the whole
  cause of the seeded BEHIND state; `mirror-sync <tree>` (a shell builtin,
  root only) runs one sync by hand and `mirror-sync` alone prints the table.
* **Third-party source (§11)**: `sashimi` is a real tree on the `cdn` host
  signed with a key that is in no keyring. Its key is published with the tree
  (`keys/<fingerprint>.asc`), so trusting it is a real act with real material
  behind it; until then every update naming it fails. Its `nettop` package
  ships a background `updater` — the world logs it, `ps` shows it, and the
  event log records that a third-party package started a background service.
* **Provisioning**: `vps create PLAN [hostname] [image]` and
  `ProvisionVPSWithOS` give the buyer debian|ubuntu|alpine|arch|fedora with
  that distribution's sources file, keyring and manager, a locked root and a
  sudo account; VM guests get the same real package management.

## Verified

`tests/pkgs_test.go` (all with a fresh world, all asserting world state):

* every distribution has exactly one manager and its own sources file, and
  the others are `command not found`;
* install before update refuses (`no package lists are cached`), a non-root
  user is refused by the lock, `apt update` fetches the mirror's metadata,
  `apt install htop` lands the payload's file, records the package and its
  origin, `apt list --installed` reads it back, removal removes the files and
  the record, and re-installing works;
* nginx pulls libc, creates its files, user and running service; removing
  libc is refused while nginx needs it and succeeds after nginx is removed;
* the seeded debian tree is `BEHIND` *with the disabled cron line named*, the
  client warns on every update, a stale tree still installs, and
  `mirror-sync debian` makes the warning go away;
* deleting a served index makes the tree `CORRUPTED` (by file, not by flag),
  the client refuses to install from it, and a real sync repairs it;
* a third-party source is refused with `NO_PUBKEY` until its published key is
  trusted, and then installs a package that starts a background process and
  says so in the event log;
* stopping the mirror's web server breaks updates and starting it fixes them;
* a VPS bought as alpine/arch/fedora really has that manager, that sources
  file, a root account, the archive key, and installs over `sudo`;
* publishing a new package upstream, then killing the sync between its index
  and its release file, leaves damage both ends can prove (`hash sum
  mismatch`), and the repair sync makes the new package installable;
* the mirror keeps itself current from its own crontab, and the tree whose
  line is commented out is the one that stays behind.

Full gate with this workstream: `go vet ./...` clean, `go test ./...` green
(27 test files, ~172 tests). `tests/world_test.go` and `tests/ftp_test.go`
now reference `w.Repos["debian"]` (the old `main`/`contrib` repos are gone),
and `tests/tls_test.go` fetches the mirror's front page, which is now a real
index of the trees it serves instead of a hardcoded string.

## Not implemented on purpose

* **Player-hosted caches/mirrors** (§10 wants them): the pieces are there —
  files, sync, verification — but a player-run cache would need a "serve this
  tree from my box" command and a client that accepts a second source. Not
  faked; recorded here.
* **Package version anomalies as events** (§11 包版本异常): a repository can
  publish an old version and a client will install it, but nothing yet
  compares versions or alerts on a rollback. `PublishPackage` is the hook a
  future "upstream regressed" incident would use.
* **Source packages (`deb-src`), multi-arch, and `apt`'s deb822 drop-ins**
  are parsed where the format is trivial and ignored where it is not.
* **Removal does not garbage-collect the pool**: a mirror keeps files an
  index no longer references, as real mirrors do.

---

# Household network (WS-1.7) — §14 家庭网络, and the §28 attack surface it feeds

Ownership: `internal/core/uci.go` (UCI parse/render/stage),
`internal/core/firewall.go` (the packet path's view of exposure),
`internal/shell/fw_cmds.go` (`uci`, `iptables`, `upnpc`),
`tests/firewall_test.go`. Touched, with their owners' sections updated here:
`internal/core/net.go` (`Dial`'s forwarding block, `PermitsWAN`, DMZ, the
bypass of the target's LAN scope), `internal/core/world.go` (`Device.Firewall`
and `Device.PortFwd` deleted, with `FWRule`/`FwdRule`),
`internal/core/devseed.go` + `world_init.go` (shipped configs, mara's
forward), `internal/core/iot.go` + `internal/shell/iot_cmds.go` (`camctl`,
`World.CameraCloud`), `internal/shell/vscript.go` (`reload` verb) and
`internal/shell/world_cmds.go` (recon).

## The model

Exposure is not a flag on a device. It is a configuration file, read fresh on
every packet:

* **Router** = `/etc/config/firewall` (OpenWrt UCI: `config defaults`,
  `config redirect`, `config rule`) + `/etc/config/upnpd` + the daemon's live
  lease file `/var/run/miniupnpd.leases`.
* **Host** = `/etc/iptables/rules.v4` (Fedora/RHEL: `/etc/sysconfig/iptables`),
  iptables-save format.
* `Device.FW()` returns `FirewallState{Redirects, Rules, UPnPLeases, WANInput,
  ForwardPolicy, HostInput, LogDrops, Errors}` and re-reads those files every
  call, so there is no cache to invalidate and no way for the log and the
  packet path to disagree. `AllRedirects()` adds the UPnP mappings when the
  daemon is enabled. `PermitsWAN(port)` is the one question the packet path
  asks; `RedirectFor(port)` is the NAT decision, where `WPort == 0` is a DMZ
  matching every port.
* **Fail closed.** A file that does not parse yields *no* rules — never the
  last-known-good set. `FirewallState.Errors` carries the reason, recon and
  `/etc/init.d/firewall reload` print it, and `uci` refuses to edit a file it
  cannot parse, the way real uci does, so a broken config cannot be quietly
  rewritten from a lossy parse.
* **Two verbs, one meaning.** `uci set/add/delete` stage; only `uci commit`
  writes the file. Before the commit, the packet path is untouched — which is
  the whole reason the tool has two verbs, and what makes "half-applied
  firewall" a real failure mode.

## What exists

* **`uci`** (root only): `show`, `get`, `set`, `add`, `add_list`, `delete`,
  `commit`, `changes`, `revert`. `uci add firewall redirect cam-rtsp` makes an
  anonymous section and prints its ref (`@redirect[0]`); `uci set
  firewall.cam-rtsp=redirect` creates a named one. `uci changes` lists the
  pending commands per config; `uci commit <config>` applies exactly those and
  clears them; `uci revert` throws them away.
* **`iptables`** (root only, INPUT chain): `-S`, `-L`/`-n`/`-v`, `-A`, `-I`,
  `-D`, `-F`, `-P`, with `-p tcp|udp`, `-i <iface>`, `--dport`, `-j
  ACCEPT|DROP|REJECT`, and an optional trailing `# comment`. It reads and
  rewrites the same file `iptables-save` would, so `cat`, `iptables -L` and
  the packet path can never disagree (both the `:INPUT DROP [0:0]` policy
  line and `-P INPUT DROP` are understood).
* **Real NAT and filtering in `Dial`**: a connection arriving on a router's WAN
  port consults `PermitsWAN`; a permitted port that has a redirect is DNATed to
  the LAN target and the connection *continues* there (no shortcut: the target's
  own service, scope and rules decide the outcome); a DMZ is a redirect with no
  destination port. A forward deliberately bypasses the target's LAN-only
  scope — that is what publishing a port means — and every translated or
  dropped packet is logged (`DNAT gateway:554 -> 10.77.1.40:554 (redirect
  cam-rtsp, from 203.0.113.3)`, `DROP wan 554/tcp ... (no rule permits it)`)
  when `log_drops` is on.
* **UPnP** (§14's one hole nobody typed): `upnpc -l/-a/-d` is the client half
  and only finds a gateway *upstream* of itself, so it works from a LAN host
  (and from the camera) but not on the router itself. `World.UPnPMap` writes a
  real lease file, logs on the router, and posts an event; `upnpd.enabled=0`
  stops honouring the leases without deleting them (so re-enabling restores
  the mapping), and `upnpc -d 554 tcp` from a LAN host deletes one, naming the
  host that did it.
* **`camctl`** (the vendor CLI on the camera): `status`, `cloud on|off`.
  Turning cloud viewing on is a real UPnP request through the router; it fails
  closed (`cloud viewing unavailable: ...`) when UPnP is off, and a failed
  request writes no config. `status` reports configuration and reachability
  *separately* — "cloud viewing: on" plus "no mapping on the gateway" is how a
  camera looks when somebody quietly un-mapped it, and that gap is the point.
* **`/etc/init.d/firewall reload`** is a check verb, not decoration: it reports
  what is in force right now, the redirects by name, the default policies, the
  parse errors if any, and writes `/var/run/firewall.applied`.
* **recon** prints the router's `ExposureSummary()`: every redirect with its
  target (UPnP mappings labelled as such), the DMZ as "all ports", the WAN
  input policy, and a warning when the router's own management interface is
  exposed.

## Defaults and the shipped household

`router` seeds `wan_input REJECT`, `forward REJECT`, `log_drops 1`, UPnP
enabled, and zero redirects; `pc`, `nas` and the assistant seed DROP plus the
LAN accepts they need (ssh everywhere, SMB on the NAS); a VPS and the
infrastructure hosts seed ACCEPT plus ssh, because a public host that answers
is what "public host" means. Unruled WAN input is dropped for
`pc|nas|router` and allowed for `core/vps/infra/peer/server`.

## The tests

`tests/firewall_test.go`, ten tests, each with a happy path, a permission or
policy boundary and a recovery:

* the shipped household filters 22/23/80/443/554/445/2049 from the internet,
  logs the drops, still serves the LAN, and has an empty exposure summary;
* `uci set` changes nothing until `commit`, `revert` discards, and a committed
  `enabled=0` closes the hole;
* a port-forward really translates (the camera's stream case, and the NAS's web
  console), logs the DNAT, recon follows it to the machine behind, and closing
  it restores the default;
* a DMZ carries *every* port to one host — one that answers, one that refuses —
  and removing the section restores the router;
* `src wan ... ACCEPT` exposes the router's own sshd; flipping
  `wan_input=ACCEPT` exposes its telnetd too and makes `ExposureSummary` warn,
  and flipping it back closes it;
* a VPS: a non-root user is refused by `iptables`, `-P INPUT DROP` closes
  everything but the published ssh, `-A`/`-D` publish and unpublish, and the
  file, `iptables -L` and the packet path agree;
* UPnP: the camera asks, the lease and the logs and the event appear, the
  internet reaches the stream, `upnpd enabled=0` stops honouring it without
  deleting the lease, a request while disabled fails without changing the
  camera's config, re-enabling works, and a LAN host removing the mapping
  closes it and is named in the log;
* the camera's console requires a real gateway and starts off;
* **no simulation step ever opens a port** — 200 ticks with the mirror
  syncing, NPC routines, cron and IoT, and the router is still closed;
* a malformed config fails closed, `reload` says why, `uci` refuses to edit it,
  and a hand-repaired file restores service.

## Not implemented on purpose

* **IPv6 firewalling**: `ip -6 addr` still shows no inet6 on this network, and
  the firewall model is IPv4 (`v4`-path files). §13 is where addresses grow,
  and ip6tables follows the same shape if it is ever needed.
* **conntrack states** (`-m state`), `ipset`, `fw3`, and per-rule packet
  counters: the ruleset is real but simple, and the renderer writes the file
  the tools read rather than pretending to be netfilter.
* **A web UI for the router**: managing it means `ssh` + `uci`, which is the
  honest interface for the images this world ships.
* **Wireless client isolation and VLANs**: the guest network exists as an
  interface, but §14's per-network policy is not modelled yet.

# IP system (WS-1.8) — §13 地址系统, and the addressing §12 needed to exist

## The model

One vocabulary, in `internal/core/addrs.go`: `ClassifyAddr` returns an `AddrInfo`
whose `Kind` is `loopback | link-local | private | shared | public-v4 |
public-v6 | ula | unassigned | unknown`, and every other part of the world asks
it rather than re-deriving "is this public". §13's list maps onto it directly:

* **public IPv4** — one /24 per autonomous system, allocated per prefix so a
  prefix has exactly one holder (that is what makes `whois` attribution truth
  rather than a guess);
* **public IPv6** — one /48 per AS, `2001:db8:<asn hex>::/48`, with subscriber
  /64s handed out from index 16 up and read back from `WAN.V6Issued`, so
  renumbering or a reload cannot hand the same /64 to two households;
* **RFC1918** — the household's own `10.77.1.0/24` and the LAN plans;
* **CGNAT / shared address space** — `100.64.0.0/10` from `allocSharedV4`; the
  address sits on the interface as `Iface.SharedIP`, never enters `IPMap`, and
  no inbound packet can reach it;
* **loopback / link-local / ULA** — `::1`, `fe80::`, `fd00:<lan>:<n>::/64` per
  LAN; a link-local address is present on every v6 interface and is explicitly
  *not* connectivity (fastfetch says "link-local only");
* **dynamic** — a DHCP lease is a file (`/var/run/udhcpc.<if>.lease`) and the
  address is marked `dynamic valid_lft` by `ip addr`; `RenumberWAN` moves a
  node's WAN address, its `IPMap` entry and its A record together, and refuses
  an address somebody else holds;
* **shared IP** — a plan whose nodes sit behind the provider's NAT: outbound
  works, inbound says "No route to host (carrier-grade NAT…)";
* **virtual IP** — a secondary public IPv4 lent by the provider
  (`AttachVirtual`/`DetachVirtual`/`VirtualIPs`), on the interface as a second
  `inet … secondary`, registered in `IPMap` so the packet path lands on the
  right device, movable between nodes, refused onto somebody else's address or
  a private one, and never removable when it *is* the node's own address;
* **NAT mapping** — the §14 translator stays the only thing that opens an
  inbound path, and it is re-read per packet.

## IPv6 is not decoration

Every device has a real v6 stack: a global address, a ULA and a link-local one.
`ip -6 addr`, `ifconfig`-family output, `ping6`, `dig -t AAAA`, `ss -6` and
`ip -6 route` all read it. Publication follows §13's rule that IPv6 does not
translate: a LAN host is reachable on v6 only when a rule on the router in front
*names its address* (`Rule.DestIP` in `/etc/config/firewall`, no NAT) **and** the
host's own `ip6tables` INPUT accepts the port. Either one missing and the
connection times out, which is exactly the two-sided configuration a real
household has to get right.

## Attribution and §12's rDNS

`whois`/`rdap`/`bgp` answer from the AS that announces the prefix: provider, ASN,
region, range, abuse contact, rDNS status — never a person. `Attribution.Kind`
and `Note` carry the qualifier when the answer is not a plain owner (carrier
grade NAT says so in words). A node's `RDNS` name is published in the reverse
zone, so `dig -x` returns the operator's chosen name and a private or CGNAT
address has no PTR at all.

## The packet-path bug this workstream exposed

`routerFor` now skips the device itself — a router is not its own upstream — and
that change silently removed the router from three call sites that *must* judge a
packet addressed to the household's edge: `Dial`, `Reach` and `ForwardTarget`.
The symptom was a router that stopped logging WAN drops (so §14's "filtered
packets are logged" stopped being true) and a DMZ that stopped being followed.
All three now fall back to `r = dst` when the destination is a router and no
upstream router exists, and the 14 firewall/FTP tests that depended on it are
green again.

## Verified

`tests/addrs_test.go` (9 tests) plus `tests/vps_test.go`'s region and rDNS cases:
the shipped world's addresses classify as they should; every device has a real v6
stack; a household host is unreachable on v6 until both the router rule and the
host ruleset allow it; AAAA records are published and bounded; a shared plan is
outbound-only while its egress shows the operator's NAT address; a v6-only plan
has no IPv4 at all; a dynamic address renews without moving and moves only when
the operator renumbers it; a reserved address is movable; `dig -x` finds the
operator's PTR and never a person.

## Not implemented on purpose

* **NAT64 / DNS64**: a v6-only node really cannot reach IPv4, and saying so is
  more honest than a translator nobody asked for.
* **SLAAC/RA in the packet path**: addresses are assigned by the world's own
  allocator; router advertisements are not modelled as a protocol.
* **IPv6 prefix delegation to the household's own router by DHCPv6-PD**: the
  delegated /64 is read back from the provider, which is the same fact without
  the wire protocol.

# VPS lifecycle (WS-1.9) — §12 计算机 as a machine you own

## The model

A rented node *is* a Device, so every panel verb is an operation on the machine
the rest of the world talks to. `VPSStop` really powers it off (`setPowered`),
which stops its services, clears its processes and makes `Reach`/`Dial` report
"host is down"; `VPSStart` brings back exactly the services it was running;
`VPSReboot` keeps the disk and resets uptime. Nothing here prints a status the
machine does not have.

**Regions are networks.** NovaPanel sells three datacenters, each its own AS
(`asNova` eu-central, `asNovaUS` us-east, `asNovaAP` ap-northeast) with its own
/24 and /48 — so a `--region` choice moves the address, the `whois` attribution
and the real latency a `traceroute`/`ping` reports. An unknown region is refused
before any money moves.

**The panel keeps records.** `Provider.Nodes` holds each node's plan, image,
region, datacenter, rDNS, monthly price, rebuild count and snapshots; `vps show`
prints them and `LoadWorld` backfills old saves.

## What exists

* `vps regions` — the published datacenters, their AS and their block.
* `vps create PLAN [hostname] [image] [--region R]` — buys in a region.
* `vps show <host>` — plan, image, region, state, addresses, reserved addresses,
  rDNS, price, rebuilds, snapshots.
* `vps start|stop|reboot <host>` — real power, real uptime, real reachability.
* `vps reinstall <host> <image>` — refused while running; wipes the disk, lays
  down the new distribution's own `/etc/os-release`, repositories, package
  manager and shell, **keeps** the address and the name, and issues new
  credentials (the rebuild count seeds the password, so the old one really stops
  working).
* `vps resize <host> [--cpu N] [--mem MB] [--disk MB]` and `vps disk <host> GiB`
  — refused while running, disk can only grow, RAM cannot shrink below what is
  in use, upgrades are billed (downgrades are not refunded), block storage is
  billed monthly, and the household's wallet and the panel's monthly price both
  move.
* `vps snapshot|snapshots|restore` — a snapshot is a real copy of the disk
  (files, accounts, packages); restoring is refused while running, brings back
  deleted files and removes later ones, and leaves the machine stopped so it can
  boot the restored system.
* `vps console <host>` — the machine's own shell (the same code path as
  `vm console`), refused on a powered-off node.
* `vps rdns <host> [name]` — publishes the reverse name; `dig -x` shows it.
* `vm create … --ip public|shared|v6only` — a hypervisor guest now gets the same
  addressing choice its plan implies (`CreateVM(..., ipMode)`), priced by the
  same rule via `core.NodePriceCents`.

`NodePriceCents` now lives in core (the shell's `vmCost` delegates), which is why
the panel and `vm create` cannot drift apart.

## The tests

`tests/vps_test.go`, seven tests, each with a happy path, a boundary and a
recovery: power off/on/reboot with real reachability and uptime; another account
refused every verb; reinstall refused while running and afterwards a different
system on the same address with dead old credentials; resize/disk refused while
running, billed when stopped, disk shrink and unaffordable storage refused;
snapshot/restore rollback with an unknown snapshot refused; regions giving
different blocks, different owners and measurably different latency; rDNS
published, changeable, refused for a malformed name and for another account;
console landing on the node's own filesystem and exiting.

## Not implemented on purpose

* **Bandwidth/transfer billing and datacenter migrations**: the panel bills for
  what this world can really measure; moving a node between regions would be a
  new address plus a new host, and pretending otherwise would be worse than not
  offering it.
* **Snapshot scheduling**: snapshots are the customer's action here, not a cron
  the player cannot see.
* **Live resize**: a real provider can sometimes hot-grow a disk; this world
  requires a power-off, which is the conservative and checkable rule.

# 家庭设备与物理层 (WS-1.10) — §15 家庭设备

## The model

§15 asks for the household's own machines and the failures between them. The
answer is a **physical layer made of state**: `link.go` gives every device an
uplink (`Device.Uplink`/`UplinkPort`) and every managed switch a real port table
(`SwitchState.Ports`, 8 ports, a PoE budget, per-port admin/PoE/speed). The
switch in the hall is a device with its own `dropbear`, its own UPS and its own
config file, not a hub-shaped constant.

Four things follow from that, and each one is a failure a player can chase:

* **A dead wire is a diagnosis.** `World.linkUpVia` is the only wire question;
  `Dial`/`Reach` prepend its reason, so the printer behind a downed port answers
  "Connection timed out (link down: port 6 (printer) on sw-hall is down)" instead
  of a generic unreachable.
* **A switch whose uplink died is an island, not a blackout.** Traffic that stays
  inside the island passes `crossUplink=false`, so two devices on the same switch
  still reach each other with the router dark — which is exactly why the cameras
  keep streaming through an outage while nothing reaches the internet.
* **PoE is electricity with a budget.** Power is spent in port order: a port that
  does not fit under `BudgetW` is refused, the ports already up keep theirs, and
  turning a camera's PoE off really kills it (its port then shows link *down*,
  because a PHY without power has no link). `DarkenPoE` runs on a plug pull and
  when a UPS runs flat.
* **The config is a file.** `/etc/config/switch` is rendered from the port table
  and re-read on every `linkUp`/`switchctl` (`RenderSwitchConf` /
  `LoadSwitchConfig`), exactly like the firewall — a hand edit takes effect.

Power is a first-class cause rather than a special case. `Powered()` walks
PoE → battery/lid → mains, so a camera fed by the switch, a laptop on its battery
and a NAS behind the breaker all answer honestly, and
`Device.UnavailableReason()` is the one place that puts it into words — shared by
the shell's "Connection to host lost (no power — the household supply is off)",
the switch's port table and the SSH entry's out-of-band fallback. Physical acts
got verbs (`power cut|boot|plug|unplug|lid HOST`) because a breaker, a socket and
a lid are things done with hands: performable from another machine, but not by a
machine that is dark or asleep itself.

## What exists

* `sw-alex` (TP-Link TL-SG108PE): 8 ports, 60 W PoE budget, UPS, dropbear:22;
  port 1 = uplink to the gateway, 3 = laptop, 5 = camera (PoE, 8.2 W), 6 =
  printer, 2/4/7/8 free.
* `laptop-alex`: battery + lid + charger state, users `alex`/`guest`, its own
  `dropbear` so the machine is actually reachable.
* `prn-alex`: a real spooler — `/var/spool/cups/queue`, `tray.log`, `cupsd:631`,
  paper and toner that run out, 60 lines to a sheet.
* `switchctl|swconfig show|status`, `port N up|down`, `poe N on|off`,
  `budget [W]`, `attach N HOST [poe]`.
* `lp|lpr`, `lpstat`, `cancel`, `lpadmin status|paper|toner|pause|resume` — the
  client half dials the printer's own port before spooling.
* `laptopctl status|lid open|closed|charge on|off`, `power plug|unplug|lid HOST`,
  `backup run|status`, `links` (the household's own view of what has a wire).
* `cam records` on the camera, whose clips land on the NAS: with the NAS down the
  camera says "recordings dropped: storage target unreachable" and stops.

## The chain from the spec

Camera → PoE switch → router → NAS is live in the seed, so each link's failure
has its own signature: PoE off (camera dark, switch fine), port down (device
powered but off the network), switch UPS (the house goes dark, the cameras keep
running, then the UPS runs flat and everything PoE does too), NAS unplugged
(backups and recordings stop, shares unreachable), and the main breaker (the PC
dies mid-session — the SSH entry then lands the player on the BMC, which is on
its own battery and cellular backhaul, and `bmc power boot` restores the house).

## The tests

`tests/house_test.go`, seven tests, each with a happy path, a boundary and a
recovery: cabling and port-file parity with the running state, an uplink cut
refusing to take the island down, `switchctl` diagnostics; port-down → printer
offline but still powered (syslog + recovery) and PoE-off → camera dark
(recovery); PoE budget bounds (refuse below the current draw, shrink when a
camera goes off, refuse a new port that does not fit, PoE on a self-powered
device is an error); a power cut where the switch's UPS keeps the cameras up
while the NAS dies, battery drain → everything PoE dark → `bmc power boot`;
printer spool/tray/out-of-paper hold/paper load/cancel permission/dead-port
submission; laptop battery → flat → charger recovery → lid suspend/wake → the
desktop says it has no lid; and the NAS as a dependency of `backup run`, of its
shares and of the camera's recordings, with recovery.

`tools/house_verify.sh` is the live counterpart: a real SSH session walking the
whole sequence over `:2222`, ending with the switch's own config file read back
from the switch itself.

## Not implemented on purpose

* **802.1Q/VLANs and STP**: the port model carries admin/PoE/speed/labels, which
  is what the household's failures need; a spanning-tree simulation would be a
  second, unobservable network stack.
* **Switch firmware updates and SNMP**: an unobservable daemon is scenery —
  `switchctl` reads the same state `linkUp` uses instead.
* **Printers with their own queue UI over IPP**: `cupsd:631` answers and the
  queue is real; implementing IPP's wire protocol would not change one byte of
  state a player can see.

# 系统状态 / 资源管理 (WS-1.11) — §17 资源不是装饰

## The model

§17 lists CPU, RAM, swap, disk, disk I/O, network bandwidth and process count and
says every one of them must really affect the system. The answer is a small
kernel-side accounting layer (`internal/core/resources.go`) on top of state that
was already real — memory in use and disk in use are *derived* from the processes
and the filesystem, never stored twice:

* **CPU is shared, not claimed.** Each process has a `WantCPU` and a measured
  `CPU`; `CPUShare()` is the fraction of the request a machine can honour. A
  machine with four workers on one core really gives each of them a quarter, and
  `htop` shows both numbers so the gap is visible. Everything that should slow
  down consults that one share: the printer's spooler earns credit per tick
  (a sheet costs two ticks of an idle CPU), the assistant's job progress
  advances by it, and bulk work (`WorkRate`) multiplies by it.
* **Load averages are the machine's own.** `uptime`, `top` and `htop` print the
  exponentially weighted averages of `CPUDemand()/CPUCapacity()`, so a busy
  machine says so and decays honestly when the work stops.
* **RAM → swap → OOM.** Over RAM pages into swap (half the RAM, the rule `free`
  has always printed); swap full with the machine still over RAM kills the
  biggest resident process for real — services marked failed, the victim gone
  from `ps`, the kernel's own "Out of memory: Killed process" line in the log.
  With nothing but daemons left, the services are what fails. The same chain a
  guest VM already had now applies to the machine itself.
* **A full disk is an error, not a warning.** `DiskLimitMB()` is a guest's
  virtual disk or a host's own disk, and every player-visible write goes through
  `WriteGuest`, which returns `no space left on device` when the bytes do not
  fit. Logs are the second casualty: a full `/` really loses syslog lines,
  `df` reports the count, and the reckoning ("N message(s) dropped") is written
  once there is room. Installs check room first and refuse in each manager's
  voice.
* **Disk I/O and the link are throughput.** `DiskMBps` (per profile, overridable)
  and `HW.NetMbps` give `WorkRate()`: a gigabit-plus-500 MB/s machine completes
  one unit of bulk work per tick, a slow one takes proportionally longer. The
  mirror sync is the visible consumer — a sync that took three ticks on a fast
  mirror takes more on a slow or busy one, and the tree's state is unchanged by
  the waiting.
* **The process table is finite.** `ProcLimit()` (RAM/8, floor 24) refuses forks
  with `Resource temporarily unavailable`, the count of refusals is kept, and
  `vmstat` reports it.

## What exists

* `stress` — real background load: `--cpu N`, `--vm N --vm-bytes SIZE`, `--timeout T`
  (`pkill stress` ends it early). The workers are real entries in the process
  table with real memory and real demand.
* `dd` — writes real bytes into the filesystem (`if=/dev/zero`, a source file,
  `bs=`, `count=`, `status=none`), reports the records that landed, and fails
  with the filesystem's error when the disk fills. A single file is capped at
  64 MiB — a stated model limit, not a fake errno — so a disk fills the honest
  way, with several files.
* `htop`, `top` — memory, swap, tasks, load average and per-process CPU vs what
  it asked for, with a line when the machine is over RAM or the disk is full.
* `free` — real memory and real swap in use; `df` — the real limit, the warning
  when writes are failing and the dropped-log count; `uptime` — the measured
  load average; `vmstat` — one sample per command (the world clock moves on
  ticks, so there is nothing to sleep for) with the per-tick disk and network
  rates the transfer paths feed.

## The tests

`tests/resources_test.go`, six tests, each with a happy path, a boundary and a
recovery: four workers on one core sharing a quarter each with a spooler that
stops finishing sheets, the load average climbing and decaying, and the fork
limit refusing the load past the table's size; a 300 MiB hog paging, a second
hog taking the machine past swap and the kernel killing the biggest resident
while the printer's own daemon survives and the swap drains; 16 MiB of flash
filling from `dd`, the write after it failing with the filesystem's error, the
log losing lines and counting them, and the file being removed to bring
everything back; `apt` refusing to unpack onto a full disk in its own voice and
taking the same package once there is room; every monitoring command printing
the same numbers the consequences use; and the same 64 MiB write taking twice as
long on a mirror whose disk is two fifths as fast.

`tools/resource_verify.sh` is the live counterpart over `:2222`: a load on the
laptop, the same write measured before and after, an OOM kill on the switch, and
a disk filled, logged, recovered.

# 防守和安全软件 (WS-1.12) — §33 防守和安全软件

Ownership: `internal/core/security.go` (flows, failures, bans, jails, IDS rules,
`SecurityTick`), `internal/core/secaudit.go` (auditd, aide, clamav, monit,
central logs, quarantine), `internal/core/attacker.go` (the world's own
scanner), `internal/core/backup.go` (restic-shaped backup),
`internal/core/hids.go` (rkhunter-shaped host monitoring),
`internal/shell/sec_cmds.go` + `internal/shell/backup_cmds.go` (the tools'
commands), `tests/security_test.go`, `tools/security_verify.sh`. Touched, with
their owners' sections updated here: `internal/core/world.go`
(`Service.MonitDown`, `Device.Security` and its gob shadows),
`internal/core/engine.go` (`ScannerTick()` then `SecurityTick()` in `Tick`),
`internal/core/net.go` (the ban gate in `Dial`, and `NoteFlow` on every
attempt), `internal/core/ftp.go` + `internal/shell/{remote,sftp_cmds}.go` (a
rejection is recorded by the target), `internal/core/vmdisk.go` (a real write
is what an audit rule sees), `internal/core/firewall.go` (`FirewallPosture`),
`internal/core/cron.go` (`/etc/cron.d` support, so a package's own schedule
runs), `internal/core/pkgs.go` (`putSecurityTools` for debian/alpine/fedora,
and the malware database on the mirror), `internal/core/world_init.go` (the
household's own facts: the NAS keeps the central log, the router forwards to
it, the laptop carries the sample malware), `internal/shell/log_cmds.go`
(`logger`) and `internal/shell/{fs,shell}.go` (printf really formats, the
quote rules are the real ones — see below).

## The model

§33 asks for defence tools that really affect the world. This world offers them
three facts, and every tool below is one of them:

* **flows** — the packet path's own record of who tried to reach a machine.
  There is no packet capture here, so the flows *are* the sensor: every `Dial`,
  whatever its verdict, leaves a line on the destination (`Device.NoteFlow`, a
  ring of 400). A port scan is therefore a *shape in that table* — N distinct
  ports from one source inside a window — a brute force is a count of
  refusals, and 异常流量 is the third shape: N connection attempts from one
  source, whatever ports they are.
* **fails** — rejected authentication, recorded by the **target**, never by the
  client. A jail counts what the machine it defends actually saw, so an
  attacker cannot talk their way out of the record by not being logged.
* **changes** — file hashes (aide), kernel audit records (auditd), service
  states (monit), the lines a machine writes (central logs), the bytes of a
  file (clamav), the repository's own manifests (restic) and the machine's
  facts about itself (rkhunter).

Three rules follow, and they are what separates this from decoration:

1. **A tool is installed software.** Its commands exist only once its package
   is installed (`pkgBinaries` + `pkgCommandMissing` in `shell.go`): on a fresh
   machine `fail2ban-client status` prints `command not found`, and `secstat`
   names every missing tool and the config file it would have read.
2. **The configuration file is the configuration.** Jails, IDS rules, the
   integrity watch set, monit's checks, the audit rules, the backup repository,
   the host monitor's watch list and the router's log destination are re-parsed
   on every call — an operator who edits a file changes behaviour on the next
   command, and nothing is cached.
3. **A tool acts only while its service runs.** A stopped IDS alerts nobody, a
   stopped jail bans nobody, a stopped watchdog restarts nothing, a stopped
   auditd records nothing, a stopped collector receives nothing, a stopped host
   monitor compares nothing — and a ban is enforced in `Dial` *before* every
   other gate (port range, power, link, firewall, service), so a banned source
   really cannot reach the machine.

## What exists

* **`fail2ban`** — `/etc/fail2ban/jail.conf` (shipped: `bantime 10m`,
  `findtime 10m`, `maxretry 3`, an enabled `sshd` jail) is parsed on every call;
  `fail2ban-client status|status <jail>|set <jail> banip|unbanip|reload` acts on
  the real ban table. A ban expires on the world clock, and expiry *forgets the
  offender's failures* the way a real jail does, so the same three failures
  cannot re-ban forever.
* **`suricata`** — `/etc/suricata/rules` with `portscan ports= window=`,
  `authfail fails= window=`, `flood conns= window=` and `exploit` rules, each
  `action=ban,alert`, `alert`, a `level=` and a `bantime=`; `suricata -T` prints
  exactly what was parsed and refuses a rule file it cannot read. Alerts land in
  the machine's own alert ring (`secstat alerts`), bans in the same ban table
  fail2ban uses.
* **`aide`** — `/etc/aide/aide.conf` (`watch =` directories, `interval =`,
  `freshfiles =`), a real baseline database of per-path hashes, `aide --init`,
  `aide --check` (prints `modified`, `added`, `removed`) and `aide --status`.
  The baseline is immutable; each path alerts **once** per change, and a stopped
  monitor watches nothing.
* **`clamav`** — `freshclam` fetches `/clamav/main.db` from the mirror over the
  same HTTP path as the package metadata, `clamscan -r` finds the signatures
  that really are in the file, `--move=DIR` moves the bytes to quarantine and
  removes the original, and `secstat quarantine` lists what was taken. A machine
  that never ran `freshclam` refuses to scan (`virus database missing`), and one
  whose database is over a week old says `stale`.
* **`monit`** — `/etc/monit/monitrc` `check service <name> maxdown=N
  interval=T action=restart`: the service is really started again after N failed
  checks, a check for a service that does not exist reports `not-present`
  instead of inventing success, and the restarted service is *running*.
* **`auditd`** — every `/etc/audit/rules.d/*.rules` line is parsed with real
  `auditctl` syntax (`-w path -p wa -k key`, `-a always,exit -F arch=b64 -S
  execve -k exec`, `-F path=`/`exe=`/`syscall=`), `auditctl -l` prints the loaded
  set, and `ausearch -k key|-w path|-n N` reads the records. A record exists only
  if the daemon runs **and** a loaded rule matches.
* **central logs** — a machine configured to forward (rsyslog `*.* @host:514`,
  OpenWrt's `log_ip`/`log_port` from `/etc/config/system`) sends each line over
  the same packet path as everything else, so a collector that is down, banned
  or unwired really loses lines; the sender says `could not forward to <host>`
  when it notices. `secstat remote [host]` reads the collector's own record.
* **`restic`** (Backup) — the repository is real files on the machine that
  holds it: `config`, `keys/`, `data/<xx>/<blob>`, `snapshots/<id>`, `index/`.
  Every blob is content-addressed and written only when it is not already there,
  so the second backup of an unchanged tree costs nothing; every snapshot is a
  manifest naming the files, their modes and their blobs. `/etc/restic/env`
  names the repository (`RESTIC_REPOSITORY`, `RESTIC_PASSWORD_FILE`) and the
  shipped default is **off the machine it protects** (`root@nas:/srv/restic`),
  resolved through the same packet path as everything else. `restic
  init|backup|snapshots|check|restore|forget|stats|cat` — `check` re-hashes
  every referenced blob and `restore` re-hashes every blob it reads, so a
  tampered repository is a named error in both, never a hope and never the
  wrong bytes quietly written back into the live tree; `restore` refuses to
  clobber a live file without `--overwrite`, and
  `forget --prune` really drops the blobs no remaining snapshot references. A
  package's schedule lives in `/etc/cron.d/restic`, and `/etc/cron.d` is read by
  the game's cron with the user on each line, exactly as Vixie cron reads it.
* **`rkhunter`** (HIDS) — `/etc/rkhunter.conf` says what the host watches
  (`CHECK_SERVICES`, `CHECK_ACCOUNTS`, `CHECK_BINARIES`, `BINARY_WATCH`,
  `SUSPICIOUS_DIRS`, `INTERVAL`), `/var/lib/rkhunter/baseline` is the picture
  `--propupd` took, and `--check` compares the machine with it: an unknown
  listening port, an account that did not exist, a binary whose bytes differ, a
  new executable in a watched directory, an executable in a world-writable
  directory. The baseline is a deliberate act — nothing is reported before one
  is taken — and the running service raises each finding as an alert once per
  window.
* **`secstat`** — the posture report (`status`, `alerts [n]`, `flows [n]`,
  `bans`, `audit`, `firewall`, `hids`, `backup`, `remote [host]`,
  `quarantine`) reads the same state the tools act on, which is how a player
  checks the world instead of trusting a sentence. The firewall's two lines come
  from `Device.FirewallPosture()` — the same `FW()` every packet consults.
* **The world's own attacker** — a real device (`scan-host`) with a real public
  address and a default route that sweeps the public addresses of the world
  every 97 ticks, 24 ports per target, and then tries a short credential list
  against whatever ssh endpoint really answered (through a port-forward, if that
  is where the port landed). It stops when it is banned. Nobody scripts it.

## The shell had to be honest first

§33's story is edited in configuration files, so the shell had to grow up:
`printf` now really formats (escapes, `%s`/`%d`/`%x`/`%c`/`%b`, flags and width,
and the format repeating until the arguments run out) instead of printing its
own format string, `echo -e` interprets escapes while plain `echo` does not, and
`tokenize` follows the real quote rules — inside single quotes every character
is data (a backslash included), inside double quotes only `$`, `` ` ``, `"`, `\`
and a newline lose theirs, and outside quotes a backslash escapes what follows.
Without that, `printf 'watch = /etc\ninterval = 1m\n' > /etc/...` wrote a single
mangled line. `tests/world_test.go`'s `TestPrintfAndQuotingAreReal` pins it, and
the tools here are installed and configured through exactly that surface.

## The tests

`tests/security_test.go`, sixteen tests, each with a happy path, a boundary and
a recovery: the jail bans the source of three failures and the ban really blocks
`Dial` (and is lifted, and expires); an edited jail file changes behaviour; an
IDS portscan rule alerts and bans from a real sweep; an integrity check notices
a real change to a watched path and nothing at all while stopped; an antivirus
refuses to run without a database, flags a stale one, finds the sample malware
and quarantines real bytes; monit restarts a stopped service on the second
failed check and reports a check it cannot satisfy; auditd records a write to a
watched path and nothing to an unwatched one; central logs arrive at the
collector and a stopped collector loses them; each tool's commands are absent
until its package is installed; the scanner's tick is deterministic and bounded;
`secstat` agrees with the state behind it; a backup keeps yesterday's file after
today's deletion, restores it, notices a tampered blob and frees space on
`forget --prune`; a flood rule catches a flood and leaves one connection alone;
and the host monitor reports a new account and an executable in `/tmp`, then
goes quiet after a new baseline.

## Verified

`go vet ./...` clean, `gofmt -l` empty, the whole suite green.
`tools/security_verify.sh` is the live chain over the real SSH entry, and it
walks the story in twelve steps: a fresh household's posture (`secstat` naming
the missing tools), the scripted DNS fault repaired on the router, suricata
installed on the NAS where a real `nmap` from the PC becomes
`9 distinct ports from home-pc (2001:db8:fbfe:10::b) in 2m` and the same nine
attempts sit in the flow table, fail2ban closing the door on the PC's address
while the laptop's session to the same machine still opens (the ban is per
source address, and the operator lifts it by reading the jail's own report from
a session that is not banned), aide's baseline and a real edit to `/etc/hosts`,
clamav updating from the mirror and moving the sample file to
`/var/quarantine/home_alex_Downloads_invoice-2026-04.pdf.quarantined`, monit
bringing a stopped `sshd` back on the second failed check, auditd showing the
watched write to `/etc/passwd` and nothing for `/etc/hosts`, rkhunter taking a
baseline and then naming an executable planted in `/tmp`, the router's
forwarding stopping and starting while the refused attempts stay in the NAS's
flows, restic initializing, snapshotting twice across a deletion and restoring
the file — then catching a tampered blob on the next `check` (`blob 8071b8632f6a
is corrupt: hashes to 6ff5fdb4ba68`), the restore of the deleted file refusing
those bytes by name so the file stays gone — and finally the
world's own scanner appearing in the gateway's own record (`24 filtered` from
`2001:db8:fc08:10::1`, on its own schedule).

## Not implemented on purpose

* **Packet capture / real IDS signatures**: the flow table is the sensor. A
  world without packets would have to fake the capture, and a fake capture is a
  log line pretending to be evidence.
* **A full `fail2ban` filter language** (`failregex`, `ignoreip`, actions): the
  jail file carries the values that decide behaviour (`maxretry`, `findtime`,
  `bantime`, `enabled`). A regex engine would decide nothing else.
* **antivirus heuristics and signature updates over the Internet**: the database
  is a real file fetched from the household's own mirror; pattern matching is
  literal, and the sample is a test string on purpose.
* **deduplication across snapshots (restic's real chunker)**: one blob per file,
  refcounted by the snapshots that name it, up to a stated 1 MiB per file. A
  rolling-hash chunker would change no state a player can observe and would make
  a repository unreadable by hand.
* **remote/central alerting (e-mail, webhooks, SIEM)**: the collector's log and
  the machine's own alert ring are the household's record; §34's investigators
  read them.
* **quarantine restore**: files are moved, listed and readable; a restore verb
  would need a policy about who may un-quarantine what, which is §35's business.

## Not implemented on purpose

* **Schedulers, priorities and cgroups**: `nice` exists as the value it always
  was, and the fair share is one number per machine — a per-process scheduler
  would be a second, unobservable CPU model.
* **Per-user RLIMIT_NPROC and ulimits**: the table limit is the machine's, which
  is what a small device really hits first.
* **Block-device I/O queues and latency curves**: throughput is a rate per tick.
  Modelling queue depth would change no state a player can see.
* **Disk quotas and cgroup memory limits**: one machine, one disk, one RAM
  limit — the failures people actually meet.

# 取证 / 网管 / ISP / Provider 与 Law Enforcement (WS-1.13) — §34 + §35

## The model

Two spec sections share one question: who can find out what, from which records,
and who is allowed to act on it. §34 builds the organisations that answer for an
address — registry, ISP, hosting provider, datacenter, corporate SOC — and the
case ladder each of them runs. §35 builds the unit at the top: Law Enforcement,
with its own resources, its own permissions and its own blind spots.

Neither layer teleports. A desk works from its own records on its own machine,
and the unit works from what the networks disclose to it, in writing. "IP 查到谁"
stops where the world says it stops: an address resolves to an ASN, a range and
an abuse contact, and to a subscriber only when a network hands one over under a
request that is written down.

## What exists — §34

* **Eight kinds of desk**, each a real host with a real ASN, an MTA, an abuse
  mailbox, a service level and a portal: `registry` (RDAP/whois on :43, the
  allocation records and nothing else), `netcrest-noc` (the access ISP, AS64510,
  residential lines), `novapanel-abuse` + `novapanel-dc` (hosting provider and
  its datacenter, AS64520), `meridian-soc` (the enterprise SOC, AS64530, with
  `meridian-hq|dc|fs|ws`), and `le-cyber` (§35, AS64540, "GovNet").
* **A report is refused unless the reporter's own records hold the address.** An
  opinion, a note, or an address nothing on the reporter's machine saw is not
  evidence, and `abuse report` says which record is missing.
* **Findings come only from the desk's own AS**: a complaint from outside the
  network is hearsay, and a desk that has no address in its own range will not
  pretend to have logs about one.
* **The case ladder is a state machine on the world clock**: filed → triaged →
  notified (the subject is told, in their own mailbox) → enforced (port
  suspended for a 24 h term that lapses on its own) → referred (to the network
  that really answers for the address) → escalated (to the unit) → closed. Every
  rung writes a line to the case history, and the history *is* the audit trail.
* **Open cases absorb repeats**: a second report about an open case merges into
  it and records `AlsoReported`, so a desk does not open the same file twice;
  self-initiated tickets honour a six-hour cooldown.
* **A desk notices its own customers**: with the datacenter's `netflowd`
  exporter running, a provider sees its own addresses attacking other networks
  and opens a case without being told; stop the exporter and it goes back to
  knowing only what it is mailed.
* **The console is a Unix group, not a role flag**: `abuse queue|show|triage|act`
  requires an account in the organisation's own group on the organisation's own
  machine (`OnDeskStaff`), and shift accounts with their own passwords are what
  the job board hands out.
* **A carrier-grade NAT address is a dead end for everyone**: the provider can be
  asked about it, and no subscriber can be named for it.

## What exists — §35

* **The unit is state**: a ledger of investigator hours (24 to a shift, refilled
  at 1 per sim hour), a cap of three open files, an intake cursor into its own
  mailbox, the orders it has obtained, and its published blind spots.
* **Intake is a real mailbox**: a complaint mailed to `cases@cnu.gov.example`
  becomes a file — naming the complainant, carrying the complaint as evidence,
  and quoting the first address in the text. A complaint that names no address is
  written into the unit's log instead of being turned into a guess, and an
  address already on file is not filed twice.
* **The ladder is walked, not jumped**: intake → lawful request → disclosure →
  investigation → order → disposition. Requesting records costs 2 hours,
  investigating 8, an order 4; with no hours left the file waits for the next
  shift (a 30-minute push and a line in the unit's log, never a hidden sleep).
* **A lawful request is a written message to the network that announces the
  address**, and it is refused without a stated basis. The registry is refused
  too: it publishes allocation records, so it can name the network and is not the
  network to ask. An address no network announces ends the file with that reason.
* **A disclosure is a name or it is not a disclosure**: a platform that can name
  the rack but no customer on file ends the file ("there is nobody to name on an
  order"), and a NAT address ends it as carrier-grade NAT.
* **A warrant is the last rung and a real document**: `WR-%06d`, signed by an
  account in the unit's `agents` group, served into the network's own mailbox,
  with the subscriber told. It needs a named subscriber *and* a file standing on
  at least five artifacts (the complainant's evidence, the disclosures, the
  network's own records). Sampled off the console, `abuse act <case> warrant`
  refuses with exactly what is missing.
* **The unit's permissions are groups**: an intake analyst (`cases`, group `le`)
  reads, requests and closes; an investigator (`agent`, groups `le`+`agents`)
  opens the investigation and signs the order. Both refusals name the group.
* **What the unit cannot do is in the file**: it has no network of its own, so
  every record came from somebody else; it cannot reach a machine, so nothing is
  seized; it cannot make a NAT address resolve to a person. `abuse blind` prints
  the same list the code enforces.
* **Every file ends with a disposition**: charged, dropped or closed, with the
  reason on the record.

## The tests

`tests/abuse_test.go` (eight tests, 30+ assertions) drives §34 end to end.
`tests/law_test.go` (nine tests) pins §35: that a fresh file refuses both an
investigation and an order and that the unit has none of the network's powers;
that a lawful request is a real message in the ISP's mailbox and the disclosure
is what turns an address into an account; that the two permission levels are
Unix groups; that an order is served on the network and the subscriber is told;
that hours are a resource spent and refilled on the world clock; that intake
files a complaint with an address and refuses to guess at one without; that the
registry, an unannounced address and a rack with no customer on it each end a
file honestly; that a machine with nobody's name on it yields no order; and that
J-106/J-107 pass only after the work exists in the world.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green.
`tools/abuse_verify.sh` walks the live entry in eight sections: the household's
DNS fault repaired on the router, `whois` naming the network and never the
subscriber (including the CGNAT pool answer), the console refusals, eight real
rejected logins at the provider's desk, the world's own scanner found in the
gateway's flow record and reported with that record as evidence, the ticket read
back by its own filer, and then §35 from the other side: the household mails the
unit, the unit's intake turns it into a file, an analyst without the `agents`
group is refused the order, the file itself is refused the order, intake triages
and requests in writing, the hours ledger drops, `abuse blind` prints the unit's
limits, the network answers on its own service level, and a second file — about
the household's own line, complained about by the provider — is disclosed,
investigated, signed against and served, with the subscriber's own mailbox
carrying the notice.

## Not implemented on purpose

* **A teleporting raid**: an order ends at records retention on the line. The
  unit has no path to a machine in this world, which is the point of §35's blind
  spots; a seizure would need a device-level mechanic that does not exist.
* **Cross-border requests / MLAT**: there is one jurisdiction, so there is one
  unit. A treaty layer would decide nothing a file does not already decide.
* **Court dockets and trials**: `charged` is where the world stops. A trial would
  need a second party with standing, and there is none.
* **A warrant for a NAT address**: refused by design. The provider can be
  compelled for the retention records it holds, and it cannot name a subscriber
  it never had.
* **Random case generation**: every file has a complainant, an address and a
  record behind it. A desk that invented work would break §36's causality before
  §36 is even implemented.

# 现实网络中的因果关系 (WS-1.14) — §36

## The model

§36 asks for one property, not a feature: nothing in this world happens for no
reason. The failures people actually meet are conjunctions — an old disk *and*
load *and* uptime; an exposed port *and* a weak credential *and* nobody
watching; a bill that stayed unpaid. This workstream adds the chains that were
missing and keeps every one of them readable from state a player can inspect.

## What already existed (verified, not re-implemented)

* RAM → swap → OOM → a service killed, with the OOM reckoning written to the
  machine's own log (§17, `internal/core/resources.go`).
* A wrong resolver → names fail while IPs work (§13/§5: the dnsmasq fault the
  live scripts repair).
* An unpaid household account → the ISP suspends the uplink, mains are cut with
  it, and paying really restores service (`internal/core/npc.go`:
  `UtilityTrouble` / `PayUtilities`).
* A deterministic attacker: the world's scanner sweeps on its own schedule (a
  player who reads the logs can predict the next sweep), and every attempt goes
  through the same packet path as everything else.

## What this adds: an intrusion is a consequence

A working credential used to leave one log line. Now, when the world's own
attacker guesses a credential that works on a machine the internet reached, the
machine is *owned*, and the ownership is state:

* a **process** in the machine's own process table (an implant whose name is not
  a package),
* a **payload** on disk under `/tmp` that the process runs from,
* a **persistence line** in `/etc/cron.d` — the machine's real cron reads it, and
  that is the difference between cleanup and believing you cleaned up: kill the
  process and the line brings it back on the next tick, remove the line and the
  implant dies,
* a **beacon** the implant dials every 7 ticks: a real packet path to the
  attacker, with the flow recorded where it really is — on the far side, because
  a host cannot see its own egress any more than a real one can.

Whether anyone notices is the target's own §33 tooling, asked one at a time:
auditd sees the execution, aide sees the new file where it watches, rkhunter
sees the new executable in `/tmp`, the IDS sees the sweep that preceded it. A
machine with none of them is owned in silence, and `secstat compromise` says so
in as many words: `noticed by:  nothing`.

`secstat compromise` is the reader: every presence with its credential, its
three artifacts, its beacon count and what noticed it — plus which artifacts are
still on disk and running. `secstat compromise clean` removes the same three
things a player would remove by hand (process, payload, persistence line) and
keeps the record, because a machine that was owned once is worth remembering.
The record survives a save.

## The condition is the player's, not the seed's

The bot's credential list deliberately does **not** contain the seed's default
passwords. The world therefore does not own itself at boot: the conjunction
§36 names — reachable ∧ weak credential ∧ unmonitored — is something the player
builds by publishing a port (`uci set/add redirect`, commit, reload), leaving a
default password on the exposed box, and installing no sensors. The tests build
that conjunction explicitly and pin what follows; a live chain needs the
`passwd` verb (see "Not implemented on purpose").

## The tests

`tests/intrusion_test.go` (six tests) pins the chain: a working guess leaves the
three artifacts and the log line; killing the process alone does not end the
presence because the persistence line restarts it, while removing the line does;
the beacon is a real callback counted in the attacker's own flow record; a
machine with no sensors notices nothing while the same intrusion on a watched
machine lands in auditd's and the host monitor's alerts; a NATed household
machine is never in the scanner's target list and so is never owned; and
`CleanFoothold` really removes every artifact, with the record surviving a save.

## Not implemented on purpose

* **RDP as a service**: the sweep already tries 3389; there is no Windows stack in
  this world to answer it. The chain is identical over ssh, which is the door
  the world actually has.
* **Random failures**: wear-driven disk death is the next chain in this
  workstream, and it will be thresholds on real drivers (power-on hours, I/O
  load, age), never a dice roll.
* **A `passwd` verb**: changing a machine account's password is what makes the
  weak-credential half of the conjunction a *choice* rather than a test-only
  state. Until it exists, `chaos`-style "make yourself vulnerable" play is done
  through configuration the world already has (publishing a port, installing no
  sensors), and the credential half is exercised by the tests.

# passwd 与磁盘磨损 (WS-1.14 continued) — closing §36's two open items

WS-1.14 named two things it did not build: wear-driven disk death (thresholds
on power-on hours, I/O load, age — never a dice roll) and the `passwd` verb
(what makes the weak-credential half of the conjunction a *choice*). Both are
built here, each with its recovery half, because a chain without a fix is a
cutscene, not a system.

## passwd: the credential is a choice now

* `Device.ChangePassword` (`internal/core/devseed.go`, next to
  `refreshPasswd`) is the one path for password changes: it sets `User.Pass`
  and rewrites `/etc/passwd` + `/etc/shadow` + `/etc/sudoers`, then logs the
  `passwd` line. The account and the file can never disagree afterwards.
* The shell's `passwd [user]` (`internal/shell/game_cmds.go`, beside su/sudo)
  is setuid-like: changing your own password goes through the state update, so
  it never needs read access to the 0640 shadow file. Your own password needs
  the current one; anyone else's needs root; root skips the current check.
  Mismatch and empty are refused with the password unchanged, and failures are
  recorded the way su's are. Prompts go through `ReadPasswordLine`, so the
  live session and `runWithStdin` feed them identically.
* The two NPC hardening paths (`agents.go` patrol, `case.go` heat reaction)
  used to assign `u.Pass` by hand, leaving a stale hash in `/etc/shadow` —
  one credential store disagreeing with the login path. Both now go through
  `ChangePassword`.
* `tests/passwd_test.go` (six tests) pins it: own change works end to end
  (su with the new password succeeds, the old fails, shadow rewritten, syslog
  line), wrong-current and mismatch refuse without landing, non-root cannot
  touch another account, root can (including deliberately weak), and the
  change survives a save.

## wear: 老硬盘 + 高负载 + 长期运行, as counters

* `Rsrc` (`internal/core/resources.go`, which owns that shape) gains the
  three cumulative drivers — `DiskWrittenB` (fed by the same `NoteDiskWrite`
  funnel as every real write), `PowerOnTicks`, `HotTicks` (ticks spent
  powered while CPU demand exceeds capacity) — plus the `DiskFailAt` latch,
  the one-shot `DiskWarnAt`, and `DiskGraceUntil`. Counters never reset on
  reboot: `Boot` moves, wear does not. Fresh devices start their counter at
  the boot offset (36h of ticks), so `smartctl` agrees with `uptime`; a
  provisioned VPS resets it to zero with its `Boot`.
* `DiskHealth` derives PASSED/WARNING/FAILED with the reason naming the
  driver and its numbers (endurance = own size × 300 full-disk writes, warn
  at 60%; age warn/fail at 12,000/24,000 sim-hours; overload warn/fail at
  50/200 saturated sim-hours — all exported consts, stated as model limits).
  The wear tick runs inside `resourceTick`, so §17 still runs last and
  `engine.go` needed no edit.
* FAILED latches writes off with EIO at the single gate (`WriteGuest`,
  checked before ENOSPC because removing files is not the fix), at install
  time (`InstallFromView` returns the disk's own error in each manager's
  voice), and in the log path (`Logf` drops and counts like it does when
  full). Reads keep working — the data is not gone, which is what makes
  backup the real fix. WARNING is one log line plus an event, and changes
  nothing.
* `smartctl [-a|-H] /dev/sda` (`internal/shell/resource_cmds.go`) reads the
  same counters `DiskHealth` decides from; `fsck [/dev/sda1|/]` is root-only
  and repairs the latch with a 120-tick grace (`RepairDisk` in core, with the
  dropped-log reckoning shared with the disk-full path) without healing the
  wear — the health still fails, so the disk fails again on schedule. `df`,
  `vmstat` and `htop` surface the health the way they already surface
  fullness, with the FAILED-verdict and the bare health told apart so a disk
  inside its fsck grace is not misreported.
* `tests/diskhealth_test.go` (seven tests) pins the chain: fresh PASSED with
  sane counters, one-shot WARNING that still takes writes, FAILED refusing
  `echo`/`dd`/install with EIO while reads work (cause named in
  `dmesg`/`df`/`smartctl`), the overload driver failing on its own, the
  fsck permission boundary plus repair-then-relapse past the grace period,
  and counters plus latch surviving a save.

## A persistence bug found underneath — and fixed

`Device.GobEncode` declared `Rsrc` in its shadow struct and `GobDecode`
restored it, but the encode literal never assigned it: every save silently
dropped the whole resource accounting (swap, OOM count, load, and now wear).
One line (`Rsrc: d.Rsrc`) fixes it, which the wear save/load test pins.
Same class, NOT fixed (out of scope, different subsystem): `NATed` is in the
encode shadow but never assigned there and absent from the decode shadow, so
it does not round-trip either — see `internal/core/world.go:590`.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green
(266 pass, 0 fail — the 13 new tests included).
No new live-verify script: no entry or networking path was touched
(`passwd`/`smartctl`/`fsck` ride the existing sessions), so the unit + full
suite coverage is the verification.

## Not implemented on purpose

* **A new-disk mechanic**: `fsck` buys one sim-hour, it does not replace the
  platter. The honest long-term fix is backup (reads work) and migrate — a
  `disk replace` verb with billing, data loss when skipped, and restore would
  be its own workstream.
* **RDP as a service**: unchanged from WS-1.14 — no Windows stack to answer
  it, the chain is identical over ssh.

# P1 补完 — VM snapshots, phone power, cron respects power

Phase 1's one-word list was almost done; three functional gaps were not.
Each is closed here with its recovery half, and each fix is covered by the
normal path, a boundary and a recovery in tests.

## `vm snapshot` — the hypervisor half of "Snapshots"

Phase 1 says Snapshots; only the provider record had them (`vps snapshot`).
A hypervisor guest had no rollback at all — and worse, saving a world with a
guest present recursed (`VM.Host`/`VM.W` drag the whole world into every
guest) until the process died, which would have taken the live server down
with it on the next scheduled save.

* `VMSnapshot` (`internal/core/vm.go`) mirrors the provider's `Snapshot`
  shape (FS, users, installed, provenance) and lives on the guest.
  `World.VMSnapshot` copies a live guest; `World.VMRestore` replaces the disk
  wholesale on a stopped guest only, then leaves it stopped — starting it is
  a separate act. Cap 8, auto `snap-N` names, duplicate names refused.
* `VM.GobEncode`/`GobDecode` carry data only; `LoadWorld` (`resolver.go`)
  re-links `Host`/`W` the way `device.W` is re-linked. Old saves cannot
  contain a guest (writing one was what crashed), so there is no backcompat
  case to carry.
* Shell: `vm snapshot NAME [SNAP]`, `vm snapshots NAME`, `vm restore NAME
  SNAP` (`internal/shell/vm_cmds.go`), same contract words as `vps`.
* `tests/vm_test.go` (three tests): live snapshot + stopped rollback (new
  file gone, seeded file back, still stopped, boots after), restore refused
  while running (checked before the snapshot lookup) plus duplicate names,
  and save/load round-trip of guest + snapshot with a working restore
  afterwards — the round-trip is also the regression test for the crash.

## Phone power — a dead phone is off, and never a one-way door

The SMS layer already drained the battery, stopped sshd and refused the
spool at zero — but `Powered()` disagreed, so cron kept running and every
error said "up". Meanwhile `phone charge` only ran on the phone itself,
which a dead phone cannot reach.

* `phoneDead(d)` (`internal/core/power.go`) is the one question — a tracked
  phone at zero. `Powered()` returns false for it; `UnavailableReason()`
  says "battery empty". Reaching zero also drops `NetUp` (like every other
  power-off), charging restores it — the packet path and the shell gate read
  the same fact.
* `CronTick` skips unpowered devices: a dark machine runs no jobs, and the
  daemon re-arms from boot instead of stampeding the backlog. This also
  closes the same hole for power-cut PCs, which kept running their crontabs
  in the dark.
* `phone charge` works from any of the owner's machines (`PhoneFor`), the
  way `power` verbs are hands rather than shell commands. `phone status`
  stays on the phone itself.
* Tests: `TestPhoneBatteryLifecycle` (updated — it used to charge from the
  dead phone, which is exactly the one-way door) now pins dark/cause/rescue
  from the PC, and `TestCronDoesNotFireOnADarkMachine` pins silence while
  dark plus exactly-once firing on the next minute after restore.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green
(271 pass, 0 fail). No new live-verify script: no entry or networking path
was touched.

## Not implemented on purpose

* **VM ownership checks**: `vm start/stop/destroy` never checked owners, so
  the new verbs do not either — per-guest ACLs would be their own
  workstream, not a silent asymmetry between verbs.
* **A new-disk mechanic**: unchanged — see the wear section above.

# 黑市 (Phase 2) — the bazaar

Phase 2's remainder starts here, in dependency order: multiplayer, async PvP
and orgs all need more world first, but a black market chains only systems
that already exist — the BBS market board (classifieds with no execution),
the bank (no escrow, no player transfer verb), anonymous FTP (a drop box
with no market), and the evidence graph (no financial kind). So the bazaar
is listings, atomic swaps, a fee, and evidence, on one shady host.

## The model

`bazaar.neohome.example` (infra box, public IP, `marketd` on 8444, anonymous
vsftpd jail at `/srv/bazaar/drops`) holds `MarketState` (`internal/core/
market.go`, which owns that shape; the pointer lives on `World`). The
`market` shell builtin resolves and dials it like the BBS/IRC clients do —
a stopped marketd is no market. Two goods, both real state:

* **credentials** — `market sell-cred USER@HOST PRICE [--ftp]` reads the
  password over stdin and proves it with one real login probe from the
  bazaar host (`AttackLogin`, ssh/22 default, ftp/21 with the flag). The
  probe is logged on the target like any other attempt: verification leaves
  a trail, which is the point. Unreachable (a NATed box with no forward) or
  wrong is refused, not warehoused. A published forward is dialed through
  the router's front door (`marketFrontAddr`), the way the scanner reaches
  the same box — a forwarded credential lists, an unforwarded one honestly
  cannot.
* **dead-drop files** — bytes already on the bazaar (put there with the same
  `ftp -A` verbs as everything else), hash-pinned at list time.
  `market buy` flips the file world-readable so the buyer fetches it over
  ftp; a file that changed since listing delists instead of selling
  something the buyer never saw.

Buying is an atomic swap: re-verify, move money (seller proceeds, 5% fee to
the `bazaar` account, floor 25¢), deliver, record. The buyer's balance is
checked for the full price first, so a short buyer is refused before
anything moves. A good that fails re-verification (a rotated password, a
changed file, a dark host) delists with no charge. Fraud — re-listing
someone else's drop — is possible and permanently attributed: the game
answers fraud with evidence, not prevention.

Every listing and every sale files `market` evidence (a new kind; the old
readers filter by theirs, so nothing breaks) and both sides' bank histories
carry the trade. The seeded credential is the neighbour's live ftp password,
which her own heat reaction rotates — buy it after that and the re-probe
delists it in front of you. Trades heat the world through the same `Record`
as everything else, which closes the loop: heat → mara hardens → the goods
go stale.

`BazaarTick` (called from `World.Tick`) expires listings after 30 sim-days
and runs one deterministic collector: every six sim-hours mara buys the
cheapest affordable player listing. Same rules as any buyer.

## The tests

`tests/market_test.go` (eight tests): seeded board lists without leaking
the secret; credential buy end-to-end (exact cents each way, password works
on darkden afterwards, `market` evidence filed, goods leave the board);
rotation before purchase delists with no money moved; NATed and
wrong-password sells refused without landing; the full dead-drop chain over
real ftp (put → list → buy as a *different* account → perms flip → get);
a stopped marketd refuses connections; the collector buys the cheapest
player listing on its round; listings, secrets and sale records survive a
save.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green
(278 pass, 0 fail). No new live-verify script: no entry or networking path
was touched.

## Not implemented on purpose

* **Escrow with disputes**: the swap is atomic, so there is nothing to
  arbitrate. A confirm-receipt flow would be its own workstream.
* **Vendor reputation**: sales are attributed and permanent, which is the
  substrate reputation would be computed from — but no score is computed.
* **Egress relay as a good**: routing a buyer's traffic out of the
  bazaar's address would be genuinely valuable (and genuinely traceable),
  but it needs packet-path changes, not market records.

# 复杂任务框架 (Phase 2 + §44) — staged missions

Phase 2's "more complex tasks" and the §44 Mission template arrive together,
because they are the same gap: a `Job` was flat (one `Verify` string, one
payout, no prerequisites), and even the J-106→J-107 chain was held together
by Help prose rather than state. The ten world-state verifiers stay exactly
as they are — the new part is order, gates and installments around them.

## The model

`Job` (`internal/core/world.go`) gains the §44 fields, all empty on a legacy
one-shot job: `Requires` (job IDs that must be `Done` first), `Stages`
(`MissionStage{Name, Help, Verify, Pay}` — one of the verifiers `VerifyJob`
already speaks), `StageIdx`, plus `Solutions`/`Expected`/`Evidence`, which
are the template rendered in `job show`, not a second verifier.

* `AcceptJob` refuses work whose prerequisites are not done, naming the
  missing one. `PayJob` refuses staged jobs outright (`use: job advance`) —
  installments are the only way a mission pays.
* `AdvanceJob` checks only the *current* stage (a copy with its `Verify`,
  so the job's own finished verifier is never clobbered), then `payStage`
  moves that stage's pay and advances; the last stage completes the job.
  Skipping ahead is impossible because only the index is ever checked.
* The assistant works staged jobs one stage per six-tick cycle through the
  same `payStage`, after acting through the extracted `assistantAct` (the
  two fixes it always had: dns-fix, pkg-busybox). `TaskAssistant` refuses a
  mission containing any other stage kind up front, naming the stage — a
  task it cannot perform would sit in its queue forever. Skill grows once
  per finished mission, not per stage.
* Shell: `job advance ID`, stage progress in `job list` (`stage 2/3`),
  `[x]/[>]` marks plus requires/solutions/expected/evidence in `job show`.
* Seeds: J-108 (branch bring-up, requires J-101: ssh-up → web-up → clean,
  $15/$25/$10) and J-109 (abuse-desk certification, requires J-103:
  abuse-report → abuse-triage, $20/$30).

## The tests

`tests/mission_test.go` (four tests): the prerequisite gate names J-101 and
opens after it is really paid, lump-sum pay redirects to advance, and three
ordered installments land exactly; a planted router ban fails the current
stage with the world's reason, moves no money, then pays exactly on
recovery through to completion (legacy double-payout refused); the
assistant completes a two-stage mission over two work cycles with exact
total pay, one skill point and a really repaired fault, while an undoable
stage is refused by name at delegation; stage index and acceptance survive
a save and the mission finishes afterwards.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green
(282 pass, 0 fail). No new live-verify script: no entry or networking path
was touched.

## Not implemented on purpose

* **New verifier predicates**: every stage reuses the ten the world already
  speaks. A genuinely new check (mail-ready, backup-done) would be its own
  verifier, not a stage feature.
* **Retrofitted chains**: J-106→J-107 stay prose-linked. Bolting `Requires`
  onto shipped jobs would change payouts players may already be mid-way
  through; new missions carry the new semantics.

# P2 剩余:多人、组织、异步 PvP

Phase 2's enterprise half (§34/§35) was already built; what remained was
people: more than one human, crews with shared money, and fights between
them. All three arrive here, and the third is deliberately emergent — every
verb an async raid needs already existed, so what is pinned is that the
loop works across two citizens with attribution both ways.

## Citizens — roommates, not guests

The transport was always multi-user; the entries were not (both hardcoded
`FindUser("alex")`). `runPlayerSession` and the telnet login now land as
the player's own account on their own machine, with the old fallback kept.

`InvitePlayer` (`internal/core/players.go`) vouches a password-login
citizen: an existing player pays a $20 setup fee, the newcomer gets a
dedicated PC on the household LAN (allocated, never carried — a full pool
refuses), their own account, home, bank account and resolver line, and
lands broke with a temp password to change. Isolation is physical
(separate devices) one layer down and Unix permissions on shared ones;
`bank transfer` moves money between people (typo payees and overdrafts
refused).

## Orgs — a name, a roster and a real treasury

`Org` (`internal/core/org.go`, `Clans` on `World`): founder-owned,
invite-only (invites consumed on join), owner-only kick/withdraw/post/
cancel, walk-away leave (the owner cannot abandon a crewed org). Money only
enters through contributions; the treasury is a real bank account plus a
counter that mirrors it.

Contracts (`org post ORG TITLE $PAY VERIFY [HELP]`) escrow the full pay
from the treasury up front — a broke crew cannot promise. `PayJob` pays
contracts from the hold instead of minting (total money conserved, pinned
by test); `org cancel` refunds unaccepted work and reads `cancelled`,
never `paid`. `job show`/`list` already carry org contracts; the new
`trophy <device> <path>` verifier is proof-of-intrusion: the file must
exist AND carry the worker's tag. Planting takes real access, removing it
un-completes the work. Members may be players or known NPC handles
(roster fact, not agency).

Seed: midnight (daemon42 + mira-9, $200 treasury) with an open $100 trophy
contract on `/tmp/pwned` of darkden — reachable today over anonymous ftp,
untagged as seeded, deletable by its owner.

## PvP — the loop, not a system

No PvP-specific code was needed beyond the trophy verifier: break in with
stolen/bought/default credentials (the §36 conjunction, now a choice via
`passwd`), plant a tag, and the evidence already names the attacker's
citizen with the origin address for tracing. The test runs §43 in
miniature across two provisioned VPSes with ticks between the acts.

## The tests

`tests/org_test.go` (four): full lifecycle with permission boundaries at
every verb; conservation across post→pay from hold; cancel refunds and
reads cancelled while taken work is protected; the seeded trophy
end-to-end over real ftp (fail → plant → pay exact hold → treasury
funded). `tests/multi_test.go` (four): invite validation/fees/duplicates/
broke refusals plus resolver line; landing as self with separate devices
and working `passwd`; transfer verbs with typo/overdraft refusals; the
async loop both directions with named actors, origins and ticks between.

## Verified

`gofmt -l` empty, `go vet ./...` clean, `go test ./... -count=1` green
(289 pass, 0 fail). No new live-verify script: no entry or networking path
was touched (entry landing is covered by unit tests; live entries degrade
independently by design).

## Not implemented on purpose

* **Second households/subnets**: citizens share the household LAN like the
  MCP players do. A second subnet is a LAN-plan change (addr.go owns those
  numbers), not a citizenship change.
* **VM-style guest ACLs**: `vm` verbs never checked owners and still do
  not; crew membership gates money and contracts, not hypervisors.
* **Reputation/scores**: attribution is permanent and readable; no number
  is computed from it.

# 缺口 1:身份命令 + Household 实体（§4/§46）

账号表一直是真状态，但唯一的入口是 seed 和软件包：没有 useradd，没有
groupadd，连 `/etc/group` 文件都不存在。本批把 §4 的身份动词和 §46 的家
一起收尾。

## 账号即文件

* `Device` 加 `GroupIDs`（GID 注册表），`users.go` 拥有全部账号变更：
  `AddUser`（锁定口令、建家、UID 取 ≥1000 最小空闲）、`DelUser`（UID 0
  拒绝，`-r` 连家带邮箱，不带则文件留给数字 owner，私组永不随人走）、
  `AddGroup`（空组也进注册表）、`UsermodGroups`（`-aG` 追加、`-G` 只换
  次组，主组动不得；phantom 组拒绝——这就是 groupadd 存在的意义）。
* GID 只有一条规则（`GroupID`，唯一的解释者）：与用户同名的组拿该用户
  的 UID（Debian 私组），其余分配 ≥1000 且避开所有 UID/GID，持久化后永
  不漂移。`refreshPasswd` 每次重绘 passwd/shadow/sudoers **和 group**；
  `id` 经同一规则打印真实 GID，与文件一致。
* Shell：`useradd [-G] [-s]`、`userdel [-r]`（自己删自己拒绝）、
  `usermod -aG|-G`、`groupadd`、`groups`、`chpasswd`（管道批量，坏行
  报告不吞），变更走 root，失败走 sudoers 同款拒绝。`usermod -aG sudo`
  是真提权：sudoers 重绘后同一口令直接 uid=0。
* 持久化教训的第二次学费：`GroupIDs` 进 gob shadow 写了类型、字面量、
  回填三处（Rsrc 当年只写了两处，丢过整表）；外加 `LoadWorld` 给老存档
  全量重绘 `/etc/group`（幂等）。

## Household 一等实体

`Household{ID, Founder, Members, Router, NAS}`（`players.go`，`World` 持
map）：`InvitePlayer` 不再手写 router-alex/nas-alex，改从 inviter 所在
house 继承；成员随邀请进出；创始人 seed；老存档 load 时按 player 表回
填。LAN 子网仍归 addr.go——家是人和共用设备，不是地址。

## 测试

`tests/users_test.go`（七个）：锁定开户+四文件一致+`passwd` 解锁登录；
`usermod -aG sudo` 前后 sudo 实测提权；phantom 组拒绝；GID 不撞 UID、
人不走数不走、空组留文件、`id` 对账；删 root 两层拒绝；`-r` 与不带
的去留；house seed/继承/成员/过存档；chpasswd 批量两改一跳。

## 验证

`gofmt -l` 空、`go vet ./...` 干净、`go test ./... -count=1` 绿
（297 pass，0 fail）。无 live 脚本：未碰入口与网络。

# 缺口 6:四个小件（BBS 私信 / git 分支 / sftp -r / IMAPS）

Four items the workstreams marked "no storyline needs it yet" — the stories
arrived with PvP and crews, so here they are. Each is small, each reuses the
gates its neighbours already pass through.

## sftp -r

`get -r`/`put -r` walk real trees on both ends through the existing
single-file gates (`SFTPGet`/`SFTPPut` per file: same permission checks,
same evidence). Directories are made with the new `SFTPMkdir` (checked
writes, logged); a lone file through `-r` behaves like a plain transfer; a
refusal stops the walk with what already landed left in place. Tested both
directions plus the two failure shapes.

## BBS 私信

`bbs mail <user> <subject>` (same until-`.` body as posting) drops a 0600
letter into `/srv/bbs/mail/<to>/`; `bbs inbox` lists and `bbs readmail N`
reads the session user's own letters only. Anyone may send (the board's
open ethos); root on the box reads all, like mbox. The market board's "mail
me here" finally works — daemon42's seeded letter to alex waits in his box.

## git 分支（FF 合并）

Branches are ref files plus a symref HEAD: `git branch` (create/list/`-d`
with merged-check/`-D`), `git checkout [-b]` (dirty tree refuses — no
stash exists, so overwriting would lose work), `git merge` (fast-forward
only; diverged pairs are refused by name instead of inventing a conflicted
tree). `headOf` resolves symrefs with legacy bare ids passing through, so
old clones, server repos and every existing caller keep working; fresh
clones check out master. `git status`/`commit` print the real branch.
True 3-way merges stay out (see below).

## IMAPS :993

The mail hosts bind a second socket each: `imaps` on 993 with a leaf cert
for the LAN hostname, issued in seedTLS like every other TLS name.
`mutt -f imaps://user@home-pc/INBOX` handshakes against the client's own
trust store before any IMAP byte — an IP connection fails the name check
at the handshake, not at login — and `openssl s_client -connect host:993`
diagnoses it through the generic path. Stopping the daemon closes the port
like any other service.

## 测试与验证

sftp 递归双向+失败形、BBS 私信隔离+0600、分支全流程（FF/脏树/删保护/坏
名）+旧断言更新（`[main`→`[master]`，写死的分支名）、IMAPS 开箱+IP 名
称拒绝+停服。`gofmt` 空、`vet` 干净、全量绿（303 pass，0 fail）。

## Not implemented on purpose

* **True 3-way merges**: commits are single-parent; a merge commit with two
  parents plus content resolution is its own workstream. Diverged pairs tell
  you to delete a side, which always exists as a way out.
* **BBS accounts/passwords**: letters are addressed by shell identity, like
  posts. A passworded BBS login would be a second account system for one
  command's benefit.
