package run

import (
	"bytes"
	"go/ast"
	"os"
	"testing"
)

// jailstdout_test.go pins where a jail's OWN standard output goes — pid 1's, relayed by the keeper
// until the jail is ready (Options.JailStdout), and the first session's (Options.SessionStdout) —
// at both call sites in runContainer's path. The host floor's capture and build jails set both to
// the launch's stderr: an agent's stdout is routinely parsed, and a first `yolo host -- <bin>` that
// captured printed its installer's lines there, ahead of the agent's own
// (host-tool-provisioning.md §8).

// TestASessionStdoutTakesTheSessionsStdout: the session's client writes its stdout to
// SessionStdout, and its stderr to this process's stderr as before.
func TestASessionStdoutTakesTheSessionsStdout(t *testing.T) {
	var got bytes.Buffer
	arm := armLaunchSignalsWith(nil, func(int) {})
	o := &Options{SessionStdout: &got}
	rc, err := runArmedSession([]string{"sh", "-c", "echo JAIL_SESSION_STDOUT"}, arm, o)
	arm.detach()
	arm.disarm()
	if err != nil || rc != 0 {
		t.Fatalf("runArmedSession = %d, %v", rc, err)
	}
	if got.String() != "JAIL_SESSION_STDOUT\n" {
		t.Errorf("SessionStdout received %q, want the session's stdout (JAIL_SESSION_STDOUT)", got.String())
	}
}

// TestAJailStdoutAloneLeavesTheSessionOnTheProcesssStdout: a caller that only keeps the jail's own
// boot lines apart (a build jail's act, cli's jailTail) does not take the session off the proxy.
func TestAJailStdoutAloneLeavesTheSessionOnTheProcesssStdout(t *testing.T) {
	var named bytes.Buffer
	if got := (&Options{JailStdout: &named}).sessionStdout(); got != os.Stdout {
		t.Errorf("a JailStdout alone sends the session's stdout to %v, want this process's", got)
	}
}

// TestTheJailsStdoutIsTheProcesssUnlessACallerNamesOne: nil is this process's stdout — an agent's
// session, whose output is the product — and a writer a caller named is that writer.
func TestTheJailsStdoutIsTheProcesssUnlessACallerNamesOne(t *testing.T) {
	if got := (&Options{}).jailStdout(); got != os.Stdout {
		t.Errorf("an unset JailStdout is %v, want this process's stdout", got)
	}
	var named bytes.Buffer
	if got := (&Options{JailStdout: &named}).jailStdout(); got != &named {
		t.Errorf("JailStdout set to a writer is %v, want that writer", got)
	}
}

// TestTheKeepersRelayWritesPidOnesStdoutToTheJailsStdout pins the relay's call site in
// runContainer, which no unit test can drive (it starts a real container): pid 1's stdout frames go
// to o.jailStdout(), never to os.Stdout by name, and its stderr frames to o.jailStderr().
func TestTheKeepersRelayWritesPidOnesStdoutToTheJailsStdout(t *testing.T) {
	found := false
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || skelCallee(call) != "relay" || len(call.Args) != 5 {
			return true
		}
		found = true
		if c, ok := call.Args[2].(*ast.CallExpr); !ok || skelCallee(c) != "jailStdout" {
			t.Errorf("the keeper's relay writes pid 1's stdout to %v, want o.jailStdout()", call.Args[2])
		}
		if c, ok := call.Args[3].(*ast.CallExpr); !ok || skelCallee(c) != "jailStderr" {
			t.Errorf("the keeper's relay writes pid 1's stderr to %v, want o.jailStderr()", call.Args[3])
		}
		return true
	})
	if !found {
		t.Fatal("runContainer no longer calls the keeper's relay with five arguments")
	}
}
