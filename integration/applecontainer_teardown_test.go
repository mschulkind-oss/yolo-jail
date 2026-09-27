package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestACForgivesOnlyTheTeardownRaceAfterACompletedProbe pins acForgivableTeardown from Linux:
// a failed `container run --rm` is forgiven only when the probe printed its done marker and the
// only thing after it is Apple Container's teardown race. Named TestAC…, not TestAppleContainer…,
// so it runs everywhere rather than only on the Mac job.
func TestACForgivesOnlyTheTeardownRaceAfterACompletedProbe(t *testing.T) {
	probe := "type=character special file\nread=[]\n"
	for _, tc := range []struct {
		name string
		out  string
		want bool
	}{
		{"completed probe, then the race", probe + acProbeDone + "\nError: " + acTeardownRace + "\n", true},
		{"completed probe, then the race without the Error prefix", probe + acProbeDone + "\n" + acTeardownRace, true},
		{"the race before the probe finished", probe + "Error: " + acTeardownRace + "\n", false},
		{"completed probe, then another error", probe + acProbeDone + "\nError: image not found\n", false},
		{"completed probe, the race, and more", probe + acProbeDone + "\nError: " + acTeardownRace + "\nError: something else\n", false},
		{"completed probe and a plain non-zero exit", probe + acProbeDone + "\n", false},
		{"nothing at all", "", false},
	} {
		if got := acForgivableTeardown(tc.out); got != tc.want {
			t.Errorf("%s: acForgivableTeardown = %v, want %v\n%s", tc.name, got, tc.want, tc.out)
		}
	}
}

// TestEveryAppleContainerRunGoesThroughTheHelper keeps the forgiveness at every call site: a
// probe that runs `container run` itself would fail the Mac job on the CLI's teardown race again
// (run 36298275277), so no source file here may spell that command outside acContainerRun.
func TestEveryAppleContainerRunGoesThroughTheHelper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("listing the integration sources: %v (%d files)", err, len(files))
	}
	direct := regexp.MustCompile(`"container",\s*"run"`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, loc := range direct.FindAllIndex(b, -1) {
			t.Errorf("%s runs `container run` directly at byte %d; call acContainerRun so a completed probe survives the CLI's teardown race", f, loc[0])
		}
	}
}
