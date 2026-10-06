package entrypoint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
)

// miseuserecord.go is the JAIL'S HALF of the mise use record (internal/miseuse, a term coined
// for docs/design/minimal-disk-footprint.md OQ-DF4): a container jail's main process says, in
// the shared tool store, which installed tool versions its workspace uses, so that the host can
// reclaim the versions no jail on the machine has used for 30 days.
//
// THE MAIN PROCESS WRITES IT, because it is the one process that lives exactly as long as the
// jail and has nothing else to do: it boots, then holds (jailmain.go). So the record costs no
// session anything — the `mise ls` calls run beside the jail, never in front of an agent — and
// it can be refreshed for as long as the jail runs, which is what keeps a jail that runs for
// longer than the 30-day window from losing the tools it is using.
//
// THREE WRITES, and each one is there for a reason:
//
//  1. At the start of the hold, before provisioning, so a version the workspace already uses is
//     on record within seconds of the container starting. Another launch's housekeeping pass
//     can be deciding what to reclaim at that very moment.
//  2. Once provisioning has an outcome, so what `mise install` just added is named too (until
//     then it is protected by its own fresh install time).
//  3. Every miseuse.Refresh after that, for as long as the jail runs.
//
// A FAILURE IS RECORDED, NOT DROPPED. A record whose mise calls failed says so (Record.Unknown),
// and the host declines to reclaim anything while it is in force: a jail that used something
// nobody can name is the tri-state rule's "cannot tell", and a missing record would read as
// "used nothing".

// miseUseLsTimeout bounds one `mise ls`. Measured 0.2-5 s on a loaded machine with a thousand
// tracked configs; a call that runs past this is recorded as unknown rather than waited on.
const miseUseLsTimeout = 2 * time.Minute

// provisionOutcomePoll is how often the main process looks for provisioning's outcome.
const provisionOutcomePoll = 5 * time.Second

// miseUseRecorder writes one jail's record, under one name for the jail's life.
type miseUseRecorder struct {
	store     string // the tool store as this jail sees it: $MISE_DATA_DIR
	workspace string // YOLO_HOST_DIR, the record's Workspace
	name      string // miseuse.NewName, once
	now       func() time.Time
	// mise runs one mise command and returns its stdout.
	mise func(args ...string) ([]byte, error)
}

// newMiseUseRecorder is the recorder for this jail's environment.
func newMiseUseRecorder(e *Env) *miseUseRecorder {
	path := BootPath(e)
	env := envWith(os.Environ(), "PATH", path)
	// OFFLINE: "latest" then means the latest INSTALLED version, which is the one the shims run,
	// and no record waits on the network. A resolution against the remote list could call the
	// installed version the workspace is running "prunable" the day a newer one is published.
	env = envWith(env, "MISE_OFFLINE", "1")
	// A blocker generated for mise (a guardrails pack's) must not stop the record.
	env = envWith(env, "YOLO_BYPASS_SHIMS", "1")
	dir := e.WorkspaceDir()
	return &miseUseRecorder{
		store:     e.MiseData,
		workspace: e.Getenv("YOLO_HOST_DIR"),
		name:      miseuse.NewName(),
		now:       time.Now,
		mise: func(args ...string) ([]byte, error) {
			bin := lookPathIn(path, "mise")
			if bin == "" {
				return nil, errors.New("mise is not on this jail's PATH")
			}
			ctx, cancel := context.WithTimeout(context.Background(), miseUseLsTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Dir, cmd.Env = dir, env
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				msg := strings.TrimSpace(stderr.String())
				if i := strings.IndexByte(msg, '\n'); i >= 0 {
					msg = msg[:i]
				}
				if msg != "" {
					return nil, fmt.Errorf("`mise %s`: %w: %s", strings.Join(args, " "), err, msg)
				}
				return nil, fmt.Errorf("`mise %s`: %w", strings.Join(args, " "), err)
			}
			return stdout.Bytes(), nil
		},
	}
}

// record writes the record once: what the workspace uses now, or why that could not be told.
func (r *miseUseRecorder) record() error {
	rec := miseuse.Record{Workspace: r.workspace, Recorded: r.now()}
	installed, err1 := r.mise("ls", "--installed", "--json")
	prunable, err2 := r.mise("ls", "--prunable", "--json")
	current, err3 := r.mise("ls", "--current", "--installed", "--json")
	if err := errors.Join(err1, err2, err3); err != nil {
		rec.Unknown = err.Error()
	} else if needed, err := miseuse.Needed(r.store, installed, prunable, current); err != nil {
		rec.Unknown = err.Error()
	} else {
		rec.Installs = needed
	}
	return miseuse.Write(r.store, r.name, rec)
}

// recordMiseUseWhileHolding is the main process's recorder loop: the three writes the file
// comment names. provisioned reports whether provisioning has an outcome; wait sleeps for a
// duration and reports whether to go on (false ends the loop, which only a test asks for — in a
// jail the loop ends with the process).
func recordMiseUseWhileHolding(r *miseUseRecorder, provisioned func() bool, wait func(time.Duration) bool) {
	_ = r.record()
	for waited := time.Duration(0); !provisioned(); waited += provisionOutcomePoll {
		if waited >= miseuse.Refresh || !wait(provisionOutcomePoll) {
			break
		}
	}
	_ = r.record()
	for wait(miseuse.Refresh) {
		_ = r.record()
	}
}

// startMiseUseRecorder starts the recorder beside the hold. It writes nothing at all when the
// jail has no store to describe.
func startMiseUseRecorder(e *Env) {
	if e.MiseData == "" {
		return
	}
	r := newMiseUseRecorder(e)
	go recordMiseUseWhileHolding(r,
		func() bool { _, ok := readMainState(provisionOutcomeFile); return ok },
		func(d time.Duration) bool { time.Sleep(d); return true })
}
