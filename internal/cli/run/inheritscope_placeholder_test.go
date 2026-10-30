package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ISSUE #66: a workspace that once ran under podman keeps, in its sidecar, the EMPTY file podman
// creates at the target of a single-file `:ro` bind (0 bytes, mode 0700). It holds no data, yet
// a macos-user launch refused it as an owner file it could not prove it had delivered. Through
// Run, both with the user config still JSONC and with it converted to TOML: the launch moves the
// placeholder aside, says so in one line, and dispatches. Red if the arm stops clearing it.
func TestMacosUserLaunchMovesAsideAnEmptyPodmanPlaceholder(t *testing.T) {
	for _, tc := range []struct {
		name, file, body string
	}{
		{name: "legacy user config", file: "config.jsonc", body: `{"packs": ["claude"]}` + "\n"},
		{name: "converted user config", file: "config.toml", body: "packs = [\"claude\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			userDir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(userDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(userDir, tc.file), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			ws := floortest.ResolvedTemp(t)
			configDir := filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail")
			placeholder := plantPodmanPlaceholder(t, configDir, "config.jsonc")
			nested := plantPodmanPlaceholder(t, configDir, "inherited-launch.jsonc")
			before, err := os.Lstat(placeholder)
			if err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			var got macosUserCall
			called := false
			o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
				called, got = true, c
				return 0
			})
			if rc := Run(*o); rc != 0 || !called {
				t.Fatalf("Run() = %d, dispatched %v: an empty podman placeholder refused the launch\nstderr:\n%s", rc, called, stderr.String())
			}
			if _, err := os.Lstat(placeholder); !os.IsNotExist(err) {
				t.Errorf("the placeholder still occupies %s (err %v)", placeholder, err)
			}
			aside := placeholder + ".podman-placeholder"
			after, err := os.Lstat(aside)
			if err != nil || !os.SameFile(before, after) || after.Size() != 0 || after.Mode() != before.Mode() {
				t.Errorf("the placeholder was not moved intact to %s: before %v after %v err %v", aside, before, after, err)
			}
			if _, err := os.Lstat(nested); err != nil {
				t.Errorf("the nested placeholder, which this backend never writes, was touched: %v", err)
			}
			var lines []string
			for _, line := range strings.Split(stderr.String(), "\n") {
				if strings.Contains(line, placeholder) {
					lines = append(lines, line)
				}
			}
			if len(lines) != 1 || !strings.Contains(lines[0], aside) || !strings.Contains(lines[0], "empty") {
				t.Errorf("the launch did not say in one line that it moved %s aside to %s:\n%s", placeholder, aside, stderr.String())
			}
			if !slices.Contains(got.overlay.Dests, inheritPreflightRel) || slices.Contains(got.overlay.Dests, inheritPreflightLegacyRel) {
				t.Errorf("the launch delivered %v, want the TOML user scope and no JSONC fallback", got.overlay.Dests)
			}
		})
	}
}

// A dry run describes the launch without changing the sidecar: it neither moves the placeholder
// nor refuses because of it, and it says what a launch will do.
func TestMacosUserDryRunLeavesTheEmptyPlaceholderAndSaysALaunchMovesIt(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := floortest.ResolvedTemp(t)
	placeholder := plantPodmanPlaceholder(t, filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail"), "config.jsonc")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.DryRun = true
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("dry Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	if _, err := os.Lstat(placeholder); err != nil {
		t.Errorf("a dry run moved the placeholder: %v", err)
	}
	if _, err := os.Lstat(placeholder + ".podman-placeholder"); !os.IsNotExist(err) {
		t.Errorf("a dry run created %s.podman-placeholder (err %v)", placeholder, err)
	}
	if !strings.Contains(stderr.String(), "will move") || !strings.Contains(stderr.String(), placeholder) {
		t.Errorf("the dry run did not say a launch will move %s aside:\n%s", placeholder, stderr.String())
	}
}

// What the move must NOT take: anything with data, anything it cannot prove is an unshared regular
// file. Each keeps the refusal, and the refusal names the exact `mv` that clears the way.
func TestMacosUserKeepsRefusingAnythingButAnUnsharedEmptyFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, path string)
		mv    bool
	}{
		{name: "non-empty owner file", mv: true, plant: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"packs": ["copilot"]}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "empty file hardlinked elsewhere", mv: true, plant: func(t *testing.T, path string) {
			other := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(other, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(other, path); err != nil {
				t.Skipf("no cross-directory hardlink here: %v", err)
			}
		}},
		{name: "link to an empty file", plant: func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "empty")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `["claude"]`)
			ws := floortest.ResolvedTemp(t)
			configDir := filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(configDir, "config.jsonc")
			tc.plant(t, path)
			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
			called := false
			o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { called = true; return 0 })
			if rc := Run(*o); rc == 0 || called {
				t.Fatalf("Run() = %d, dispatched %v: %s was taken for an empty placeholder", rc, called, tc.name)
			}
			after, err := os.Lstat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Errorf("the refusal changed %s: before %v after %v err %v", path, before, after, err)
			}
			if _, err := os.Lstat(path + ".podman-placeholder"); !os.IsNotExist(err) {
				t.Errorf("the refusal moved something to %s.podman-placeholder (err %v)", path, err)
			}
			if tc.mv {
				want := "mv " + path + " " + path + ".moved-aside"
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("the refusal does not name the command %q:\n%s", want, stderr.String())
				}
				if strings.Contains(stderr.String(), "its prior delivery could overwrite") {
					t.Errorf("the refusal kept the garbled sentence:\n%s", stderr.String())
				}
			}
		})
	}
}

