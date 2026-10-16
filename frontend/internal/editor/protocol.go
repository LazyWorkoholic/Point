package editor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lazyworkoholic/point/frontend/internal/protocol"
)

// The wire: ProjectResolve, then ListProjectFiles for the tree, then one
// ReadProjectFile per open.
//
// # Where the Editor's project comes from
//
// The Editor has no session and therefore no project_id -- B30 binds a
// project to a SESSION, and this workspace has none. So it resolves one
// itself, exactly as ProjectManager.resolve does for a bare path: walk up
// from the working directory looking for .point\, and accept that there may
// be nothing there.
//
// The working directory is the right starting point because it is the
// directory Point was launched from, which is the project the user means in
// every case this editor is for. A file opened from somewhere else entirely
// resolves its OWN project from its own path (openPath below), which is the
// task doc's note 4; the tree's root and a file's lock owner are two
// different questions and only the second one is per-file.
//
// No .point\ found means READ-ONLY, everywhere: a file with no project
// above it cannot take a lock, so it is read-only by construction rather
// than writable-but-unlockable. That is the same call
// _handle_write_project_file already makes by requiring a project instead
// of resolving one optionally.
//
// In practice the backend is stricter than that, and it is worth being
// precise about it rather than discovering it at the gate: with no project
// resolved, `resolve_path` REFUSES every read outright
// (managers\pathguard.py -- "no project scope means every guarded
// operation is off by default"), so a file with no .point\ above it cannot
// be opened at all, not merely opened read-only. The read-only path below
// stays as the defence for a reply that arrives anyway, which is what a
// later open-by-path surface passing allow_outside_project would produce.
//
// # The per-file walk-up is already done, by the backend
//
// _handle_read_project_file calls _resolve_optional_project, which reads
// the SAME `path` key the file is named by -- so a read with no project_id
// resolves the project by walking up from the FILE's own path. That is
// exactly the task doc's note 4, and it needs no second ProjectResolve
// from this side. A file in a different project than the tree's therefore
// opens correctly scoped, and the reply's own `outside_project` field is
// what says whether a project was found for it (onFile reads that rather
// than re-deriving it from the tree's root).

// treeLimit is how many paths one ListProjectFiles asks for.
//
// The backend's default is 200 and does `matched[:limit]`, which would
// silently truncate this repo's own tree (389 tracked files) -- a file tree
// missing its tail with no indication is worse than a slow one. 5000 is
// chosen to be past any plausible single project rather than tuned:
// _build_index already drops anything over index.max_file_kb and skips
// .venv, node_modules, __pycache__ and .git, so the list is files a person
// would open.
const treeLimit = 5000

type queryKind int

const (
	queryResolveProject queryKind = iota
	queryListFiles
	queryReadFile

	// F13b's five. Each reply has to be matched back to the DOCUMENT it
	// is about, which is why pendingReq carries a path: a FileLockResult
	// payload is `{ok, holder, age_seconds}` and names no file at all, so
	// correlating on the envelope id is the only way to know whose lock
	// it is.
	queryWriteFile
	queryAcquireLock
	queryHeartbeatLock
	queryReleaseLock
	queryTakeOverLock
	queryReloadFile
)

// pendingReq is one query in flight: what kind of reply to expect, and
// which document it concerns.
//
// path was added by F13b and is empty for the three queries that are about
// the workspace rather than a file (ProjectResolve, ListProjectFiles).
// doSend fills it from the payload's own "path" key rather than taking it
// as a parameter, so a new per-file query cannot forget to record it.
type pendingReq struct {
	kind queryKind
	path string
}

// localID mints a request id. protocol's own newID does the same job but is
// unexported, and chat\protocol.go makes the same call for the same reason:
// sixteen random bytes needs no shared helper to duplicate correctly.
func localID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("editor-%p", &b)
	}
	return hex.EncodeToString(b[:])
}

// send issues one query and remembers what kind of reply to expect.
//
// Like chat's, it calls Request rather than Send and never reads the
// channel: a correlated reply reaches both its waiter and the model, so the
// model still sees it through the ordinary EnvelopeMsg path. What Request
// buys is registering the waiter at all -- without one, every reply logs as
// "unroutable event", which is the client's signal for a real contract bug
// and the wrong label for a workspace's own query.
func (w *Workspace) send(cmdType string, kind queryKind, payload map[string]any) {
	w.sendFn(cmdType, kind, payload)
}

