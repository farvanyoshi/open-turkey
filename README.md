# Open Turkey

A command-line productivity blocker for Linux, backed by a `systemd` service.

Open Turkey is an open-source, Linux-native alternative to [Cold Turkey](https://getcoldturkey.com/): it blocks distracting websites and applications and keeps them blocked, even against your own attempts to undo it on impulse. Where Cold Turkey is paid and centered on Windows/macOS, Open Turkey is free, built in Go, and designed around the way Linux actually enforces policy.

It organizes blocking into **blocks** — named groups of:

- **Sites** (domains, e.g. `youtube.com`)
- **Apps** (process names, e.g. `discord`, `telegram-desktop`, or Flatpak app IDs, e.g. `com.discordapp.Discord`)

When you **activate** a block, a daemon continuously ensures the blocking layers stay applied.

## How it works — 4 enforcement layers

Open Turkey doesn't rely on a single, easily-bypassed mechanism. Each active block is enforced on four independent layers, and a `systemd` daemon re-applies them every 5 seconds if anything is tampered with:

1. **`/etc/hosts`** — resolves blocked domains to `0.0.0.0` (affects every program, not just browsers). Written atomically, and every entry is validated so a crafted domain can't inject extra lines.
2. **Firewall (`iptables` + `ip6tables`)** — drops packets to the blocked sites' real IPv4 and IPv6 addresses, and drops DNS-over-HTTPS (DoH) to well-known resolvers so browsers can't resolve names over HTTPS and slip past `/etc/hosts`. The real addresses are looked up directly from public resolvers, bypassing the poisoned `/etc/hosts`.
3. **Browser enterprise policies** — managed `URLBlocklist`/`WebsiteFilter` policies for Firefox, Chromium, Google Chrome, Brave, Edge, Opera and Vivaldi, plus DoH turned off and locked in the browser itself. The user cannot disable these from inside the browser, not even in private/incognito mode.
4. **Process kill** — terminates blocked apps that are running (SIGKILL), matched by exact executable name so unrelated processes aren't caught. A Flatpak app ID matches every process in that app's sandbox, whatever its executables are called. Everything a blocked app has launched is killed with it — e.g. blocking `lutris` (or `steam`, `heroic`) also kills the games started from it. And when an app was opened from your desktop's menu or dock, every process in its desktop app unit is killed too, even ones that were detached from it, as long as the unit's app ID is the blocked name or ends in `.<name>` (so `lutris` covers `net.lutris.Lutris`, and `discord` covers the `com.discordapp.Discord` Flatpak). If you block a shell or session process itself (`bash`, `tmux`, `plasmashell`, …), only that process is killed, not everything started from it.

Because the hosts and firewall layers act below the browser, blocking works even for browsers installed via Snap or Flatpak (where the system-wide browser policy file may not be read).

> **Threat model.** Open Turkey creates *friction*, not a security boundary. Anyone with root can, with enough determination, tear the blocks down — that's intentional: it's your machine, and there is always a recovery path so you can't lock yourself out. The layers, the locked-block rules and the typing challenge exist to outlast an *impulse*, not a deliberate root-level effort. Blocking is IPv4/IPv6 by IP, but IP rules are best-effort: addresses change and CDNs are shared, so `/etc/hosts` and browser policy are the authoritative layers.

## Installation

Prerequisites:

- Linux with `systemd`
- `iptables` available
- Go (to build), at `/usr/local/go/bin` or otherwise on your `PATH`

Recommended:

```bash
sudo ./install.sh
```

This:

- compiles the binary (`CGO_ENABLED=0`, pure-Go SQLite via `modernc.org/sqlite`)
- installs `open-turkey` (a wrapper) and `open-turkey-bin` (the real binary) into `/usr/local/bin`
- adds a rule in `/etc/sudoers.d/open-turkey` so it runs without a password prompt
- installs and starts the `systemd` service `open-turkey.service`
- creates the database at `/var/lib/open-turkey/open-turkey.db`

The installed program is independent of the source directory — once installed, you can remove the project folder and it keeps working.

## Concepts

- **Block**: a set of sites/apps (and optional browser patterns) you block together. A block is either a **blocklist** (block its sites) or an **allowlist** (`--allowlist`: block everything *except* its sites, enforced at the browser layer).
- **Active**: a block currently in effect (the system is enforcing it).
- **Character lock (`--lock`)**: prevents `stop`. The only way out is `unlock`, which shows a string of random characters and requires typing all of it in one go, with at least 98% accuracy (at most 6 errors in the default 300 characters). Mistakes aren't highlighted (Backspace works, but you have to spot them yourself), paste is rejected, and `--lock-chars` sets the length (default 300 characters, several minutes of typing) — deliberate friction to outlast an impulse. Each character is a letter (upper or lower case) 60% of the time, a digit 35% of the time and a symbol 5% of the time; symbols can be any printable ASCII symbol on a US keyboard (`` !"#$%&'()*+,-./:;<=>?@[\]^_`{|}~ ``).
- **Time lock (`--for` / `--until`)**: the block cannot be removed *at all* — not even with the challenge — until the time passes. The strongest lock.
- **Schedule**: recurring windows (e.g. weekdays 09:00–17:00). The daemon turns the block on when a window opens and off when it closes; while a window is open the block can't be stopped. Starting **30 minutes before** a window opens, the block is frozen: it can't be edited or removed, and that schedule can't be removed, until the window closes.

While a block is *held* (char-locked, time-locked, in a schedule window, or within 30 minutes of one opening) it also can't be edited or removed, and `make uninstall` refuses. Everything is friction, not a security boundary — a determined root user can always recover (see the threat-model note above).

## Commands

### 1) Create and configure a block

```bash
open-turkey block create social-media
open-turkey block add-site social-media instagram.com x.com facebook.com
open-turkey block add-app  social-media discord telegram
```

Blocking an app also kills everything it launched — block `lutris` and the games started from Lutris close too.

For a Flatpak app, block it by its app ID (see `flatpak list --app`). This is more reliable than guessing the process name, since many Flatpaks run under generic names like `python3` or `electron`:

```bash
open-turkey block add-app social-media com.discordapp.Discord
```

Seed from a built-in template (`social-media`, `news`, `video`, `shopping`), or make an allowlist block:

```bash
open-turkey block create focus --template social-media
open-turkey block create work  --allowlist
open-turkey block set-mode work allowlist   # or blocklist
```

Add keyword/wildcard URL patterns (browser layer only):

```bash
open-turkey block add-pattern social-media '*casino*' '*://*/*ads*'
open-turkey block remove-pattern social-media '*casino*'
```

Share a block between machines:

```bash
open-turkey block export social-media > social.json
open-turkey block import social.json
```

Show details:

```bash
open-turkey block info social-media
```

List all blocks:

```bash
open-turkey block list
```

### 2) Activate / deactivate

Activate:

```bash
open-turkey start social-media
```

Activate with a character lock, or a time lock:

```bash
open-turkey start social-media --lock              # typing challenge to remove
open-turkey start social-media --lock --lock-chars 200
open-turkey start social-media --for 2h            # can't be removed for 2 hours
open-turkey start social-media --until 17:00       # can't be removed until 17:00
```

Recurring schedule (the daemon activates/deactivates it automatically):

```bash
open-turkey schedule add social-media --days weekdays --from 09:00 --to 17:00
open-turkey schedule add social-media --days sat,sun --from 10:00 --to 12:00
open-turkey schedule list social-media
open-turkey schedule remove social-media 1
```

Windows are same-day; for an overnight span (e.g. 17:00–05:00) add two windows (`17:00–23:59` and `00:00–05:00`). From **30 minutes before** a schedule's window opens until it closes, that schedule **can't be removed** — otherwise deleting it would end the block early or stop it from starting, defeating the point. Schedules whose windows aren't open or about to open can still be removed at any time, and you can always add new ones.

During that same period (30 minutes before any of a block's windows until it closes) the block itself is frozen: its sites, apps, patterns and mode can't be changed, and it can't be removed.

Show status:

```bash
open-turkey status
```

Deactivate (only if the block isn't held — see Concepts):

```bash
open-turkey stop social-media
```

Unlock a character-locked block (this also **deactivates** it). Time locks and open schedule windows can't be unlocked — they lift on their own:

```bash
open-turkey unlock social-media
```

To practice or test the challenge without touching any block (no root needed), run this from the source directory, optionally with a length:

```bash
./try-challenge.sh        # 300 characters
./try-challenge.sh 100
```

### 3) Editing a block's lists

Remove a domain from a block:

```bash
open-turkey block remove-site social-media instagram.com
```

Notes:

- If the block is **active**, Open Turkey re-applies the layers so the change takes effect immediately.
- If the block is **held** (character-locked, time-locked, in an open schedule window, or within 30 minutes of one opening), you **cannot** edit its sites/apps/patterns/mode. Clear the hold first (unlock, or wait for the time lock / schedule window to end), make your changes, then activate again.

Remove an app (process) from a block:

```bash
open-turkey block remove-app social-media discord
```

Remove an entire block (must be inactive, and not within 30 minutes of a schedule window):

```bash
open-turkey block remove social-media
```

## Service (daemon) and logs

The daemon is managed by `systemd`:

```bash
systemctl status open-turkey
systemctl restart open-turkey
journalctl -u open-turkey -f
```

## Uninstall

```bash
sudo make uninstall
```

Uninstalling first runs a guard that **refuses while any block is held** (character-locked, time-locked, in an open schedule window, or within 30 minutes of one opening), so uninstalling can't be used to sidestep a lock — clear the hold first. When it proceeds, it deactivates all blocks and removes every blocking layer (`/etc/hosts` entries, firewall rules, browser policies) so the system is left clean rather than blocked-with-no-tool.

## Intentionally not implemented

Cold Turkey's "Frozen Turkey" locks you out of the whole computer (screen lock / forced logout) for a set time. Open Turkey deliberately does **not** do this: a bug or a mistimed lock could leave you unable to use your own machine with no safe recovery. Time-locked blocks give most of the benefit — un-removable focus for a fixed window — without the lock-out risk. IPv6 is covered by the firewall's IP rules, but those rules are best-effort (shared CDN IPs, changing addresses); `/etc/hosts` and browser policy remain the authoritative layers.

## License

MIT — see [LICENSE](LICENSE).
