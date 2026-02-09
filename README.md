# Osiris - New Relic Incident Console (Go rewrite)

Lightweight terminal monitoring dashboard that lists New Relic entities, highlights those with active alerts, and lets you launch SSH or RDP sessions directly from the TUI.

## Key changes (recent)
- Rewritten in Go using `tview` (single static binary).
- Chunked UI population to avoid UI-thread starvation when displaying many entities.
- Background incident fetch (async) with REST fallback to classic Alerts Violations when NerdGraph fields are unavailable.
- `app.Suspend` calls for SSH/RDP hardened with panic recovery and guaranteed UI redraw on return.
- Heartbeat logger (`~/.osiris/debug.log`) added to help detect hangs.
- WSL-aware RDP: prefers Windows `mstsc.exe` when available under `/mnt/c/...`.

## Features
- Auto-refreshing server list from New Relic (configurable interval).
- Alert highlighting and concise alert messages in the details pane.
- Launch SSH (`s`) or RDP (`r`) from the UI; interactive sessions run outside the TUI and return cleanly.
- Vim-style search (`/`) and `n` to find next match.

## Installation & Build

Requirements: Go toolchain (1.21+ recommended).

Build:
```bash
go build -o osiris .
```

Run:
```bash
./osiris
```

On Windows use the WSL shell or build natively with a Go toolchain for Windows.

## Configuration
Create `~/.osiris/config` (or `%APPDATA%\.osiris\config` on Windows) with:
```
api_key=<YOUR_NEW_RELIC_API_KEY>
account_id=<YOUR_NEW_RELIC_ACCOUNT_ID>
refresh_interval=30
```

## Runtime & Logs
- Debug and heartbeat logs are written to `~/.osiris/debug.log` — useful when diagnosing freezes or API errors.

## Controls

| Key | Action |
|-----|--------|
| ↑/↓ | Navigate servers |
| s | SSH into selected server (suspends UI) |
| r | RDP into selected server (suspends UI; WSL-aware) |
| Space | Manual refresh |
| / | Search (type query when prompted) |
| n | Find next search match |
| q | Quit |

## Architecture (current)
- `main.go` — TUI, input handling, UI updates, heartbeat.
- `newrelic.go` — NerdGraph entity search, incident probing, REST violations fallback.
- `config.go` — config loading and debug logging.

## Troubleshooting
- If the UI appears blank after returning from an external RDP/SSH session, check `~/.osiris/debug.log` for heartbeat lines and `updateListView` messages. The app now forces a UI redraw after suspend-return; if issues persist paste the debug log when reporting.

## Next improvements
- Better REST→entity matching by GUID when available.
- Improve incident detail display and filtering.
- Add optional stack-dump watchdog if heartbeat stops.

