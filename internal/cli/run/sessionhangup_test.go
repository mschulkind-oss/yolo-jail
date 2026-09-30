package run

// sessionhangup_test.go pins the launcher's half of an attached session's hangup
// (sessionhangup.go; docs/design/jail-lifetime-last-session-wins.md JL-D4, OQ-JL8, JL-D52): which
// jails an attach names its session in, the exec that hangs it up, the arm's teardown, and that
// attachExisting runs its exec under that arm and names its session on the exec's argv.

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestAnAttachNamesItsSessionOnlyInAJailThatCanHangItUp: a jail whose frozen tags carry
// session-hangup gets a fresh 32-hex id per attach; one launched before the tag, or one whose
// environment could not be read, gets none, since its entrypoint would boot on the hangup form.
func TestAnAttachNamesItsSessionOnlyInAJailThatCanHangItUp(t *testing.T) {
	cur := strings.Split(currentJailEnv, "\n")
	a, b := attachSessionID(cur), attachSessionID(cur)
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(a) || a == b {
		t.Errorf("ids %q and %q: want a fresh 32-hex id per attach", a, b)
	}
	for name, env := range map[string]string{
		"a jail launched before the tag": preGateEnv,
		"an explicit list without it":    "YOLO_VERSION=1\n" + entrypoint.ContractTagsEnv + "=entry-channel\n",
		"an unreadable environment":      "",
	} {
		if id := attachSessionID(strings.Split(env, "\n")); id != "" {
			t.Errorf("%s: named its session %q", name, id)
		}
	}
	if got := sessionEnvArgs(""); got != nil {
		t.Errorf("no id, but the exec gets %q", got)
	}
	if got := sessionEnvArgs("abc12345"); !slices.Equal(got, []string{"-e", entrypoint.SessionIDEnv + "=abc12345"}) {
		t.Errorf("the exec's pair is %q", got)
	}
}

// TestTheHangupIsTheEntrypointsHangupForm: `<rt> exec --detach-keys= <cname>
// /opt/yolo-jail/bin/yolo-entrypoint --yolo-hangup-session <id>`, with no terminal.
func TestTheHangupIsTheEntrypointsHangupForm(t *testing.T) {
	got := hangupSessionCmd("podman", "yolo-ws-1", "abc12345")
	want := []string{"podman", "exec", "--detach-keys=", "yolo-ws-1", JailEntrypointPath,
		entrypoint.HangupSessionArg, "abc12345"}
	if !slices.Equal(got, want) {
		t.Errorf("hangup argv %q, want %q", got, want)
	}
	if got := hangupSessionCmd("container", "c", "abc12345"); slices.Contains(got, "--detach-keys=") {
		t.Errorf("Apple Container got podman's flag: %q", got)
	}
}

// TestTheAttachTeardownHangsUpItsSessionAndNeverStopsTheJail: the arm's teardown runs the hangup
// exec, bounded, then puts the terminal back. It runs no stop, since the jail is its other
// sessions' too. A failed hangup says what it could not do; a jail that cannot hang a session up
// is said to be one, and nothing is exec'd into it.
func TestTheAttachTeardownHangsUpItsSessionAndNeverStopsTheJail(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	var stderr bytes.Buffer
	o.Stderr = &stderr
	var calls [][]string
	var bound time.Duration
	rc := 0
	o.Exec = func(argv []string, _ string, _ []string, timeout time.Duration) ExecResult {
		calls = append(calls, argv)
		bound = timeout
		return ExecResult{Ran: true, RC: rc}
	}
	restored := 0
	o.RestoreTerminal = func() { restored++ }

	o.attachTeardown("podman", "yolo-ws-1", "abc12345")()
	if len(calls) != 1 || !slices.Equal(calls[0], hangupSessionCmd("podman", "yolo-ws-1", "abc12345")) {
		t.Fatalf("the teardown ran %q, want the one hangup exec", calls)
	}
	if bound != sessionHangupTimeout {
		t.Errorf("the hangup is bounded by %s, want %s", bound, sessionHangupTimeout)
	}
	if restored != 1 {
		t.Errorf("the terminal was put back %d times, want 1", restored)
	}
	if stderr.Len() != 0 {
		t.Errorf("a hangup that worked printed: %s", stderr.String())
	}

	rc = 125
	o.attachTeardown("podman", "yolo-ws-1", "abc12345")()
	if !strings.Contains(stderr.String(), "Could not end this session's processes in yolo-ws-1") ||
		!strings.Contains(stderr.String(), "exited 125") {
		t.Errorf("a failed hangup must say so:\n%s", stderr.String())
	}

	calls, stderr = nil, bytes.Buffer{}
	o.Stderr = &stderr
	o.attachTeardown("podman", "yolo-ws-1", "")()
	if len(calls) != 0 {
		t.Errorf("a jail that cannot hang a session up was exec'd into: %q", calls)
	}
	if !strings.Contains(stderr.String(), "cannot end one session's processes") {
		t.Errorf("an older jail's arm must say it cannot end the session:\n%s", stderr.String())
	}
	for _, c := range calls {
		if len(c) > 1 && c[1] == "stop" {
			t.Errorf("the attach's teardown stopped the jail: %q", c)
		}
	}
}

