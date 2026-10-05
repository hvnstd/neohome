# Workstream notes

One section per workstream, oldest first. Each section owns its files; check
ownership there before touching cross-cutting files (`world.go` pointers,
`engine.go` tick calls, `world_init.go` seeds, `resolver.go` backcompat).

## Index (status 2026-10-05, tip `ea8eb1d`)

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

Verification status at tip: full `go test ./...` green, `go vet` clean,
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
