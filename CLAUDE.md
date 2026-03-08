# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

HTTP reverse proxy for the Anthropic API with a Fyne GUI. Captures request/response data, parses SSE streams, extracts rate limit headers, and displays live metrics.

### File Structure
- `main.go` — entry point, flag parsing, wires proxy + GUI together
- `proxy.go` — HTTP reverse proxy, SSE parsing, session capture, all exchange/log types
- `gui.go` — Fyne GUI model, rendering, layout, styles
- `gui_test.go` — GUI unit tests
- `storage.go` — session persistence, loading/saving exchange logs to disk
- `config.go` — configuration

## Build & Run

```sh
go build -o what-the-claude .
./what-the-claude -listen 127.0.0.1:6543 -remote https://api.anthropic.com -capture
```

Flags:
- `-listen` — local listen address (default: 127.0.0.1:6543)
- `-remote` — remote endpoint base URL (default: https://api.anthropic.com)
- `-capture` — write JSON logs per session to `sessions/session-<id>.json`

## Architecture

### GUI (Fyne)
- **Split-panel layout**: left = session list, right = request list for selected session
- **Stats bar**: request/session counts with configurable time range (1h/6h/12h/24h/7d/all-time)
- **Status bar**: rate limit usage (5h/7d with reset times), env var copy button
- **Session detail**: session ID, request count, "Copy resume cli command" button
- **Detail window**: opens on request click, shows time/model/status/events/tx/rx and request body size
- **Systray**: rate limit display in menu bar, click to toggle window

### Proxy
- `ServeHTTP` forwards requests, buffers bodies for logging
- Parses request body for model/user_id/account_id/session_id
- Parses SSE response streams into `sseEvent` array (event type + JSON data)
- Extracts Anthropic headers (rate limits, reset times, organization ID)
- Sends `logEntry` to GUI via channel

### Data Flow
1. HTTP request → proxy buffers req/resp bodies
2. Parse request metadata, SSE events, Anthropic headers
3. Send `logEntry` to GUI via `updateChan`
4. Write full `exchangeLog` JSON to session file (if `-capture` enabled)
5. GUI updates session list, request list, stats, and status bar

### Session Files
When `-capture` is enabled:
- Creates `sessions/` directory
- Writes one file per session: `sessions/session-<uuid>.json`
- Each line is a complete `exchangeLog` JSON object with timestamp, headers, bodies, events, rate limits

## Key Types

- `exchangeLog` — full request/response record for JSON logging
- `logEntry` — display-ready data sent to GUI (includes parsed events, headers)
- `sseEvent` — parsed SSE stream item (event type + JSON data)
- `parsedMetadata` — extracted model, user_id, account_id, session_id from request body
- `session` — groups logEntries by session ID, tracks activity times
- `guiModel` — main GUI state, holds sessions, filters, widgets

## Time Range Filter

The time range dropdown (top-left of stats bar) controls both:
- The stats display (requests/sessions counts)
- The session list filtering (only sessions with activity in the selected range)
