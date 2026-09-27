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
- **Blocking layers** (`internal/blocker/`): `/etc/hosts`, iptables firewall,
  browser policies (Firefox/Chrome/Chromium/Brave) and process killing.
- **Daemon** (`internal/daemon/`): systemd service, reapplies the layers every 5s
  in case anything is changed manually.
- **DB** (`internal/db/`): SQLite at `/var/lib/open-turkey/open-turkey.db` (WAL).
- **Lock** (`internal/lock/`): typing challenge that locks a block's deactivation
  (`--lock`), creating friction against impulsive deactivation.

## System installation

`make install` copies everything outside the source directory: the real binary to
`/usr/local/bin/open-turkey-bin`, the `open-turkey` wrapper (does `exec sudo`), a rule
in `/etc/sudoers.d/open-turkey`, the `open-turkey.service` unit and the DB in
`/var/lib/open-turkey/`. The installed program doesn't depend on the project folder.

## Commands

Full usage and examples in `README.md`. Summary:
`block create|add-site|add-app|remove-site|remove-app|list|info|remove`,
`start [--lock]`, `stop`, `unlock`, `status`.
