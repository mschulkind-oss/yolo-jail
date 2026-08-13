package svcendpoint

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This file closes two gaps found by mutation-testing the transport against PR #32's
// suite as the acceptance bar. Both properties were CLAIMED in a comment and asserted
// nowhere: the existing tests passed with the behaviour inverted.

// TestOversizedFrameAllocatesNothingBeforeTheCap makes "the cap is checked BEFORE the
// buffer is allocated" a MEASURED fact.
//
// TestOversizedTokenFrameRejected states that property and cannot see it: moving
// `got := make([]byte, n)` above the bounds check leaves every assertion in that test
// passing (the drop is still prompt, the log line is unchanged, the handler is still
// never reached) while each unauthenticated connection allocates n bytes. Measured
// under that mutation: 1024.0 MiB per connection, from a peer that has presented no
// credential — a one-packet remote memory-exhaustion primitive, which is exactly what
// the cap exists to prevent and is one of the properties carried over from #32.
//
// So the assertion has to be on ALLOCATION, not on the reject. The margin is ~64x, so
// the threshold is not sensitive to unrelated allocation by idle listeners from
// earlier tests in this package.
func TestOversizedFrameAllocatesNothingBeforeTheCap(t *testing.T) {
	const frameLen = 1 << 30     // 1 GiB claimed by a 4-byte length prefix
	const allocBudget = 16 << 20 // 64x headroom over the ~15 KiB a clean run uses

	s := startServer(t)
	conn := dialPinnedRaw(t, s.path)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	writeRawTokenFrame(t, conn, frameLen, nil)
	expectNoAck(t, conn)

	runtime.ReadMemStats(&after)
	if delta := after.TotalAlloc - before.TotalAlloc; delta > allocBudget {
		t.Errorf("an unauthenticated %d-byte length prefix allocated %d bytes (%.1f MiB); "+
			"the cap must be checked BEFORE the buffer is allocated",
			frameLen, delta, float64(delta)/(1<<20))
	}
	if n := s.calls.Load(); n != 0 {
		t.Errorf("handler invoked %d times, want 0", n)
	}
}

// TestPublishDoesNotFollowAPlantedSymlink is #32's TestRelayTCPTokenRejectsPlantedFile,
// carried over — it had no counterpart here.
//
// #32 kept its token in a host-side file and tested that a symlink planted at that
// path was neither trusted nor FOLLOWED BY THE WRITE. Under the token-in-the-endpoint-
// file decision the "not trusted" half is gone (the listener mints its token; it never
// reads one), but the write half became MORE important, not less: the endpoint file is
// now the credential, it lives at a fully deterministic path, and its directory's
// parent is a world-writable /tmp.
//
// temp-file-plus-rename gives the property for free — rename REPLACES a symlink rather
// than following it — which is precisely why nothing asserted it. Mutation-verified:
// resolving the path first (one plausible EvalSymlinks call in Publish) writes the live
// 64-hex token into an attacker-chosen file with the whole suite still green.
//
// ensurePrivateDir covers a symlinked DIRECTORY. This is the leaf.
func TestPublishDoesNotFollowAPlantedSymlink(t *testing.T) {
	dir := privateDir(t)
	// The victim lives OUTSIDE the publication directory: the point of planting a
	// symlink is to redirect the write somewhere the publisher never inspects.
	victim := filepath.Join(t.TempDir(), "victim")
	const untouched = "someone else's file\n"
	if err := os.WriteFile(victim, []byte(untouched), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "svc.endpoint")
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}

	ep := sampleEndpoint(t)
	if err := Publish(path, ep); err != nil {
		t.Fatalf("Publish over a planted symlink: %v", err)
	}

	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), ep.Token) {
		t.Errorf("THE BEARER TOKEN WAS WRITTEN THROUGH THE PLANTED SYMLINK into %s", victim)
	}
	if string(data) != untouched {
		t.Errorf("the planted symlink's target was modified: %q", string(data))
	}
	// The symlink must have been REPLACED, not written through — otherwise the next
	// republish redirects again and the endpoint the client reads is the attacker's.
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		t.Errorf("%s is still a symlink after Publish", path)
	}
	// And the publication really did land, so the assertions above are not vacuous.
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read after publishing over a planted symlink: %v", err)
	}
	if got.Token != ep.Token {
		t.Errorf("published token = %q, want the one Publish was given", got.Token)
	}
}
