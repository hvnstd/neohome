# NeoHome — agent notes

Spec authority is `PROJECT.md` (verbatim user spec). `WORKSTREAM_NOTES.md` + `git log --oneline -15` show active workstream file ownership — check before touching cross-cutting files.

## Hard constraint (from spec)

Never execute host code: no real ELF/PE, no player input passed to host shell, no real Docker/VM per player. Everything is Go state in `internal/core` driven by builtins in `internal/shell`. Every new command/service must read or write real world state (no stub "installed successfully"), every anomaly needs a traceable cause, no wait-walls.

## Layout

- `cmd/neohome/` — real entry: telnet `:2024` + SSH `:2222`, `NEOHOME_WORLD` (default `world.gob`), engine ticks every 3s, saves every 12th tick.
- `internal/core/` — world state machine (`World`, `Device`, VFS, `Tick()` in `engine.go`, gob `Save`/`Load` in `resolver.go`, seed in `world_init.go`).
- `internal/shell/` — virtual shell builtins (`NewShell` + `ExecLine` + `RunLoop` in `shell.go`).
- `tests/` — the only package with tests. `tools/` — live end-to-end verify scripts + drivers (`live_drive.go`, `sshdrive/`).

Import direction is one-way: `shell` imports `core`; **`core` must never import `shell`** (cycle). Cross-layer hooks use registration, e.g. `core.SetCronExec` (see `internal/core/cron.go:32`) satisfied by `shell` init.

## Commands

```bash
go build -o neohome ./cmd/neohome
go vet ./...
go test ./...                                    # only neohome/tests has tests
go test ./tests -run TestName -v                 # single test, e.g. -run TestTmuxSessionsAreRealProcesses
```

Fast unit path (no server): `core.NewWorld()` + `shell.NewShell(w, dev, user, out, ip, tty)` + `ExecLine` — see the `run()` helper in `tests/world_test.go:19`, and `runWithStdin` (same file) when the command reads passwords or an interactive body from stdin.

## Adding a subsystem (pattern)

1. State slot: add a pointer on `World` in `internal/core/world.go` (shape owned by one file, see comment at `world.go:61`).
2. Tick: implement `W.XTick()`, call it from `World.Tick()` in `internal/core/engine.go:190`.
3. Seed: wire initial state in `NewWorld()` (`world_init.go`), daemons/jobs must be world-visible files/services, not hidden maps.
4. Shell: add builtin in `internal/shell/<area>_cmds.go` operating on that state.
5. Test in `tests/<area>_test.go` via `Tick()` advancement (no wall-clock sleeps) + a `tools/<area>_verify.sh` for the live path if entry/networking is touched.
6. Persistence: `World.mu` is unexported so gob skips it — keep new fields gob-encodable (no channels/funcs in persisted structs).

Locking: live server shares one `*World`; hold `w.Lock()` around `Tick`/`Save`/session setup (see `cmd/neohome/main.go`, `ssh.go`).

## Gotchas

