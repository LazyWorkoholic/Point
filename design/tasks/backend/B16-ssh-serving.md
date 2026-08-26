# B16 — SSH Serving

**Phase:** P2 · **Batchable:** no · **Depends on:** B15, F12

**Drop this episode if you reach the B12 checkpoint with ~3 weeks left.**

## Purpose

Serve Point over SSH so a second person gets a real TUI with no client install —
**topology B**: their own UI, their own runtime, the same database.

## Build

### Wish server (Go side)
- `charmbracelet/wish` handler starting a Bubble Tea program per connection.
- Host key generated on first run into `%LOCALAPPDATA%\Point\ssh_host_key`.
- Port from config, default **2222** — not 22, which needs elevation.
- Terminal size comes from the SSH session and feeds `Compute` unchanged. A guest
  on 80×24 gets the collapsed layout while the owner is on 200×50.

### Identity
```toml
[ssh]
enabled = false          # OFF by default
port = 2222
max_connections = 3

[[ssh.users]]
name = "aryan"
key_fingerprint = "SHA256:..."
role = "owner"

[[ssh.users]]
name = "friend"
key_fingerprint = "SHA256:..."
role = "guest"
daily_token_cap = 50000
ollama_base_url = "http://localhost:11435"   # optional reverse tunnel
```
Fingerprint the client's public key at connect; unknown key → reject with a clear
message. No passwords, no account system — SSH already solved this.

### Permissions — enforced in the **backend**
```python
@dataclass(frozen=True)
class Permissions:
    terminal: bool          # guest: False
    exec_process: bool      # guest: False
    fs_scope: str           # guest: "project"   owner: "anywhere"
    editor_write: bool      # guest: config, default False
    memory_write: str       # guest: "proposals"  owner: "accepted"
    memory_global: bool     # guest: False
    token_cap: int | None
```
Attached to the connection at authentication, checked at **every command entry
point**. A UI check is bypassable; a backend check is not.

### Runtime per connection
- One Python runtime per SSH session; killed on disconnect.
- `connection_id` on every envelope both ways (the B5 additive change from B6),
  so events route to the right client.
- Refuse past `max_connections` with a message — each runtime is 150–300MB.

### Shared database
- WAL is already on (B6). Add `busy_timeout` retries around write paths.
- `created_by` on sessions; the session list shows everyone's with an owner tag.
- Project memory is shared. **Global memory is owner-only.**
- **Nobody may cancel another user's task** in v1.

### Presence
The `presence` table, heartbeat on a timer, `presence.list()`. Rendered as a
popup, not a permanent pane.

### Credentials
Per request: the guest's own key if stored against their fingerprint → the
owner's key subject to the daily cap → refuse with instructions. Ollama needs no
key; a guest with `ollama_base_url` set uses **their** machine via a reverse
tunnel.

### File locks
B7's soft locks now do real work: two people, one file, heartbeat-backed lock,
take-over on stale, mtime check on save.

## Do NOT

- Implement shared-screen attach or co-editing (parked).
- Give a guest terminal, ExecProcess, or filesystem access outside the project.
- Allow a guest to write `accepted` or `global` memory.
- Enable SSH by default.

## Done when

1. Two SSH connections run tasks simultaneously; each sees only its own stream.
2. Both see the shared session list with owner tags, and shared project memory.
3. A guest's `terminal`, `exec_process`, and out-of-project reads are refused
   **by the backend** — verified by calling the commands directly, not through
   the UI.
4. A guest at their cap is refused before spending; the owner is unaffected.
5. An unknown key is rejected at connect.
6. `max_connections + 1` is refused with a message.
7. A guest opening a file the owner is editing is told it is held, with the
   owner's name and the lock age; take-over works once the lock goes stale.
8. The owner quitting while a guest is connected gives the guest a clean message,
   not a hang.

## Manual verification (PowerShell)

You can test everything from **one machine, two Windows Terminal tabs** — SSH to
`localhost`. Windows 10/11 ships an OpenSSH client; confirm with `ssh -V`.

```powershell
# generate a test key if you have none
ssh-keygen -t ed25519 -f "$env:USERPROFILE\.ssh\point_test" -N '""'
ssh-keygen -lf "$env:USERPROFILE\.ssh\point_test.pub"     # gives the SHA256 fingerprint
```
Put that fingerprint in `config.toml` as the `guest` user, and your normal key as
`owner`. Then, with Point running with `ssh.enabled = true`:

```powershell
# tab 2 — connect as the guest
ssh -p 2222 -i "$env:USERPROFILE\.ssh\point_test" localhost
```
**Expect:** the Point TUI, sized to that window.

```powershell
# unknown key
ssh-keygen -t ed25519 -f "$env:TEMP\unlisted" -N '""'
ssh -p 2222 -i "$env:TEMP\unlisted" localhost
```
**Expect:** rejected at connect.

**Permission checks — call the backend directly, not the UI:**
```powershell
uv run python cli.py --as guest terminal run "dir"
uv run python cli.py --as guest project read-file --file "C:\Windows\win.ini"
uv run python cli.py --as guest memory accept <id>
```
**Expect:** three refusals.
**Fails if:** any succeeds — a UI that merely hides a button is not a permission.

```powershell
# reverse tunnel (run from the "guest" side; here, the same machine)
ssh -R 11435:localhost:11434 -p 2222 -i "$env:USERPROFILE\.ssh\point_test" localhost
```
**Expect:** with `ollama_base_url` configured for that user, inference goes
through port 11435.

```powershell
# connection cap — open max_connections + 1 tabs
```
**Expect:** the last one is refused with a message.

## Handoff

State after this episode: Point is multi-user in the way that matters — shared
persistence, separate execution, least-privilege guests, honest resource limits.
Live shared execution stays deliberately out of scope, and **that scoping is
itself the answer** to "why not real-time collaboration?".

Record: whether Windows OpenSSH needed any special handling for the host key or
the PTY size messages.
