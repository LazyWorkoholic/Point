# B7 — Project System

**Phase:** P1 · **Batchable:** no · **Depends on:** B6

> **Unpaired from B6 on 2026-08-19.** B6 grew too large to share a verification
> pass. This is its own session now; read `completed/backend/B06-*.md` first to
> see what actually landed.

## Purpose

Give project scope a real identity that survives renames and moves, and put a
single guard on every filesystem path the system will ever touch.

## Build

### Project identity
- `project init` creates `.point\project.json`:
  `{ "project_id": "<generated>", "name": "<dir name>", "created_at": ... }`
- A row in `projects` maps `id → root_path` (last known). **Match on ID, not
  path**, so moving or renaming the folder keeps its memories.
- Resolution order: explicit `--project` → walk up from cwd looking for
  `.point\` → none.
- **No `.point\` means no project scope.** Return `project: null`; the UI greys
  project features rather than silently doing nothing.
- On init, append `.point/` to the project's `.gitignore` unless
  `git.share_project_memory = true`.

### Path guard — one function, used by everything
```python
resolve(path: str, project: Project | None) -> Path   # raises PointError(path_denied)
```
- Expands `~` and environment variables, resolves symlinks and `..`, normalizes
  to an absolute Windows path.
- **Case-insensitive containment check** — Windows paths are case-insensitive, so
  a naive `startswith` comparison lets `D:\PROJECTS\Point\..\other` through.
  Use `Path.resolve()` on both sides and compare case-folded.
- Rejects anything outside `project.root_path` unless `allow_outside_project`.
- With no project, only absolute paths, and only when `allow_outside_project`.

Every `@file` attachment, every tool, every editor open goes through this. It is
also the SSH jail — one implementation, not two.

### File index
- Walk the project tree honouring `.gitignore`
  (`index.respect_gitignore`, default true).
- Skip binaries (null bytes in the first 8KB) and files over `index.max_file_kb`
  (default 100).
- Skip `.venv`, `node_modules`, `__pycache__`, `.git` unconditionally — on this
  repo `.venv` alone would otherwise dominate the index.
- In-memory, built lazily on first use, invalidated on a coarse timer or an
  explicit refresh. **No filesystem watcher in v1.**
- Query: `project.list_files(prefix, limit)`.

### Soft file locks
```
acquire(project_id, rel_path, holder)  → ok | held_by(name, age_seconds)
heartbeat(...)                          every 15s while the file is open
release(...)                            on close / unload / quit
stale                                   heartbeat older than 60s → free
take_over(...)                          explicit; steals a stale or held lock
```

Plus a **stale-buffer check** for save: given `rel_path` and the mtime recorded
at open, report whether the file changed on disk. Saving over a changed file must
be refused, not silently done.

### Commands and queries
`project.init`, `project.resolve`, `project.list_files`, `project.read_file`
(guarded, size-capped, returns content + mtime + size), `project.lock.*`.

Add the matching `cli.py` subcommands.

## Do NOT

- Build the `@` picker UI (F8) or the editor (F13).
- Add a filesystem watcher.
- Implement RAG, symbol indexing, or parsing.

## Done when

1. `project init` a folder, add a memory bound to it, rename the folder, move it,
   resolve again → same `project_id`, memory still found.
2. `read_file` on a path outside the root is refused, **including** via `..` and
   via a differently-cased drive path.
3. With `allow_outside_project = true`, an absolute outside path is allowed and
   reported as outside.
4. `list_files` excludes `.gitignore`d paths, `.venv`, and `__pycache__`; with
   the toggle off, gitignored paths appear but `.venv` still does not.
5. Two holders: A acquires, B is told `held by A (12s)`; A stops heartbeating;
   after 60s B acquires cleanly.
6. The save-check reports `changed` after the file is touched externally.

## Manual verification (PowerShell)

```powershell
# 1. init
New-Item -ItemType Directory "$env:TEMP\proj" -Force | Out-Null
cd "$env:TEMP\proj"
git init | Out-Null
"secrets/" | Set-Content .gitignore
New-Item -ItemType Directory secrets -Force | Out-Null
New-Item -ItemType File secrets\a.txt, main.py -Force | Out-Null

cd D:\Projects\Point\backend
uv run python cli.py project init --path "$env:TEMP\proj"
Get-Content "$env:TEMP\proj\.point\project.json"
Get-Content "$env:TEMP\proj\.gitignore"
```
**Expect:** a `project_id`, and `.point/` added to `.gitignore`.

```powershell
# 2. gitignore respected
uv run python cli.py project list-files --path "$env:TEMP\proj"
```
**Expect:** `main.py` and `.gitignore`; **no** `secrets\a.txt`.

```powershell
# 3. identity survives a rename
Rename-Item "$env:TEMP\proj" "proj-renamed"
uv run python cli.py project resolve --path "$env:TEMP\proj-renamed"
```
**Expect:** the same `project_id` as step 1.

```powershell
# 4. path guard — all three must be refused
uv run python cli.py project read-file --path "$env:TEMP\proj-renamed" --file "..\..\Windows\win.ini"
uv run python cli.py project read-file --path "$env:TEMP\proj-renamed" --file "C:\Windows\win.ini"
uv run python cli.py project read-file --path "$env:TEMP\proj-renamed" --file "$($env:TEMP.ToUpper())\proj-renamed\..\..\Windows\win.ini"
```
**Expect:** three refusals naming the project root.
**Fails if:** any returns content, or the error is a raw traceback.

```powershell
# 5. locks
uv run python cli.py project lock acquire --file main.py --holder aryan
uv run python cli.py project lock acquire --file main.py --holder guest
```
**Expect:** first ok; second reports `held by aryan (Ns)`.

```powershell
# 6. stale save check
uv run python cli.py project read-file --file main.py     # note the mtime it prints
Add-Content "$env:TEMP\proj-renamed\main.py" "# changed"
uv run python cli.py project check-stale --file main.py --mtime <the mtime>
```
**Expect:** `changed`.

**Cleanup:**
```powershell
Remove-Item "$env:TEMP\proj-renamed" -Recurse -Force
```

## Handoff

State after this episode: projects have durable identity; there is exactly one
path-resolution function and everything must use it; locks and the stale-save
check exist and are testable from the CLI. No UI yet.

Note in `completed/`: the case-insensitivity fix in the path guard is
Windows-specific and easy to regress — say so explicitly.

Do not proceed until every check above passes.