func (w *Workspace) doSend(cmdType string, kind queryKind, payload map[string]any) {
	if w.client == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		w.log.Warn("editor: marshalling a query", "type", cmdType, "err", err)
		return
	}
	id := localID()
	env := protocol.Envelope{ID: id, Type: cmdType, Payload: raw}
	if _, err := w.client.Request(env, 0); err != nil {
		w.log.Warn("editor: sending a query", "type", cmdType, "err", err)
		return
	}
	path, _ := payload["path"].(string)
	w.pending[id] = pendingReq{kind: kind, path: path}
}

// onConnected starts the sequence. Fired from every protocol.ConnectedMsg,
// first launch and every later reconnect alike, so a backend restart
// re-resolves the project and re-indexes the tree rather than leaving the
// pane showing a list from a process that no longer exists.
//
// A resolve already in flight is not re-sent. Init() resolves when the
// client is already connected, and switchWorkspace REPLAYS ConnectedMsg to
// a freshly loaded workspace for the reason its own doc gives (the real
// one arrived before this workspace existed) -- so on every load after the
// handshake both fire, and without this guard every open of the Editor
// cost two ProjectResolve and two ListProjectFiles round trips.
func (w *Workspace) onConnected() {
	w.backendDown = false
	if w.resolving {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		w.tree.note = "cannot read the working directory"
		return
	}
	w.resolving = true
	w.send(protocol.CmdProjectResolve, queryResolveProject, map[string]any{"path": cwd})
}

func (w *Workspace) handleEnvelope(env protocol.Envelope) tea.Cmd {
	req, ours := w.pending[env.CorrID]
	if !ours {
		return nil
	}
	delete(w.pending, env.CorrID)
	if w.client != nil {
		w.client.Release(env.CorrID)
	}

	if env.Type == protocol.EvError {
		return w.onError(req, env)
	}

	switch req.kind {
	case queryResolveProject:
		w.onProject(env)
	case queryListFiles:
		w.onFileList(env)
	case queryReadFile:
		return w.onFile(env)
	case queryWriteFile:
		return w.onWritten(req, env)
	case queryAcquireLock, queryTakeOverLock:
		return w.onLockResult(req, env)
	case queryHeartbeatLock:
		return w.onHeartbeatResult(req, env)
	case queryReleaseLock:
		// FileLockReleased carries `{released: bool}` and nothing
		// actionable follows either way: a false means this holder did
		// not hold it, which is already the state the editor is in by
		// the time the reply lands (releaseLock clears its own side
		// first, because the release is also sent on the way out of the
		// process where no reply will ever be read).
		return nil
	case queryReloadFile:
		return w.onReloaded(req, env)
	}
	return nil
}

// onError turns a refusal into one status line.
//
// A path denial and an unresolvable project both arrive as Error envelopes
// rather than as a slug (B32's split, which is about the WRITE path but
// applies to the read the same way), and both mean the editor should not
// have offered the open. They are shown and dropped; nothing retries.
func (w *Workspace) onError(req pendingReq, env protocol.Envelope) tea.Cmd {
	var p struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = env.UnmarshalPayload(&p)
	msg := p.Message
	if msg == "" {
		msg = "the backend refused the request"
	}
	w.log.Warn("editor: backend refused", "code", p.Code, "message", msg)
	if req.kind == queryListFiles {
		w.tree.note = msg
		return nil
	}

	// F13b: a failed WRITE has to clear its own in-flight flag, or the
	// document is permanently un-saveable -- saveDoc refuses to stack a
	// second write on one that never finished. An Error here means the
	// save should never have been offered (a path denial, an
	// unresolvable project), so autosave is stopped for this document
	// too rather than retrying once a second against a refusal that will
	// not change.
	if req.kind == queryWriteFile {
		if d := w.docByPath(req.path); d != nil {
			d.saving = false
			d.autosaveBlocked = true
		}
	}
	return w.say(msg)
}

// docByPath finds an open document by the absolute path the backend
// echoed. Nil when it has been closed since the query went out, which is
// an ordinary outcome rather than an error: a reply for a file that is no
// longer open has nothing to update.
func (w *Workspace) docByPath(path string) *doc {
	if path == "" {
		return nil
	}
	for _, d := range w.docs {
		if d.path == path {
			return d
		}
	}
	return nil
}

type wireProject struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
}

