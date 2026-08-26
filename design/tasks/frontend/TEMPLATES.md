# Templates — Adding UI and Capabilities

**This file is where Aryan adds new ideas.** Everything below "Aryan's additions"
is his; the templates above are the format.

## Instructions to Claude Code

- A `UI-XX` entry with `Status: [ ] not built` is a **specification to implement**
  when its `Belongs to` episode is the current episode. It is not a suggestion.
- If a `UI-XX` entry belongs to an episode that has already passed, **do not
  build it opportunistically.** Report it and let Aryan schedule it.
- If an entry is ambiguous or contradicts `LAYOUT.md` / `THEME.md`, **stop and
  ask.** Do not resolve it by guessing.
- **Never invent a `UI-XX` entry.** Only Aryan writes these.
- Every new element uses the shared `Popup` / `Confirm` primitives, semantic theme
  roles, rounded borders, and a registered action. No exceptions without a written
  reason in the entry.

---

## Template: UI element

```
### UI-XX  <name>
Trigger:       <key, or action id, or the event that opens it>
Type:          popup | confirm | card | panel | inline | overlay
Purpose:       <one line — what problem it solves>
Layout:        <ASCII sketch>
MinSize:       <w×h>
Data needed:   <which backend query or event supplies it>
Actions:       <key → what happens, one per line>
Empty state:   <what it shows with no data>
Loading state: <what it shows while waiting>
Belongs to:    <F-episode>
Status:        [ ] not built
```

## Template: capability

```
### CAP-XX  <name>
ID:              <registry id, e.g. "coding">
Trigger:         @<name>            (basic mode, runs in Chat)
Advanced:        @<name> --advanced (opens its workspace, if any)
Agent config:    provider/model/effort · tools · scopes · limits
Primary surface: transcript | diff+plan | table | chart | document
Right pane:      Status · Task · <capability-specific panel>
MinSize:         <w×h>
Workspace?:      yes / no — justify: a capability earns a workspace only when
                 its primary surface is NOT a transcript
Belongs to:      <episode>
Status:          [ ] not built
```

### The capability contract

```
MUST     implement Workspace (Init/Update/View/Suspend/Resume/ID)
         render into the rect Compute hands it — never assume a size
         declare MinSize and refuse below it with real numbers
         use the shared input component, @ picker, popup and confirm
         put its tasks in the GLOBAL task list, not a private one
         survive unload while one of its tasks is running

MUST NOT own a provider or call one directly
         own memory or its own storage
         own a task queue
         write files (diffs only, applied by the user)
         talk to another workspace directly — emit; the root routes
```

Registration is one struct; palette entry, help text, leader binding, lifecycle
and task-tray integration all follow from it:

```go
Register(Capability{
    ID: "coding", Name: "Coding Assistant",
    Agent: codingAgentConfig,
    Trigger: "@coding", Advanced: "--advanced",
    Workspace: NewCodingWorkspace,   // nil = chat-only capability
    LeaderKey: "a",
    MinSize: Size{100, 30},
})
```

---

## Confirmed UI elements (specified during design)

Described in their episode files; listed so numbering starts from a known point.

```
UI-01  Command palette                F7
UI-02  Memory browser                 F11
UI-03  Settings + providers           F10
UI-04  @ picker (agents/files/memory) F8
UI-05  Task tray                      F9
UI-06  Result card                    F9
UI-07  Diff card                      F9
UI-08  Confirm dialog                 F7
UI-09  Context composition card       F6
UI-10  First-run provider chooser     F10
UI-11  Help screen                    F7
UI-12  Startup workspace chooser      F12
```

---

## Aryan's additions

> Add new `UI-XX` / `CAP-XX` entries below this line. Numbering continues from
> UI-12 and CAP-01.

### UI-13  Unload confirmation
Trigger:       workspace unload action (leader → x)
Type:          confirm
Purpose:       prevent losing an unsaved editor buffer by reflex
Layout:
```
╭ Unload Editor? ────────────────────────╮
│  2 files have unsaved changes.         │
│  Background tasks will keep running.   │
│                                        │
│      [ Unload ]      [ Cancel ]        │
╰────────────────────────────────────────╯
```
MinSize:       40×7
Data needed:   dirty-buffer count (local); running task count (tasks.list)
Actions:       `⏎` unload · `esc` cancel · `←/→` move selection
Empty state:   n/a — if nothing is dirty and no task runs, unload without asking
Loading state: n/a
Belongs to:    F12
Status:        [ ] not built

### UI-14  Quit confirmation
Trigger:       quit action (`^Q`)
Type:          confirm
Purpose:       quitting kills the backend, so running tasks genuinely die here —
               unlike unload, where they survive
Layout:
```
╭ Quit Point? ───────────────────────────╮
│  1 task is still running:              │
│    ⟳ Research  0:42                    │
│  Quitting will cancel it.              │
│                                        │
│      [ Quit ]        [ Cancel ]        │
╰────────────────────────────────────────╯
```
MinSize:       40×9
Data needed:   tasks.list (running only), dirty buffers
Actions:       `⏎` quit · `esc` cancel
Empty state:   nothing running and nothing dirty → quit immediately, no dialog
Loading state: n/a
Belongs to:    F12
Status:        [ ] not built
