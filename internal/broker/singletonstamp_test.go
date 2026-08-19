package broker

import (
	"os"
	"path/filepath"
	"testing"
)

// stampDeps is a Deps whose PID path lives in a temp dir, so the stamp lands somewhere
// the test owns.
func stampDeps(t *testing.T) Deps {
	t.Helper()
	dir := t.TempDir()
	return Deps{PIDFilePath: filepath.Join(dir, "broker.pid")}
}

// The state this whole mechanism exists for: a singleton is RUNNING and every
// connect-based probe calls it healthy, but it predates the front and will consume the
// preamble as the request. Absence of the stamp is what distinguishes it.
func TestSingletonWithoutAStampDoesNotSpeakThePreamble(t *testing.T) {
	deps := stampDeps(t)
	if SingletonSpeaksPreamble(deps) {
		t.Fatal("a singleton with no capability stamp must NOT be reported as preamble-speaking — " +
			"that is the pre-conversion daemon, and calling it compatible is the silent failure")
	}
}

func TestAStampedSingletonSpeaksThePreamble(t *testing.T) {
	deps := stampDeps(t)
	if err := os.WriteFile(singletonStampPath(deps), []byte(singletonStamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !SingletonSpeaksPreamble(deps) {
		t.Fatal("a singleton this build stamped must be reported as compatible")
	}
}

// A stamp from a FUTURE wire contract must not read as compatible either. The constant
// is versioned precisely so the next change to the preamble can invalidate it.
func TestAForeignStampIsNotCompatible(t *testing.T) {
	deps := stampDeps(t)
	if err := os.WriteFile(singletonStampPath(deps), []byte("fronted-preamble-v99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if SingletonSpeaksPreamble(deps) {
		t.Fatal("an unrecognised stamp must read as incompatible, not as good enough")
	}
}

// The stamp is a sibling of the PID file so the existing lifecycle owns it. If it ever
// stops being derived from PIDFilePath, kill/cleanup stops removing it and a dead
// daemon's stamp outlives it — which would make an incompatible singleton look fine.
func TestTheStampIsASiblingOfThePIDFile(t *testing.T) {
	deps := stampDeps(t)
	if got, want := filepath.Dir(singletonStampPath(deps)), filepath.Dir(deps.PIDFilePath); got != want {
		t.Fatalf("stamp must live beside the PID file so the same cleanup removes it: %s vs %s", got, want)
	}
}
