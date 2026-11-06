package run

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
)

// ISSUE #66, AFTER SLICE 12a: a workspace that once ran under podman keeps, in its sidecar, the
// EMPTY file podman creates at the target of a single-file `:ro` bind (0 bytes, mode 0700), at
// podman's `config/yolo-jail/config.jsonc`. A macos-user launch in either mode now delivers its
// user scope at HOME's own `.config` (MMP-D5, MMP-D25), so the placeholder is in nobody's way:
// through Run, with the user config JSONC and TOML, the launch dispatches, leaves the placeholder
// exactly where it was, and says nothing about it. Red if the account mode's destination goes
// back to podman's path, where the placeholder would refuse or be moved.
func TestMacosUserLaunchLeavesAPodmanPlaceholderOutOfItsWay(t *testing.T) {
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
			podmanDir := filepath.Join(runtimeHome(ws, "macos-user"), "config", "yolo-jail")
			placeholder := plantPodmanPlaceholder(t, podmanDir, "config.jsonc")
			before, err := os.Lstat(placeholder)
			if err != nil {
				t.Fatal(err)
			}
			for _, dry := range []bool{true, false} {
				var stdout, stderr bytes.Buffer
				o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
				o.DryRun = dry
				var got macosUserCall
				called := false
				o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
					called, got = true, c
					return 0
				})
				if rc := Run(*o); rc != 0 || (!dry && !called) {
					t.Fatalf("Run() (dry %v) = %d, dispatched %v: a podman placeholder outside HOME's .config refused the launch\nstderr:\n%s",
						dry, rc, called, stderr.String())
				}
				after, err := os.Lstat(placeholder)
				if err != nil || !os.SameFile(before, after) || after.Size() != 0 {
					t.Errorf("the launch (dry %v) touched the placeholder at %s: err %v", dry, placeholder, err)
				}
				if _, err := os.Lstat(placeholder + podmanPlaceholderSuffix); !os.IsNotExist(err) {
					t.Errorf("the launch (dry %v) moved something to %s%s (err %v)", dry, placeholder, podmanPlaceholderSuffix, err)
				}
				if strings.Contains(stderr.String(), placeholder) {
					t.Errorf("the launch (dry %v) spoke of a placeholder in nobody's way:\n%s", dry, stderr.String())
				}
				if !dry && (!slices.Contains(got.overlay.Dests, inheritPreflightRel) || slices.Contains(got.overlay.Dests, inheritPreflightLegacyRel)) {
					t.Errorf("the launch delivered %v, want the TOML user scope and no JSONC fallback", got.overlay.Dests)
				}
			}
		})
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
			configDir := filepath.Join(runtimeHome(ws, "macos-user"), ".config", "yolo-jail")
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
// takes the next free name. Asked of the mover a podman relaunch uses (movePlaceholderAside), at
// podman's own path, since no macos-user destination is a podman bind's target since slice 12a.
func TestPlaceholderMoveNeverOverwritesAnEarlierOne(t *testing.T) {
	wsState := floortest.ResolvedTemp(t)
	placeholder := plantPodmanPlaceholder(t, filepath.Join(wsState, "config", "yolo-jail"), "config.jsonc")
	earlier := placeholder + podmanPlaceholderSuffix
	if err := os.WriteFile(earlier, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest, err := movePlaceholderAside(wsState, filepath.Join("config", "yolo-jail", "config.jsonc"), os.Geteuid())
	if err != nil {
		t.Fatalf("the placeholder was not moved: %v", err)
	}
	if b, err := os.ReadFile(earlier); err != nil || string(b) != "kept\n" {
		t.Errorf("the earlier placeholder at %s was overwritten (err %v): %q", earlier, err, b)
	}
	if info, err := os.Lstat(earlier + "-2"); err != nil || info.Size() != 0 || dest != earlier+"-2" {
		t.Errorf("the new placeholder did not land at %s-2 (dest %s info %v err %v)", earlier, dest, info, err)
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
// single-file way, so its empty target is moved aside like the legacy one. Asked with podman's
// own storage (each destination at the undotted path its bind uses).
func TestClearPodmanPlaceholdersCoversTheTOMLDestination(t *testing.T) {
	wsState := floortest.ResolvedTemp(t)
	path := plantPodmanPlaceholder(t, filepath.Join(wsState, "config", "yolo-jail"), "config.toml")
	var stderr bytes.Buffer
	o := &Options{Stderr: &stderr}
	podmanStorage := func(rel string) (string, error) { return filepath.FromSlash(strings.TrimPrefix(rel, ".")), nil }
	if gone := o.clearPodmanPlaceholders(wsState, podmanStorage, inheritScopeDestinations(config.InheritPreflight)...); len(gone) != 0 {
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
