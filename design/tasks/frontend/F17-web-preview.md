# F17 — Web Preview

**Phase:** P2 · **Batchable:** no · **Depends on:** F14 · **Backend:** —

## Purpose

Edit HTML/CSS/JS in Point and watch the page update in a real browser — without
Point ever rendering HTML, and without touching the Go↔Python protocol.

## The one-paragraph version

A "dev server" is a small program that serves your files over
`http://localhost:PORT` and tells the browser to reload when they change. For a
plain folder of files, **Point can be that program** — it is ~150 lines. For an
npm project, **the framework already is that program and does it better**, so
Point runs it and gets out of the way.

## Build

### Tier ① — static projects: Point IS the dev server

Trigger: the project has **no `package.json`**.

```go
// a net/http FileServer over the preview root, in its own goroutine
// + an injected <script> in served HTML that opens a websocket back
// + fsnotify on the preview root
// change detected → send "reload" down the websocket → page refreshes
```

- Port from config (`preview.port`, default 3000); if taken, try the next free
  one and report which was used.
- Inject the reload script only into `text/html` responses, immediately before
  `</body>`. Never rewrite anything else.
- The websocket carries one message: `reload`. Nothing else.

### Tier ② — npm projects: run theirs

Trigger: `package.json` exists with a `dev` (or `start`) script.

- Run it in the **F14 terminal pane** — not a hidden process, so build errors are
  visible where you already look.
- Watch its output for a `localhost:PORT` line and open the browser **once** at
  that port.
- Point does **no** file watching and **no** reload injection here. Vite's HMR
  swaps a single CSS file without even refreshing; ours would be strictly worse.
- If no port appears within ~15s, say so and offer the configured default.

### Reload policy
```
human edit   → reload on SAVE
agent write  → reload IMMEDIATELY
```
A half-typed `.container {` should not flash a broken page every keystroke; an
agent's write is already a complete change. One setting: `preview.reload_on =
"save" | "change"`.

**Interaction with autosave (F13/D90):** with `editor.autosave = "after_delay"`,
save and change converge — the file saves ~1s after you stop typing and the page
reloads then. That is the intended experience, and the 1000ms delay is what keeps
it from reloading on a half-written rule. With autosave off, `reload_on = "save"`
means an explicit `Ctrl+S`.

Do **not** set `reload_on = "change"` while autosave is on and expect something
different — they are the same trigger at that point.

### Watcher discipline — the only continuously-running cost here
- **`fsnotify`**, which uses `ReadDirectoryChangesW` on Windows — OS
  notifications, **not polling**. A polling watcher on a large tree burns CPU
  forever and is exactly the kind of thing this project exists to avoid.
- Scope the watch to the **preview root only**, not the whole project.
- Skip `.git`, `node_modules`, `.venv`, `__pycache__`.
- Debounce ~100ms — editors write several times per save.
- **Stop watching entirely when the preview closes.** Same rule as the right pane
  dropping its subscriptions when hidden.

### Actions and UI
```
preview.open      Ctrl+P (registered action, also in the palette)
preview.stop      stops the server and the watcher; closes nothing in the browser
preview.restart
```
Status bar shows `▶ preview :3000` while running, nothing when not. The right
pane does **not** gain a card for this — one status-bar token is enough.

### Config
```toml
[preview]
enabled   = true
root      = "."          # relative to the project root
port      = 3000
reload_on = "save"       # save | change
browser   = ""           # empty = the OS default
```

## Do NOT

- Render HTML, CSS, or JavaScript in the terminal. The browser is one of the best
  renderers ever built; use it.
- Reimplement HMR for npm projects.
- Build framework auto-detection, server supervision, restart policy, or HMR
  error parsing into the TUI — that is tier ③ and it is **parked**. It is a
  process supervisor that replaces typing one command once per session.
- Route any of this through the Python backend. It is a Go-side concern only.

## Why this cannot slow the app down

```
Go ↔ Python     JSON lines over pipes     the protocol, untouched
Go ↔ Browser    HTTP + one websocket      a separate socket entirely
```
Python never sees the preview. The pipe never carries a byte of it. When the
preview is off, **the goroutine does not exist**. While it is on, the cost is a
handful of requests at page load and one idle websocket carrying the string
`reload` occasionally — orders of magnitude less than the browser's own
rendering, which you would be paying regardless.

## Done when

1. A folder with `index.html` + `style.css` + no `package.json`: `Ctrl+P` opens
   the browser and shows the page.
2. Editing and **saving** `style.css` refreshes the page within ~200ms.
3. Editing without saving does **not** refresh (with `reload_on = "save"`).
4. An npm project: `Ctrl+P` runs the dev script in the terminal pane, finds the
   port, and opens the browser once. Editing a file triggers the framework's own
   HMR, not a Point reload.
5. Stopping the preview stops the watcher — verified by object/handle count, not
   by assumption.
6. With the preview running and idle, Point's CPU is indistinguishable from with
   it off.
7. The port being occupied produces a clear message and a working fallback port.

## Manual verification (PowerShell)

```powershell
# tier ① — build a scratch static site
New-Item -ItemType Directory "$env:TEMP\site" -Force | Out-Null
@"
<html><body><h1>Hello</h1><link rel=stylesheet href=style.css></body></html>
"@ | Set-Content "$env:TEMP\site\index.html"
"h1 { color: red }" | Set-Content "$env:TEMP\site\style.css"

cd D:\Projects\Point
go run .\frontend\cmd\point
```
Open `$env:TEMP\site` as the project, press `Ctrl+P`.
**Expect:** the browser opens and shows red "Hello".

Edit `style.css` in Point to `color: blue`, **save**.
**Expect:** the page turns blue within a moment, no manual refresh.
Type in the file **without** saving. **Expect:** nothing happens.

```powershell
# 6. idle cost
$p = Get-Process point; $before = $p.CPU
Start-Sleep -Seconds 60
(Get-Process point).CPU - $before
```
With the preview running and untouched.
**Expect:** near zero.
**Fails if:** it climbs — the watcher is polling, or the reload loop is firing.

```powershell
# 7. port conflict
# occupy 3000 with anything, then Ctrl+P
```
**Expect:** a message naming the fallback port actually used.

```powershell
Remove-Item "$env:TEMP\site" -Recurse -Force
```

## Handoff

State after this episode: a plain HTML/CSS/JS project is fully editable and
previewable inside Point with no npm anywhere, and an npm project gets its own
dev server run in the terminal pane. Nothing about the Go↔Python protocol
changed.

Record: the fsnotify debounce that actually worked, and the measured idle CPU
with the preview running.
