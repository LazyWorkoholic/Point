# Episode B23 — Agent Listing and Directory Browsing on the Wire ✅

**Two read-only wire additions for F8's `@` picker: `ListAgents`/`AgentList`
(plumbing over `AgentRegistry.list()`, already correct, already tested) and
`ListDirectory`/`DirectoryList` (a new, uncached, single-level directory
listing that works with or without a resolved project). No picker UI, no
fuzzy matching, no F8 code — that is Session 14's job once this lands.**

> Verified by Aryan on 2026-09-25 against `tasks/backend/B23-agent-and-directory-listing.md`'s
> seven "Done when" criteria, in a scratch `%APPDATA%`/`%LOCALAPPDATA%`
> profile per CLAUDE.md's env-var-override pattern — the real profile was
> never named in any destructive call, and was confirmed intact via `doctor`
> in a fresh terminal afterward.

---

## Where the code went

Transport plus one manager method, no new package.

```
backend\
├── runtime\
│   ├── events.py     + ListAgents (incoming) -- no payload fields; every
│   │                   registered agent, every time
│   │                 + ListDirectory (incoming) -- `path` (required),
│   │                   `project_id` (optional, explicit only), `cwd`
│   │                   (optional; used only when no project and `path`
│   │                   is relative)
│   │                 + AgentList / DirectoryList (outgoing)
│   └── runtime.py    + _handle_list_agents -- wraps `self.agents.list()`,
│                       serializes {id, name, description} per agent
│                     + _handle_list_directory -- resolves project from
│                       `project_id` ONLY (see decision below), then calls
│                       `self.projects.list_directory(project, p["path"],
│                       cwd=p.get("cwd"))`
├── managers\
│   └── project_manager.py  + list_directory() / _resolve_browse_target() /
│                       _read_dir() -- a plain `os.scandir`, one level deep,
│                       skipping `.venv`/`node_modules`/`__pycache__`/`.git`
│                       exactly like `_build_index()`'s dirnames filter, but
│                       NOT routed through `_build_index`'s cache: this is a
│                       live, uncached read of one folder, not the indexed/
│                       gitignore-aware/30s-TTL file tree `list_files()`
│                       builds for one resolved project
└── cli.py            + `agents list --wire` -- drives ListAgents through
                        `call()` over the event pipe; the plain `agents list`
                        (no flag) is UNCHANGED, still calls
                        `runtime.agents.list()` in-process
                      + `project browse <target> [--project ID] [--cwd DIR]`
                        -- new subcommand, the only CLI surface for
                        ListDirectory; `--cwd` defaults to the CLI process's
                        own `os.getcwd()` when omitted, standing in for "the
                        frontend already knows its own cwd and sends it
                        without being asked"
                      + "ListAgents": {"AgentList"}, "ListDirectory":
                        {"DirectoryList"} added to _EXPECTED_REPLY
```