func (w *Workspace) onProject(env protocol.Envelope) {
	w.resolving = false
	var p struct {
		Project *wireProject `json:"project"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a Project reply", "err", err)
		return
	}
	if p.Project == nil {
		// A legitimate, common answer -- resolve() never raises for
		// "no project found". The tree has nothing to list and every
		// file that opens is read-only.
		w.projectID, w.projectRoot = "", ""
		w.tree.note = "no .point\\ above the working directory"
		return
	}
	w.projectID, w.projectRoot = p.Project.ID, p.Project.RootPath
	w.refreshTree(false)
}

func (w *Workspace) refreshTree(force bool) {
	if w.projectID == "" {
		return
	}
	w.send(protocol.CmdListProjectFiles, queryListFiles, map[string]any{
		"project_id": w.projectID,
		"limit":      treeLimit,
		"refresh":    force,
	})
}

func (w *Workspace) onFileList(env protocol.Envelope) {
	var p struct {
		Files []string `json:"files"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a ProjectFileList reply", "err", err)
		return
	}
	w.tree.build(p.Files)
	if len(p.Files) >= treeLimit {
		// Said out loud rather than truncated silently: the backend does
		// matched[:limit], so a project at the cap has a tree that is
		// missing its tail and looks complete.
		w.tree.note = fmt.Sprintf("showing the first %d files", treeLimit)
	}
	// Resume's open-file list is replayed once the tree exists, so a
	// restored tab set lands with its project already resolved.
	w.replayResume()
}

// openPath reads one file.
//
// project_id is sent when the path is inside the tree's own project.
// Anything else gets its own ProjectResolve first -- the per-file walk-up
// that decides whether that file can ever be editable.
func (w *Workspace) openPath(abs string) {
	for i, d := range w.docs {
		if d.path == abs {
			w.active = i
			w.focus = focusBuffer
			return
		}
	}
	// Opening a file that is already open is handled above; from here
	// on this is a genuine open, which will change the active document
	// when the reply lands -- see onFile.
	payload := map[string]any{"path": abs}
	if w.projectID != "" && relativeTo(w.projectRoot, abs) != "" {
		payload["project_id"] = w.projectID
	}
	w.send(protocol.CmdReadProjectFile, queryReadFile, payload)
}

func (w *Workspace) onFile(env protocol.Envelope) tea.Cmd {
	var p struct {
		File wireFile `json:"file"`
	}
	if err := env.UnmarshalPayload(&p); err != nil {
		w.log.Warn("editor: decoding a ProjectFile reply", "err", err)
		return nil
	}
	// `outside_project` is B32's own report of whether the backend
	// resolved a project for THIS file, which is the honest signal and
	// also the right one for a file in a different project than the
	// tree's. Re-deriving it from w.projectRoot would get that case wrong
	// in the direction that matters -- claiming no project, and so
	// read-only, for a file that has one.
	d := newDoc(p.File, w.projectRoot, !p.File.OutsideProject)
	prev := w.activeDoc()
	w.docs = append(w.docs, d)
	w.active = len(w.docs) - 1
	w.focus = focusBuffer
	w.note = ""
	w.applyResumeTo(d)

	// F13b: take the lock, and save whatever the previous document had
	// pending if that is the configured mode. Both happen on the OPEN
	// rather than on the request, because until the reply lands there
	// is no document to lock and no absolute path to lock it by.
	cmds := []tea.Cmd{w.acquireLock(d), w.autosaveOnFocusChange(prev)}

	if d.readOnly {
		cmds = append(cmds, w.say(d.name()+" opened read-only — "+string(d.roReason)))
	}
	return tea.Batch(cmds...)
}

// openSelected opens whatever the tree has selected, or expands it.
func (w *Workspace) openSelected() tea.Cmd {
	n := w.tree.selected()
	if n == nil {
		return nil
	}
	if n.dir {
		w.tree.toggle()
		return nil
	}
	if w.projectRoot == "" {
		return nil
	}
	w.openPath(w.projectRoot + "\\" + filepathFromPosix(n.path))
	return nil
}

// filepathFromPosix turns the wire's POSIX-relative path into a Windows
// one. The backend accepts either (resolve_path goes through pathlib), but
// sending the platform's own separator keeps the path the editor shows, the
// path it sent and the path the reply echoes back all the same string.
func filepathFromPosix(p string) string {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, '\\')
			continue
		}
		out = append(out, p[i])
	}
	return string(out)
}
