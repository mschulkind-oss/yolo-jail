package testutil

import "testing"

// A package that opts in builds the dispatcher ONCE however many tests ask, and one that does
// not builds per call. Both halves matter: the first is the speedup, the second is why a
// package without a TestMain to remove the shared directory leaks nothing.
func TestYoloBinaryDirBuildsOnceOnlyWhenShared(t *testing.T) {
	builds := 0
	old := buildYolo
	buildYolo = func(string) ([]byte, error) { builds++; return nil, nil }
	t.Cleanup(func() { buildYolo = old })

	a, b := YoloBinaryDir(t), YoloBinaryDir(t)
	if builds != 2 || a == b {
		t.Fatalf("unshared: want one build per call in distinct dirs, got %d builds, %q %q", builds, a, b)
	}

	builds = 0
	release := ShareYoloBinary()
	a, b = YoloBinaryDir(t), YoloBinaryDir(t)
	if builds != 1 || a != b {
		t.Fatalf("shared: want one build and one dir, got %d builds, %q %q", builds, a, b)
	}
	release()
	if YoloBinarySharing() {
		t.Fatal("release must end the sharing")
	}
	YoloBinaryDir(t)
	if builds != 2 {
		t.Fatalf("after release the next call builds again, got %d builds", builds)
	}
}
