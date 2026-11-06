//go:build unix

package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A setup's launch record round-trips through its own directory, created on write, and a read of
// one that is not there is fs.ErrNotExist (named-setups-plan.md slice 6a-iii).
func TestSetupStateFileRoundTrips(t *testing.T) {
	ws := t.TempDir()
	if _, err := ReadSetupStateFile(ws, "podman-self", "config-boot.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read of an absent record: %v, want fs.ErrNotExist", err)
	}
	if err := WriteSetupStateFile(ws, "podman-self", "config-boot.json", []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(SetupStateDir(ws, "podman-self"), "config-boot.json")); err != nil || string(got) != "{}\n" {
		t.Fatalf("written record = %q, %v", got, err)
	}
	if got, err := ReadSetupStateFile(ws, "podman-self", "config-boot.json"); err != nil || string(got) != "{}\n" {
		t.Fatalf("ReadSetupStateFile = %q, %v", got, err)
	}
	if _, err := ReadSetupStateFile(ws, "seatbelt-self", "config-boot.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("another setup's record read %v, want fs.ErrNotExist: each setup's records are its own", err)
	}
	if err := WriteSetupStateFile(ws, "Not/A Setup", "x", nil, 0o644); err == nil {
		t.Error("a name that is not a setup name was written")
	}
	if _, err := ReadSetupStateFile(ws, "..", "x"); err == nil {
		t.Error("a name that is not a setup name was read")
	}
}

// The jail can write every directory on the way, so a link at the record or the setup's
// directory is never followed: a read refuses, and a write replaces the link at the record
// rather than writing through it.
func TestSetupStateFileFollowsNoLink(t *testing.T) {
	t.Run("record", func(t *testing.T) {
		ws := t.TempDir()
		host := filepath.Join(t.TempDir(), "host-file")
		if err := os.WriteFile(host, []byte("host"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := SetupStateDir(ws, "podman-self")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "config-boot.json")
		if err := os.Symlink(host, link); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadSetupStateFile(ws, "podman-self", "config-boot.json"); err == nil {
			t.Errorf("read through a link: %q", got)
		}
		if err := WriteSetupStateFile(ws, "podman-self", "config-boot.json", []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(host); string(got) != "host" {
			t.Errorf("wrote through the link into the host file: %q", got)
		}
		if fi, err := os.Lstat(link); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("the record is not a regular file after the write: %v", err)
		}
	})
	t.Run("setup directory", func(t *testing.T) {
		ws := t.TempDir()
		target := t.TempDir()
		if err := os.WriteFile(filepath.Join(target, "config-boot.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(SetupsDir(ws), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, SetupStateDir(ws, "podman-self")); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadSetupStateFile(ws, "podman-self", "config-boot.json"); err == nil {
			t.Errorf("read through a linked setup directory: %q", got)
		}
		if names := SetupStateNames(ws); len(names) != 0 {
			t.Errorf("SetupStateNames listed a linked directory: %v", names)
		}
	})
}

// SetupStateNames lists the real setup directories, sorted, and nothing else.
func TestSetupStateNamesListsRealSetupDirsOnly(t *testing.T) {
	ws := t.TempDir()
	if names := SetupStateNames(ws); len(names) != 0 {
		t.Fatalf("a workspace with no state listed %v", names)
	}
	for _, d := range []string{"seatbelt-self", "podman-self", "Bad_Name"} {
		if err := os.MkdirAll(filepath.Join(SetupsDir(ws), d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(SetupsDir(ws), "a-file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := SetupStateNames(ws), []string{"podman-self", "seatbelt-self"}; !slices.Equal(got, want) {
		t.Errorf("SetupStateNames = %v, want %v", got, want)
	}
}