`ListProjectFiles`, `ReadProjectFile`, `ResolveAttachment`,
`AttachmentManager` (attach/detach/resolve/preview), and the existing
`agents list` / `project list-files` / `project read-file` CLI commands are
**unchanged** — confirmed by direct regression run, not by inspection alone
(see Manual Test Log #5–#6).

---

## The two decisions this episode had to make

### 1. A directory *listing* is exempt from the `allow_outside_project` gate a file *read* uses

**`ProjectManager.list_directory()` always calls `resolve_path(...,
allow_outside_project=True)`, unconditionally — never reading
`project.allow_outside_project` from config.**

The gate exists to stop a file **read** from pulling content out of the
machine from wherever a session's project doesn't cover; defaulting that off
is right. A **listing** exposes names, not content, and F8's own spec
requires `@C:\`, `@~\`, `@\` to work as ordinary filesystem browsing with
**no project open at all** — sharing the read gate would make that basic
case refuse by default on every fresh profile (`project.allow_outside_project`
defaults `false`), which is the wrong default for a picker's primary
navigation mode.

This does not weaken anything already built: `ReadProjectFile`,
`CheckFileStale`, and every `AttachmentManager` guard keep defaulting to
`allow_outside_project=False`, untouched by this episode. `is_outside_
project()` is still called on every `ListDirectory` result and the
`outside_project` flag is still reported (B7 done-when #3) — being exempt
from the *refusal* does not hide the *fact* of being outside the project.

**Proven directly, not just documented** — Manual Test Log #6 below reads
and browses the identical outside-project path (`%WINDIR%\win.ini`'s parent)
through both endpoints in the same run: `project browse` succeeds by
default, `project read-file` on a file at the same location refuses by
default and only succeeds with the pre-existing `--allow-outside` flag,
which this episode did not touch or add.

### 2. `ListDirectory` resolves a project from an explicit `project_id` only — never by walking up from the browsed path

Unlike `ReadProjectFile`/`CheckFileStale`'s `_resolve_optional_project`,
which also walks up from the very path it operates on to auto-discover a
`.point\` marker, `_handle_list_directory` only resolves a project when the
caller passes `project_id` explicitly. A browse target is meant to sweep
across arbitrary, often project-unrelated locations (`@C:\`, `@D:\`);
auto-discovering a project from wherever the picker currently happens to be
pointed would make an identical absolute path behave differently depending
on ambient `.point\` markers no caller asked about. F8 passes its session's
already-resolved `project_id` explicitly when it has one, the same way
`AcquireFileLock` and its siblings require one rather than infer it.

---

## Manual Test Log (Episode B23)

All runs below used the `$env:APPDATA`/`$env:LOCALAPPDATA` scratch-override
pattern from CLAUDE.md's hard rules — never rename-aside, never the real
profile.

| Done-when | Result |
|---|---|
| #1 a wire request lists every registered agent, no session, no project | ✅ `agents list --wire` returned the 3 real built-in agents (`filler-explainer`, `general`, `research`) as `{id, name, description}` |
| #2 a wire request lists an arbitrary absolute path's children, distinguishing files/dirs, no resolved project | ✅ `project browse` against `%WINDIR%` → `outside_project: True`, 133 entries, files and `<DIR>` directories correctly distinguished |
| #3 same request works relative to a caller-supplied root, resolves `~` | ✅ `project browse "."` resolved against the actual shell cwd correctly |
| #4 both refuse cleanly — a reason, never a traceback | ✅ `project browse` against `win.ini` → clean `Not a directory: ...win.ini` refusal, no stack trace |
| #5 existing `ListProjectFiles`/`ReadProjectFile`/`ResolveAttachment` unchanged | ✅ regression run: `agents list` (plain, no `--wire`) and `project list-files --path ...`/`project read-file --path ... --file ... [--allow-outside]` all behave exactly as before this episode, byte-for-byte |
| #6 the `allow_outside_project`-for-listing decision, proven not just documented | ✅ **key proof**: `project read-file` against the exact same outside-project location (`%WINDIR%\win.ini`) that `project browse` succeeded on by default was refused by default with a clear error naming both the resolved path and the project root, succeeding only with the pre-existing `--allow-outside` flag (not touched or added by this episode) — confirms Decision 1 for the identical location, not just by argument |
| #7 the same always-skip list applies with no project open | ✅ `project browse "D:\Projects\Point"` — `.venv`, `node_modules`, `__pycache__`, `.git` all absent from the listing |

Real profile confirmed intact via `cli.py doctor` in a fresh terminal after
the scratch runs.

---

## What the next episode inherits

- **F8 (`design/tasks/frontend/F08-at-picker.md`) can now be built.** Both
  endpoints it depends on exist, are wired, and are verified: `ListAgents`/
  `AgentList` for the picker's Agents section, `ListDirectory`/
  `DirectoryList` for its filesystem-browse mode (`@`, `@C:\`, `@~\`, `@\`,
  `@.\`, `@..\`).
- **F8 must pass its own cwd explicitly on every no-project `ListDirectory`
  call.** The backend never assumes it — `cwd` is read straight from the
  payload and only ever falls back to nothing (a clean refusal), never to
  the backend process's own cwd, which per CLAUDE.md is always inside
  `backend\` and essentially never where the user launched `point.exe` from.
- **F8 should pass its session's already-resolved `project_id` explicitly**
  when it has one — `ListDirectory` will not infer it from the browsed path
  (Decision 2 above). If F8 wants project-relative browsing (e.g. paging
  through the open project's own tree with relative paths), it must supply
  `project_id` on the request.
- **Browsing and reading remain two different permissions at the same
  location.** A picker showing an outside-project folder's contents by
  default is not itself a green light to attach or read any entry in it —
  `ResolveAttachment`/`ReadProjectFile`'s existing `allow_outside_project`
  gate (config-driven, default off) still stands between "I can see this
  file's name" and "I can pull its content into a session," untouched by
  this episode.
