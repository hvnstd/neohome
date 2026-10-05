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
