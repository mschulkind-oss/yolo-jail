package approvalwait

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// The exit statuses of `yolo wait r-<id>`. 0 and 75 are the two a caller acts on; every
// other one means "report it, do not retry".
const (
	ExitAllowed        = 0
	ExitUsage          = 64
	ExitUnavailable    = 69 // no record of the request here, or it cannot be read
	ExitSpent          = 72 // allowed, but its Allow once was already used or its window closed
	ExitAlreadyWaiting = 73 // another `yolo wait` holds this request
	ExitTempFail       = 75 // still pending when the timeout passed
	ExitDenied         = 77
	ExitExpired        = 78 // expired with no answer
	ExitClosed         = 79 // closed unanswered when the broker that filed it stopped
)

// noRecordGrace is how long a wait looks for a record that is not there yet before it says
// so: the broker writes it as it files the request, before the refusal reaches the agent.
const noRecordGrace = 10 * time.Second

// listWindow is how far back a bare `yolo wait` lists answered requests.
const listWindow = 24 * time.Hour

// lockDirName holds the jail's one-waiter-per-request locks, beside the host's records.
const lockDirName = "approval-waiters"

// Opts is one `yolo wait`.
type Opts struct {
	Workspace string // the jail's spelling of the workspace
	ID        string
	Timeout   time.Duration // zero: until the request's own expiry
	Out, Err  io.Writer
	Now       func() time.Time
	Poll      time.Duration
	Sleep     func(time.Duration) // tests
}

