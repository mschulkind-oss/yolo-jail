package run

// sessionhangup.go is the launcher's half of a session's HANGUP (entrypoint/sessionhangup.go is
// the jail's; docs/design/jail-lifetime-last-session-wins.md §2.3 item 1, JL-D4, OQ-JL8).
//
// An attach used to run its exec under a proxy arm that restored the terminal, killed the exec
// client and exited. Killing the client does not end the process it started, since conmon keeps
// the exec session, so a closed attach terminal left its agent running in the jail with no
// terminal until the jail stopped. As OQ-JL8 ruled, the agent now ends with its pane: the
// attach's arm first asks the jail, with one bounded exec, to hang up that session's own
// processes, and only then kills its client. Nothing else in the jail is touched, and the jail
// keeps running for its other sessions; `yolo stop` stays the way to end them all.
//
// The session is named by an id this launch mints and hands the exec in
// entrypoint.SessionIDEnv, which the session's entrypoint records against its pid. Only a jail
// whose launch froze the session-hangup contract tag knows either half, so an attach to an older
// jail names no session and its arm says it cannot end one: an older entrypoint handed the
// hangup form would read it as a session's command and run a whole boot pass.
//
// The arm is the fresh launch's kind (launchSignalArm), so it covers an attach at a terminal and
// one without, where the plain spawn used to install no arm at all and a signal ended the
// launcher with its client and nothing else.

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// sessionHangupTimeout bounds the hangup's exec. A terminal multiplexer closing a pane sends its
// SIGKILL about half a second after its SIGHUP, and one exec round trip measured 56 to 71 ms, so
// the hangup normally lands well inside it; the bound is for a runtime that does not answer.
const sessionHangupTimeout = 2 * time.Second

// newSessionID mints a session's id: 16 random bytes, in hex, which is what the entrypoint
// accepts as a record's file name.
func newSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// attachSessionID is the id this attach names its session by, or "" when the running jail
// cannot hang a session up: one whose frozen contract tags lack session-hangup, or whose
// environment could not be read. The contract gate treats an unreadable jail as current; the
// hangup cannot, because an entrypoint without the form would boot on it.
func attachSessionID(envLines []string) string {
	if tags, known := jailContractTags(envLines); !known || !tags[contractSessionHangup] {
		return ""
	}
	return newSessionID()
}

// sessionEnvArgs is the exec's `-e` pair naming its session, or nothing for no id.
func sessionEnvArgs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{"-e", entrypoint.SessionIDEnv + "=" + id}
}

// hangupSessionCmd is the exec that hangs up the session named id: the jail's entrypoint in its
// hangup form, with no detach sequence, as on every exec (runtime.DetachKeysArgs), and no
// terminal.
func hangupSessionCmd(rt, cname, id string) []string {
	argv := append([]string{rt, "exec"}, runtime.DetachKeysArgs(rt)...)
	return append(argv, cname, JailEntrypointPath, entrypoint.HangupSessionArg, id)
}

// attachSignalArm is an attach's signal arm: on a SIGINT, SIGHUP or SIGTERM it hangs up this
// session's processes in the jail, puts the terminal's jail indicator back, kills the exec client
// and exits 128+N (launchSignalArm.terminate supplies the terminal and the kill). Never stopJail:
// the jail is its other sessions' too.
func (o *Options) attachSignalArm(rt, cname, sessionID string) *launchSignalArm {
	return armLaunchSignals(o.attachTeardown(rt, cname, sessionID))
}

// attachTeardown is the attach arm's onTerminate: the hangup, then the terminal's jail indicator,
// which the front door's deferred restore never puts back on this path (os.Exit skips it).
func (o *Options) attachTeardown(rt, cname, sessionID string) func() {
	return func() {
		o.Perf.Mark("terminate.signal")
		o.hangUpAttachSession(rt, cname, sessionID)
		o.restoreTerminal()
	}
}

// hangUpAttachSession asks the jail to hang up the session named id, within
// sessionHangupTimeout. It says so, on stderr, when it cannot, since what the session started then
// runs on in the jail with no terminal.
func (o *Options) hangUpAttachSession(rt, cname, id string) {
	if id == "" {
		o.pr(o.Stderr).print("[dim]This jail was started by a yolo that cannot end one session's " +
			"processes, so what this session started may go on in it until the jail stops.[/dim]")
		return
	}
	sp := o.Perf.Span("terminate.hangup_session")
	res := o.Exec(hangupSessionCmd(rt, cname, id), "", nil, sessionHangupTimeout)
	sp.End()
	if !res.Ran || res.Timeout || res.RC != 0 {
		o.pr(o.Stderr).printf("[dim]Could not end this session's processes in %s (%s), so what it "+
			"started may go on in the jail until the jail stops.[/dim]", cname, execFailure(res))
	}
}

// execFailure words why a bounded runtime call did not answer.
func execFailure(res ExecResult) string {
	switch {
	case !res.Ran:
		return "the runtime could not be run"
	case res.Timeout:
		return "the runtime did not answer within " + sessionHangupTimeout.String()
	default:
		return "the runtime exited " + strconv.Itoa(res.RC)
	}
}