// The move never overwrites: an earlier placeholder already aside keeps its place, and the new one
// takes the next free name.
func TestMacosUserPlaceholderMoveNeverOverwritesAnEarlierOne(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := floortest.ResolvedTemp(t)
	configDir := filepath.Join(paths.WorkspaceHomeState(ws), "config", "yolo-jail")
	placeholder := plantPodmanPlaceholder(t, configDir, "config.jsonc")
	earlier := placeholder + ".podman-placeholder"
	if err := os.WriteFile(earlier, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { return 0 })
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) != "kept\n" {
		t.Errorf("the earlier placeholder at %s was overwritten (err %v): %q", earlier, err, b)
	}
	if info, err := os.Lstat(earlier + "-2"); err != nil || info.Size() != 0 {
		t.Errorf("the new placeholder did not land at %s-2 (info %v err %v)", earlier, info, err)
	}
}

// THE RACE: a file written to, or swapped for another, between the check and the rename is put
// back where it was, with its bytes, and nothing is left at the placeholder name.
func TestPlaceholderMovePutsBackAFileThatChangedBeforeTheRename(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, path string)
		want   string
	}{
		{name: "written to", want: "late data\n", change: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("late data\n"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "swapped", want: "owner file\n", change: func(t *testing.T, path string) {
			if err := os.WriteFile(path+".new", []byte("owner file\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wsState := floortest.ResolvedTemp(t)
			path := plantPodmanPlaceholder(t, filepath.Join(wsState, "config", "yolo-jail"), "config.toml")
			placeholderBeforeRename = func(p string) {
				if p != path {
					t.Errorf("the hook saw %s, want %s", p, path)
				}
				tc.change(t, p)
			}
			t.Cleanup(func() { placeholderBeforeRename = nil })
			if dest, err := movePlaceholderAside(wsState, filepath.Join("config", "yolo-jail", "config.toml"), os.Geteuid()); err == nil {
				t.Fatalf("a file that changed before the rename was moved to %s", dest)
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != tc.want {
				t.Errorf("the changed file is not back at %s (err %v): %q", path, err, b)
			}
			if _, err := os.Lstat(path + podmanPlaceholderSuffix); !os.IsNotExist(err) {
				t.Errorf("something was left at the placeholder name (err %v)", err)
			}
		})
	}
}

// The TOML destination is in the family too: a podman launch of today binds config.toml the same
// single-file way, so its empty target is moved aside like the legacy one.
func TestClearPodmanPlaceholdersCoversTheTOMLDestination(t *testing.T) {
	wsState := floortest.ResolvedTemp(t)
	path := plantPodmanPlaceholder(t, filepath.Join(wsState, "config", "yolo-jail"), "config.toml")
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr}
	if gone := o.clearPodmanPlaceholders(wsState); len(gone) != 0 {
		t.Errorf("a launch reported %v as planned rather than moved", gone)
	}
	if _, err := os.Lstat(path + podmanPlaceholderSuffix); err != nil {
		t.Errorf("the empty config.toml was not moved aside: %v\n%s", err, stderr.String())
	}
}

func plantPodmanPlaceholder(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
