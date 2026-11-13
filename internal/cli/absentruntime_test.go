package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAListingVerbNamesTheNextStepWhenItsRuntimeIsAbsent: on a Mac with Podman and no Apple
// Container, the default setup's runtime is not installed, so a verb that asks one runtime (prune)
// says so, names where to install it, and names the YOLO_SETUP that asks Podman instead; with
// neither installed it names the install alone, and with the runtime installed it says nothing.
func TestAListingVerbNamesTheNextStepWhenItsRuntimeIsAbsent(t *testing.T) {
	has := func(bins ...string) func(string) bool {
		return func(b string) bool {
			for _, x := range bins {
				if x == b {
					return true
				}
			}
			return false
		}
	}
	got := absentRuntimeNote("ps", "container", "darwin", has("podman"))
	for _, want := range []string{"yolo ps: Apple Container", "is not installed", "https://github.com/apple/container",
		"YOLO_SETUP=podman-self yolo ps"} {
		if !strings.Contains(got, want) {
			t.Errorf("Podman-only Mac: note %q does not say %q", got, want)
		}
	}
	got = absentRuntimeNote("stores", "container", "darwin", has())
	if !strings.Contains(got, "https://github.com/apple/container") || strings.Contains(got, "YOLO_SETUP=") {
		t.Errorf("bare Mac: note %q, want the install alone", got)
	}
	if got = absentRuntimeNote("prune", "podman", "linux", has()); !strings.Contains(got, "yolo prune: Podman") ||
		strings.Contains(got, "YOLO_SETUP=") {
		t.Errorf("Linux without podman: note %q, want Podman's install and no other setup", got)
	}
	for _, tc := range []struct{ rt, goos string }{{"container", "darwin"}, {"podman", "linux"}, {"macos-user", "darwin"}} {
		if got := absentRuntimeNote("ps", tc.rt, tc.goos, has("podman", "container")); got != "" {
			t.Errorf("%s on %s, installed: note %q, want none", tc.rt, tc.goos, got)
		}
	}
}

// TestListingDetectPrintsTheNoteOnceAndKeepsTheRuntime: the wired DetectRuntime still answers
// the runtime the setup selects, and prints the note at most once however often it is asked.
func TestListingDetectPrintsTheNoteOnceAndKeepsTheRuntime(t *testing.T) {
	_, ws := withHomeAndCwd(t)
	t.Setenv("PATH", t.TempDir()) // nothing installed
	t.Setenv("YOLO_SETUP", "podman-self")
	var errw bytes.Buffer
	detect := listingDetect("ps", ws, &errw)
	if rt := detect(); rt != "podman" {
		t.Fatalf("runtime %q, want podman", rt)
	}
	detect()
	if n := strings.Count(errw.String(), "is not installed"); n != 1 {
		t.Errorf("note printed %d times, want once:\n%s", n, errw.String())
	}
	// With podman on PATH, nothing is printed.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	errw.Reset()
	listingDetect("ps", ws, &errw)()
	if errw.Len() != 0 {
		t.Errorf("podman installed, yet a note: %q", errw.String())
	}
}