// TestAnAttachRunsItsSessionUnderItsOwnArm is the call-site pin, read from attachExisting: the
// session's id is minted from the jail's environment and put on the exec's argv, the arm is built
// with that id before the exec, the exec runs under it (runArmedSession, never the proxy's own
// arm through runWithProxy), and the arm is disarmed once the exec returns.
func TestAnAttachRunsItsSessionUnderItsOwnArm(t *testing.T) {
	fd := funcDecl(t, "run.go", "attachExisting")
	pos := map[string]token.Pos{}
	var disarm token.Pos
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := skelCallee(call)
		switch name {
		case "attachSessionID", "sessionEnvArgs", "attachSignalArm", "runArmedSession":
			if _, seen := pos[name]; !seen {
				pos[name] = call.Pos()
			}
		case "disarm":
			disarm = call.Pos()
		case "runWithProxy":
			t.Error("attachExisting runs its exec under the proxy's own arm, which kills the client and " +
				"leaves the session's processes running in the jail")
		}
		if name == "attachSignalArm" && len(call.Args) == 3 && skelIdent(call.Args[2]) != "sessionID" {
			t.Errorf("the arm hangs up %v, want this attach's sessionID", call.Args[2])
		}
		if name == "runArmedSession" && len(call.Args) > 1 {
			if skelIdent(call.Args[0]) != "runCmd" || skelIdent(call.Args[1]) != "arm" {
				t.Errorf("the session runs %v under %v, want runCmd under arm", call.Args[0], call.Args[1])
			}
		}
		return true
	})
	last := token.NoPos
	for _, name := range []string{"attachSessionID", "sessionEnvArgs", "attachSignalArm", "runArmedSession"} {
		p, ok := pos[name]
		if !ok {
			t.Errorf("attachExisting no longer calls %s", name)
			continue
		}
		if p < last {
			t.Errorf("%s is out of order in attachExisting", name)
		}
		last = p
	}
	if disarm == token.NoPos || disarm < pos["runArmedSession"] {
		t.Error("attachExisting does not disarm its arm once the exec returns")
	}
}

// TestAnAttachNamesItsSessionOnTheExec drives attachExisting to its exec against a fake runtime
// that records its argv: a current jail's exec carries the session's -e pair, an older jail's
// does not, and both are exec's of the jail's entrypoint.
func TestAnAttachNamesItsSessionOnTheExec(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		named bool
	}{{"a current jail", currentJailEnv, true}, {"a jail launched before the tag", preGateEnv, false}} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, tc.env, zaiSelected(t), hydratedKey(), nil)
			argv := recordExecArgv(t)
			rc, restarted := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
				stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil)
			if rc != 0 || restarted {
				t.Fatalf("rc=%d restarted=%v\n%s", rc, restarted, stderr)
			}
			got := argv()
			if len(got) < 2 || got[0] != "exec" {
				t.Fatalf("the fake runtime saw %q, want an exec", got)
			}
			var id string
			for i, a := range got {
				if a == "-e" && i+1 < len(got) && strings.HasPrefix(got[i+1], entrypoint.SessionIDEnv+"=") {
					id = strings.TrimPrefix(got[i+1], entrypoint.SessionIDEnv+"=")
				}
			}
			if tc.named && !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
				t.Errorf("the exec names no session: %q", got)
			}
			if !tc.named && id != "" {
				t.Errorf("an older jail's exec names a session: %q", got)
			}
		})
	}
}

// recordExecArgv puts a fake podman first on PATH that writes its arguments, one per line, and
// returns a reader of the last invocation's.
func recordExecArgv(t *testing.T) func() []string {
	t.Helper()
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + shquote.Quote(record) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return func() []string {
		b, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("the fake runtime never ran: %v", err)
		}
		return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
}
