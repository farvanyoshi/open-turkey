# Open Turkey

A command-line (CLI) productivity blocker for Linux, backed by a `systemd` service.

Blocking is organized into **blocks** (named groups) of **sites** (domains) and
**apps** (process names). When a block is activated, a systemd daemon
continuously ensures the blocking layers stay applied.

## Build

```bash
export PATH="/usr/local/go/bin:$PATH"
make build            # CGO_ENABLED=0 go build -o open-turkey ./cmd/open-turkey
sudo ./install.sh     # compiles + installs binary, wrapper, sudoers, systemd and DB
```

Built without CGo because the SQLite driver is `modernc.org/sqlite` (pure Go). To
check that everything compiles: `CGO_ENABLED=0 go build ./...`.

## Architecture

- **Entrypoint** (`cmd/open-turkey/main.go`): lean, just delegates to `cli.Execute()`.
- **CLI** (`internal/cli/`): Cobra commands. `block.go` (block CRUD),
  `control.go` (`start`/`stop`/`unlock`/`status`).
- **Blocking layers** (`internal/blocker/`): `/etc/hosts` (atomic writes),
  iptables + ip6tables firewall (real-IP resolution via a small built-in DNS
  client that bypasses `/etc/hosts`; expanded DoH blocklist), browser policies
  for Firefox/Chrome/Chromium/Brave/Edge/Opera/Vivaldi (blocklist, allowlist,
  wildcard patterns, DoH disabled), and exact-name process killing. Domain
  validation (`validate.go`) guards against `/etc/hosts` injection.
- **Enforce** (`internal/enforce/`): turns the active blocks into layer inputs;
  `Apply` (CLI, on state change) and `Tick` (daemon, per cycle: schedule
  reconciliation + drift-only reapply). Single source of truth so CLI and daemon
  agree.
- **Schedule** (`internal/schedule/`): pure time logic for recurring windows.
- **Daemon** (`internal/daemon/`): systemd service, calls `enforce.Tick` every 5s.
- **DB** (`internal/db/`): SQLite at `/var/lib/open-turkey/open-turkey.db` (WAL);
  `OPEN_TURKEY_DB` overrides the path (tests only; the unit never sets it).
  `db.go` core + `features.go` (modes, patterns, schedules, time locks, schema
  upgrades).
- **Lock** (`internal/lock/`): raw-mode, paste-resistant typing challenge that
  gates a char-locked block's deactivation.

## System installation

`make install` copies everything outside the source directory: the real binary to
`/usr/local/bin/open-turkey-bin`, the `open-turkey` wrapper (does `exec sudo`), a rule
in `/etc/sudoers.d/open-turkey`, the `open-turkey.service` unit and the DB in
`/var/lib/open-turkey/`. The installed program doesn't depend on the project folder.

## Commands

Full usage and examples in `README.md`. Summary:
`block create [--allowlist] [--template T] | add-site | add-app | remove-site |
remove-app | add-pattern | remove-pattern | set-mode | export | import | list |
info | remove`, `start [--lock] [--lock-chars N] [--for D | --until HH:MM]`,
`stop`, `unlock`, `status`, `schedule add|list|remove`, and the hidden
`prepare-uninstall` (run by `make uninstall`).
