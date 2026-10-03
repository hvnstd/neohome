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

Fast unit path (no server): `core.NewWorld()` + `shell.NewShell(w, dev, user, out, ip, tty)` + `ExecLine` — see `run()` helper in `tests/world_test.go:19`.

## Adding a subsystem (pattern)

1. State slot: add a pointer on `World` in `internal/core/world.go` (shape owned by one file, see comment at `world.go:61`).
2. Tick: implement `W.XTick()`, call it from `World.Tick()` in `internal/core/engine.go:190`.
3. Seed: wire initial state in `NewWorld()` (`world_init.go`), daemons/jobs must be world-visible files/services, not hidden maps.
4. Shell: add builtin in `internal/shell/<area>_cmds.go` operating on that state.
5. Test in `tests/<area>_test.go` via `Tick()` advancement (no wall-clock sleeps) + a `tools/<area>_verify.sh` for the live path if entry/networking is touched.
6. Persistence: `World.mu` is unexported so gob skips it — keep new fields gob-encodable (no channels/funcs in persisted structs).

Locking: live server shares one `*World`; hold `w.Lock()` around `Tick`/`Save`/session setup (see `cmd/neohome/main.go`, `ssh.go`).

## Gotchas

- `tools/*_verify.sh` hardcode `cd /workspace/neohome`, which does not exist in this environment (repo is at `/workspaces/264638881/neohome`). Run with an overridden workdir or fix the `cd` first; `all_verify.sh` chains them all. `killsrv.sh` kills by process-name pattern (`neohome`/`nh_*`).
- Ignored outputs: `/neohome` binary, `world.gob`/`*.gob`, `tools/_*.sh` scratch (see `.gitignore`) — never commit these.
- Live ports `:2024`/`:2222` may already be bound; entries degrade independently (server stays headless, engine keeps ticking).
