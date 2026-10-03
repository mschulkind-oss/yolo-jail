package check

import (
	"bytes"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// TestMacOSPlatformGradesThePodmanMachineShares drives the whole macOS Platform section on
// a Podman Mac and pins the shared-folders row: FAIL naming the unshared folder and the
// recreate command when the machine's recorded shares miss the prebuilt bundle (the
// Homebrew-Cellar case), PASS when they cover it, and SKIP — not a pass — when the share
// list cannot be read. Delete the checkPodmanMachineShares call and every row fails.
func TestMacOSPlatformGradesThePodmanMachineShares(t *testing.T) {
	// RESOLVED where minted: the row resolves the bundle's symlinks like the launch does,
	// and on a Mac t.TempDir() is under the /var/folders -> /private/var/folders link.
	bundle, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(bundle, "bin", "linux-"+goruntime.GOARCH)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "yolo-entrypoint"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := "/Users/me/proj"
	// The default shares less whichever covers the bundle: on a Mac t.TempDir() is under
	// /private, a default share, so leaving it in would share the "unshared" bundle.
	var missing []string
	for _, s := range []string{"/Users", "/private", "/var/folders"} {
		if bundle != s && !strings.HasPrefix(bundle, s+"/") {
			missing = append(missing, s)
		}
	}
	initCmd := "podman machine init"
	for _, s := range missing {
		initCmd += " -v " + s + ":" + s
	}
	initCmd += " -v "

	cases := []struct {
		name   string
		shares []string // nil: the machine config cannot be read
		badge  string
		want   []string
	}{
		{"the default shares miss the bundle", missing, "[FAIL]",
			[]string{"does not share: " + bundle, "podman machine rm", initCmd}},
		{"a share covering the bundle passes", []string{"/Users", filepath.Dir(bundle)}, "[PASS]",
			[]string{"Podman Machine shares the folders a jail binds", workspace, bundle}},
		{"an unreadable list is a skip", nil, "[SKIP]",
			[]string{"could not read the machine's share list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgDir := t.TempDir()
			if tc.shares != nil {
				var mounts []string
				for _, s := range tc.shares {
					mounts = append(mounts, `{"Source": "`+s+`", "Target": "`+s+`"}`)
				}
				body := `{"Mounts": [` + strings.Join(mounts, ",") + `]}`
				if err := os.WriteFile(filepath.Join(cfgDir, "podman-machine-default.json"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			o := &Options{IsMacOS: true, Stdout: &out, IsTTYStdout: func() bool { return false }}
			fillDefaults(o)
			o.Machine = "arm64"
			o.Workspace = workspace
			o.Getenv = func(k string) string {
				if k == "YOLO_RUNTIME" {
					return "podman"
				}
				return ""
			}
			o.RepoRoot = func() (reporoot.Resolution, bool) { return reporoot.Resolution{Root: bundle}, true }
			o.LookPath = func(name string) (string, bool) { return "/opt/homebrew/bin/" + name, name == "podman" }
			o.PathExists = func(p string) bool { return p == "/nix" }
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				switch strings.Join(argv, " ") {
				case "podman machine info":
					return ExecResult{Ran: true}
				case "podman machine list --format json":
					return ExecResult{Ran: true, Stdout: `[{"Name": "podman-machine-default", "Default": true}]`}
				case "podman machine inspect podman-machine-default":
					return ExecResult{Ran: true, Stdout: `[{"ConfigDir": {"Path": "` + cfgDir + `"}, "Name": "podman-machine-default"}]`}
				case "mount":
					return ExecResult{Ran: true, Stdout: "/dev/disk3s7 on /nix (apfs, local, journaled)\n"}
				}
				return ExecResult{}
			}
			// The machine answers the probe check asks it by, the patient one-shot (PR-D24).
			answeringPodman(o, `{}`)
			r := newReporter(&out, false)
			o.sectionMacOSPlatform(r, nil)

			var row string
			for _, l := range strings.Split(out.String(), "\n") {
				if strings.Contains(l, "Podman Machine shares") || strings.Contains(l, "does not share") ||
					strings.Contains(l, "Podman Machine shared folders") {
					row = l
					break
				}
			}
			if !strings.Contains(row, tc.badge) {
				t.Fatalf("shared-folders row is %q, want a %s:\n%s", row, tc.badge, out.String())
			}
			for _, w := range tc.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output lacks %q:\n%s", w, out.String())
				}
			}
		})
	}
}