- `tools/*_verify.sh` hardcode `cd /workspace/neohome` — fine when the repo is cloned at that path (it is in this environment); a clone elsewhere needs the `cd` overridden or the path exported first (`ftp_verify.sh` is the exception: it locates its own checkout and Go toolchain). `all_verify.sh` chains all 14 scripts and reports a `suspicious` count per script; review any non-zero (expected-error patterns — the scripted DNS fault's own dnsmasq log is legitimately "suspicious" in `live_verify`, and `ftp_verify.sh`'s closing `Connection timed out (filtered)` is the neighbour hardening, also expected). `killsrv.sh` kills by process-name pattern (`neohome`/`nh_*`).
- Household LAN numbers have ONE owner: `internal/core/addr.go` (`LANSubnet`, `LANGateway`, the DHCP band constants, `lanIP()`, `AllocLANStatic()` for runtime provisioning such as MCP, `ValidateLAN()` which runs at boot and panics on duplicates/pool-collisions). Never write a `10.77.1.x` literal anywhere else — a wrong seed fails loudly at `NewWorld`, in every test.
- `ftp` (client) is a `internal/shell` builtin, deliberately NOT a busybox applet: do not add it to `bbApplets` or `tests/testdata/busybox/APPLETS.txt`. The FTP *daemon* reads its own `/etc/vsftpd.conf` on every call (`core.FTPConfOf`) — never cache a parsed config; a player who edits the file must see the change on the next command. `listen_port` is intentionally unparsed (the service record's port is the one owner of that fact). The seeded `198.51.100.0/24` numbers live with the rest of the WAN plan in `internal/core/world_init.go` (`mara`'s forward, `darkden`), and only the `ftp` test asserts the drop box's `anon_root=/` vulnerability — keep it that way (fixing the seed would delete the storyline).
- Ignored outputs: `/neohome` binary, `world.gob`/`*.gob`, `tools/_*.sh` scratch (see `.gitignore`) — never commit these.
- `ssh`/`telnet` share one helper (`openRemoteSession` in `internal/shell/remote.go`): with a command it runs it on the target and returns the status, without one it hands over a nested shell using the *same* `bufio.Reader` (never wrap it again — the outer reader's buffer would starve the inner one). A session with no input stream must not enter the nested loop; that used to panic on `bufio.NewReader(nil)`.
- Trust that names a player must be evaluated against the *acting* device, not any device in the world: `core.AssistantKeyTrusted(src, dst)` takes both ends for that reason. The assistant node is key-only (`PasswordAuthentication no`, written by `SeedAssistantAccess`) because that is the directive `remote.go` actually enforces.
- IRC is a network service like the BBS: `internal/shell/irc_cmds.go` resolves `irc.neohome.example` and dials 6667 before touching `World.Chat`. Do not read or write chat in a command without passing that gate; do not cache the resolved address.
- Live ports `:2024`/`:2222` may already be bound; entries degrade independently (server stays headless, engine keeps ticking).
- Package trees live in *files* on the host that serves them: repository-relative paths are meaningless without the document root, so write and read them through the serving path (`core.WebRoot` / `ServeFile`; `pkgnet.go`'s `servedPath`). Metadata URLs are built from `Repo.URL` (the tree root), never from a source line's URL — a source line may name a subdirectory (OpenWrt feeds, Arch sections) or another host (third-party repos).
- `VFS.Read` on a **directory** returns `(nil, true)`: check `IsDir` before `Read` when scanning a directory (`SourcesForDevice` learned this the hard way). `MkdirAll` given a relative path used to loop forever on `path.Dir(".")` — it now bails at the root, and every caller passes an absolute path.
- Mirror state is derived, never assigned by hand: `MirrorTick` (called from `World.Tick`) recomputes `Repo.Status` from the bytes the mirror serves (`World.RepoIntegrity`) plus sync age. Only `StartMirrorSync`, the sync phases and `markStaleDebianTree` set state directly.
- The mirror's own crontab runs `mirror-sync <tree>` every 15 sim-minutes, and `World.Tick` fires it. A test that drives one sync by hand must stop the mirror's scheduler first (`quietMirrorCron` in `tests/pkgs_test.go`) or it will race the repair; `killSyncProc` matches the tree's args for the same reason.
- `Device.InstalledFrom` is per-device package provenance, written when the installing repository is known (`InstallFromView`/`InstallRendered`) and otherwise resolved from that device's *own* sources (`RepoForDevice`), never from a catalogue-wide name match.
- `w.Repos["main"]`/`["contrib"]` are gone: the catalogue is `debian`, `ubuntu`, `alpine`, `arch`, `fedora`, `openwrt`, `sashimi`. `vps create PLAN [hostname] [image]` takes the image (`ProvisionVPSWithOS`); a VPS ships root locked plus a sudo account, exactly like a cloud image.
- Exposure is configuration, never a flag: `Device.FW()` re-reads `/etc/config/firewall` + `/etc/config/upnpd` + `/var/run/miniupnpd.leases` (router) or `/etc/iptables/rules.v4` (host, Fedora/RHEL `/etc/sysconfig/iptables`) on *every* call. Never cache a parsed firewall config, never add a `Device.PortFwd`-style field back, and never seed a redirect for an NPC's convenience: §14 says the attack surface is what the player configured. A config that does not parse yields **no** rules (fail closed) and `uci` refuses to edit a file with parse errors, as real uci does.
- `uci set`/`add`/`delete` only *stage* (`World.UCIStaged`, the pending log is per config so `uci commit <c>` clears exactly its own lines); only `uci commit` writes the file, and `/etc/init.d/firewall reload` is a *check* verb that reports what is in force. If you add a command that changes the packet path, it must go through the same staging, or the two-verb model becomes a lie.
- A forward bypasses the target's LAN-only scope on purpose (that is what publishing a port means), but it does not bypass the target's own rules or the need for a service to be listening. `WPort == 0` on a redirect is a DMZ matching every port. Rendered files are the interface: `iptables` and the packet path both read `/etc/iptables/rules.v4`, and both `:INPUT DROP [0:0]` and `-P INPUT DROP` parse.
- `routerFor(dev) == dev` is not "the gateway": `upnpc` requires a gateway *upstream* of the caller, so it works from a LAN host and prints `no UPnP-enabled gateway on this network` on the router itself — that is correct, not a bug (delete mappings from a client). `upnpd.enabled=0` stops honouring leases without deleting them; `camctl status` reports configuration and reachability separately, because the gap between them is the whole point of a stale exposure.
- Addresses have ONE vocabulary: `core.ClassifyAddr` (`internal/core/addrs.go`) decides public/private/shared/ULA/link-local/loopback and every other subsystem asks it — never re-derive "is this public" from a string prefix. `Kind` has no `virtual` member: a virtual IP is a *secondary address*, not a kind, and lives in `Iface.Extra`.
- `routerFor` skips the device itself (a router is not its own upstream), so any caller that must judge a packet addressed to the household's **edge** has to fall back to `r = dst` when the destination's profile is `router` and `routerFor` returned nil. `Dial`, `Reach` and `ForwardTarget` all do this; a new call site that forgets it silently stops logging and stops following redirects (that bug cost 14 tests once).
- A datacenter profile's power is the customer's to control: `Powered()` returns `d.NetUp` for `core|infra|vps` (the house cannot darken a colo, but the panel can switch a node off — §12 启动/关机). `CutPower`/`RestorePower` still skip datacenters and BMCs; `setPowered` stays unexported and is reached through `World.VPSStart/VPSStop/VPSReboot`.
- A VPS is a `Device`, so a lifecycle verb must change the device, not print: `World.ReinstallVPS` wipes `d.FS`/`d.Users`/`Installed` and re-seeds the image (new password seeded with the rebuild count, so the old one really stops working) while keeping the addresses; `VPSResize`/`VPSAddDisk` require the node stopped, charge the household and move `NodeRecord.Monthly`; `VPSnapshot` copies the disk (a live copy, `cloneVFS`) and `VPSRestore` requires a power-off. Panel records live in `Provider.Nodes` (`NodeRecord`), and `LoadWorld` backfills them for old saves.
- Regions are networks, not labels: `DefaultRegions()` gives each NovaPanel datacenter its own AS (`asNova`/`asNovaUS`/`asNovaAP`), its own /24 in `publicBlocks` and its own /48, so `--region` changes the address, the `whois` answer and the real latency. `NodePriceCents` in core is the single price rule (the shell's `vmCost` delegates to it) — never price a node twice.
- rDNS is published per address: `answerVia` hands `.in-addr.arpa`/`.ip6.arpa` queries to `ptrAnswer`, which answers with the node's `RDNS` only for a block whose AS publishes a reverse zone (`AS.RDNS`); private, CGNAT and ULA addresses deliberately have no PTR. A lookup must never name a person (§13).