func (o Opts) withDefaults() Opts {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Err == nil {
		o.Err = os.Stderr
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Poll <= 0 {
		o.Poll = time.Second
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	return o
}

func (o Opts) say(format string, a ...any) { fmt.Fprintf(o.Err, "yolo wait: "+format+"\n", a...) }

// Wait waits for request o.ID's answer and returns its exit status.
func Wait(o Opts) int {
	o = o.withDefaults()
	if !ValidID(o.ID) {
		o.say("%s is not a request id such as r-0123456789abcdef0123456789abcdef; the refusal that asked for approval names it.", termsafe.Visible(o.ID))
		return ExitUsage
	}
	unlock, held, err := o.lock()
	if err != nil {
		o.say("cannot take this request's waiter lock (%v); run `yolo wait %s` again.", err, o.ID)
		return ExitUnavailable
	}
	if held {
		o.say("another `yolo wait %s` is already waiting on this request; its exit wakes whoever started it, so this one stops. Do not retry the command for this one.", o.ID)
		return ExitAlreadyWaiting
	}
	defer unlock()

	start := o.Now()
	var deadline time.Time
	if o.Timeout > 0 {
		deadline = start.Add(o.Timeout)
	}
	announced := false
	for {
		now := o.Now()
		rec, err := o.read()
		if err != nil {
			o.say("cannot read the record of %s (%s); ask the human whether they answered it with `yolo approve %s`.", o.ID, termsafe.Visible(err.Error()), o.ID)
			return ExitUnavailable
		}
		if rec == nil {
			if now.Sub(start) >= noRecordGrace {
				o.say("this workspace has no record of %s: the id is mistyped, or the host's yolo predates `yolo wait`. Ask the human to run `yolo approve %s` on the host, then, once they have, run the same command again.", o.ID, o.ID)
				return ExitUnavailable
			}
		} else if code, done := o.report(rec, now); done {
			return code
		} else {
			if deadline.IsZero() {
				deadline = rec.Expires
			}
			if !announced {
				fmt.Fprintf(o.Out, "%s is pending; waiting until %s for the host's answer.\n", o.ID, deadline.Local().Format(time.Kitchen))
				announced = true
			}
		}
		if !deadline.IsZero() && !now.Before(deadline) {
			fmt.Fprintf(o.Out, "%s is still unanswered. Give the human the command again:\n\n    %s\n\nand stop; run `%s` again once they say they have answered it.\n", o.ID, ApproveCommand(o.ID, ""), WaitCommand(o.ID))
			return ExitTempFail
		}
		o.Sleep(o.Poll)
	}
}

// report prints rec's answer and its exit status, or done false while it is pending.
func (o Opts) report(rec *Record, now time.Time) (int, bool) {
	switch state := rec.effectiveState(now); {
	case state == StatePending:
		return 0, false
	case state == StateAllowed && rec.Used:
		fmt.Fprintf(o.Out, "%s was allowed and its Allow once is already used: the command it allowed already ran. Do not run it again; report it.\n", o.ID)
		return ExitSpent, true
	case state == StateAllowed && rec.Lapsed:
		fmt.Fprintf(o.Out, "%s was allowed, but its window closed before the command ran. Running it again asks the human once more; report it, and ask before you do.\n", o.ID)
		return ExitSpent, true
	case state == StateAllowed:
		fmt.Fprintf(o.Out, "%s was allowed on the host. Run the same command again now, once.\n", o.ID)
		return ExitAllowed, true
	case state == StateDenied:
		fmt.Fprintf(o.Out, "%s was denied on the host. Do not run the command again; report it, and ask the human before trying anything else.\n", o.ID)
		return ExitDenied, true
	case state == StateExpired:
		fmt.Fprintf(o.Out, "%s expired with no answer. Report it; do not retry unless the human asks.\n", o.ID)
		return ExitExpired, true
	case state == StateRefused:
		fmt.Fprintf(o.Out, "%s was closed unanswered: the broker that filed it stopped (its jail stopped or restarted). Report it; running the command again files a new request.\n", o.ID)
		return ExitClosed, true
	default:
		fmt.Fprintf(o.Out, "%s is %s on the host. Report it; do not retry.\n", o.ID, termsafe.Visible(state))
		return ExitClosed, true
	}
}

// read reads o.ID's record, or nil when there is none yet.
func (o Opts) read() (*Record, error) {
	dir, err := openDir(o.Workspace)
	if err != nil || dir == nil {
		return nil, err
	}
	defer dir.Close()
	return readRecord(dir, o.ID)
}

// lock takes the one-waiter lock of o.ID: held is true when another wait holds it. The lock
// is an advisory flock, so it ends with the process that holds it, a killed one included.
func (o Opts) lock() (unlock func(), held bool, err error) {
	yoloDir := paths.WorkspaceStateDir(o.Workspace)
	yr, err := paths.OpenStateDirRoot(yoloDir)
	if err != nil {
		return nil, false, err
	}
	defer yr.Close()
	if err := yr.Mkdir(lockDirName, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, false, err
	}
	dir, err := paths.OpenStateSubdirRoot(yr, lockDirName, filepath.Join(yoloDir, lockDirName))
	if err != nil {
		return nil, false, err
	}
	defer dir.Close()
	f, err := dir.OpenFile(o.ID+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { f.Close() }, false, nil
}

// List prints this workspace's requests that still want something of the agent: each pending
// one, and each answered within the last day whose answer no command has acted on yet.
func List(o Opts) int {
	o = o.withDefaults()
	dir, err := openDir(o.Workspace)
	if err != nil {
		o.say("cannot read %s (%s).", filepath.Join(paths.WorkspaceStateDir(o.Workspace), DirName), termsafe.Visible(err.Error()))
		return ExitUnavailable
	}
	var recs []Record
	if dir != nil {
		defer dir.Close()
		d, err := dir.Open(".")
		if err != nil {
			o.say("cannot list the approval records (%s).", termsafe.Visible(err.Error()))
			return ExitUnavailable
		}
		names, _ := d.Readdirnames(-1)
		d.Close()
		slices.Sort(names)
		now := o.Now()
		for _, n := range names {
			id, ok := strings.CutSuffix(n, ".json")
			if !ok || !ValidID(id) {
				continue
			}
			rec, err := readRecord(dir, id)
			if err != nil || rec == nil || rec.Used || rec.Lapsed {
				continue
			}
			state := rec.effectiveState(now)
			if state != StatePending && (rec.Decided.IsZero() || now.Sub(rec.Decided) > listWindow) {
				continue
			}
			rec.State = state
			recs = append(recs, *rec)
		}
	}
	if len(recs) == 0 {
		fmt.Fprintln(o.Out, "No approval request of this workspace is pending or answered and waiting on you.")
		return 0
	}
	for _, r := range recs {
		fmt.Fprintf(o.Out, "%s  %-7s  %s  %s\n", r.ID, r.State, r.Source, termsafe.Visible(r.Command))
	}
	fmt.Fprintf(o.Out, "\nFor each allowed one, run its command again, once; for a pending one, start `yolo wait <id>` in the background.\n")
	return 0
}
