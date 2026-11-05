// Package approvalwait is the record of a broker request's answer that a jail can read, the
// in-jail `yolo wait r-<id>` that waits on it, and the one renderer of the hand-off block a
// refusal prints when a request needs the human's approval.
//
// THE RECORD IS WHERE A LATER JAIL CAN READ IT. A broker's request store lives in the host's
// own state directory, which no jail mounts, and the broker that filed a request forgets it when
// it stops. So every write of a request record also writes a small copy of its answer under the
// workspace's own state directory, `<workspace>/.yolo/approvals/<id>.json`, which the jail
// mounts: a `yolo wait` started after a restart answers from it instead of hanging on a broker
// that is gone.
//
// The record is a wake-up hint and nothing more. The jail can write that directory too, so a
// forged "allowed" record only makes the agent retry, and the broker still decides the retry
// from its own store. The host writes it the way the host-run mailbox is written: beneath a
// root that refuses a link at `.yolo` or `approvals`, through an exclusive, no-follow temporary
// name and a rename, and it never reads anything back from it.
package approvalwait

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// DirName is the records' directory under the workspace's state directory.
const DirName = "approvals"

// The states a record carries: the request store's own.
const (
	StatePending = "pending"
	StateAllowed = "allowed"
	StateDenied  = "denied"
	StateRefused = "refused" // closed unanswered when the broker that filed it stopped
	StateExpired = "expired"
)

// Record is what a jail may know of one request.
type Record struct {
	ID      string    `json:"id"`
	Source  string    `json:"source"` // the broker source, "git" or "github"
	State   string    `json:"state"`
	Command string    `json:"command,omitempty"` // the request's command, for a listing
	Filed   time.Time `json:"filed"`
	Expires time.Time `json:"expires"` // when a pending request stops waiting
	Decided time.Time `json:"decided,omitzero"`
	// Used is an Allow once its command already spent: retrying would ask again.
	Used bool `json:"used,omitempty"`
	// Lapsed is an Allow once whose window closed before any command spent it.
	Lapsed bool `json:"lapsed,omitempty"`

	// How it was answered, and by whom (docs/design/jail-history.md JH-D11). Every field is
	// additive: a record an older yolo wrote has none and reads as before.

	// Answer is the decision's kind: AnswerOnce, AnswerFor, AnswerAdd, AnswerRemember or
	// AnswerDeny; empty while pending, and for a request closed or expired unanswered.
	Answer string `json:"answer,omitempty"`
	// Via is who ran the `yolo approve` that answered it: ViaOnRequest when the user's
	// on_request hook ran it for this request, ViaHost otherwise.
	Via string `json:"via,omitempty"`
	// Until is when an AnswerFor stops covering the commands it allowed.
	Until time.Time `json:"until,omitzero"`
	// Jail is the jail that filed it, as the host names it.
	Jail string `json:"jail,omitempty"`
	// Hook is the on_request hook's part: HookRan, HookFailed or HookNone (none ran, so far).
	Hook string `json:"hook,omitempty"`
	// SpentAt is when the command that used its Allow once ran.
	SpentAt time.Time `json:"spent_at,omitzero"`
	// Reason is why it was closed unanswered: ReasonBrokerStopped.
	Reason string `json:"reason,omitempty"`
	// DenialNote is optional human feedback recorded with a denial, never an instruction to run.
	DenialNote string `json:"denial_note,omitempty"`
}

// The answers a record names (Record.Answer), the host store's own.
const (
	AnswerOnce     = "once"
	AnswerFor      = "for"
	AnswerAdd      = "add"
	AnswerRemember = "remember"
	AnswerDeny     = "deny"
)

// Who answered (Record.Via).
const (
	ViaOnRequest = "on_request"
	ViaHost      = "host"
)

// The on_request hook's part (Record.Hook).
const (
	HookRan    = "ran"
	HookFailed = "failed"
	HookNone   = "none"
)

// ReasonBrokerStopped is a request closed because the broker that filed it stopped.
const ReasonBrokerStopped = "broker-stopped"

// idPattern is a request id as the store mints it.
var idPattern = regexp.MustCompile(`^r-[0-9a-f]{32}$`)

// ValidID reports whether id spells a request id.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// LooksLikeID reports whether s is meant as a request id rather than a host-run label.
func LooksLikeID(s string) bool { return len(s) > 2 && s[:2] == "r-" }

// createFlags is every host create here: exclusive and no-follow, so a link, FIFO or file the
// jail planted at the temporary name fails the create instead of being followed.
const createFlags = os.O_WRONLY | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// Publish writes rec for the workspace at its host path. It creates no `.yolo`: a workspace
// that never launched a jail has no one to tell, and nil is returned. Every failure is
// returned for the caller to log; none may fail the request it describes.
func Publish(workspace string, rec Record) error {
	// Only the host writes a record: a broker's store is the host's, and a jail (which the
	// host marks with YOLO_VERSION) has none of its own to copy from.
	if workspace == "" || !ValidID(rec.ID) || os.Getenv("YOLO_VERSION") != "" {
		return nil
	}
	yr, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(workspace))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer yr.Close()
	if err := yr.Mkdir(DirName, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	dir, err := paths.OpenStateSubdirRoot(yr, DirName, filepath.Join(paths.WorkspaceStateDir(workspace), DirName))
	if err != nil {
		return err
	}
	defer dir.Close()
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := "." + rec.ID + ".tmp"
	_ = dir.Remove(tmp) // a leftover of a crashed write; Remove never follows a link
	f, err := dir.OpenFile(tmp, createFlags, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = dir.Remove(tmp)
		return werr
	}
	if err := dir.Rename(tmp, rec.ID+".json"); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	return nil
}

// readRecord reads id's record beneath dir, or (nil, nil) when there is none.
func readRecord(dir *os.Root, id string) (*Record, error) {
	data, err := paths.ReadRegularFileBeneath(dir, id+".json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if rec.ID != id {
		return nil, errors.New("the record names another request")
	}
	return &rec, nil
}

// openDir opens the workspace's records directory for reading, or (nil, nil) when there is none.
func openDir(workspace string) (*os.Root, error) {
	yoloDir := paths.WorkspaceStateDir(workspace)
	yr, err := paths.OpenStateDirRoot(yoloDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer yr.Close()
	r, err := paths.OpenStateSubdirRoot(yr, DirName, filepath.Join(yoloDir, DirName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return r, err
}

// effectiveState is rec's state at now: a pending request past its expiry is expired, which
// the store records only at its next transaction.
func (rec Record) effectiveState(now time.Time) string {
	if rec.State == StatePending && !rec.Expires.IsZero() && !now.Before(rec.Expires) {
		return StateExpired
	}
	return rec.State
}
