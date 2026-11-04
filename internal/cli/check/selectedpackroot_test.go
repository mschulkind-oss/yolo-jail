package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// TestCheckKeepsSelectedForkRootThroughTheHostFloor drives the real Check section order: the
// Packs section stages this fork, and the later Host agent floor reader opens its patch file.
// The staged root must outlive every section that consumes the selected pack set.
func TestCheckKeepsSelectedForkRootThroughTheHostFloor(t *testing.T) {
	fork := t.TempDir()
	patches := filepath.Join(fork, "patches")
	if err := os.MkdirAll(patches, 0o755); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(patches, "0001-check.patch")
	if err := os.WriteFile(patch, []byte("From 0123456789abcdef0123456789abcdef01234567 Mon Sep 17 00:00:00 2001\n"+
		"From: fixture <fixture@example.invalid>\nSubject: [PATCH] check root\n\n---\n"+
		"diff --git a/f.txt b/f.txt\n--- a/f.txt\n+++ b/f.txt\n@@ -1 +1 @@\n-before\n+after\n-- \n"+
		"base-commit: 0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"pi-fork","contributes":[{"kind":"program","bin":"pi","via":"source",` +
		`"fork_of":"pi","source":"git+https://example.invalid/pi?ref=main","patches":"patches",` +
		`"build":"sh build.sh","produces":[".local/bin/pi"]}]}`
	if err := os.WriteFile(filepath.Join(fork, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs":["pi",{"name":"pi-fork","source":"file://`+fork+`"}]}`)

	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.Build = false
	opts.Getenv = func(key string) string {
		switch key {
		case "PATH":
			return "/usr/bin:/bin"
		case "YOLO_SETUP":
			return "podman-self"
		default:
			return ""
		}
	}
	opts.LookPath = func(name string) (string, bool) {
		return "/usr/bin/" + name, name == "podman" || name == "nix"
	}
	podmanAnswers(&opts, ExecResult{Stdout: `{"host":{"security":{"rootless":false}}}`, Ran: true, RC: 0})
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman ps":        {Stdout: "", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":  {Stdout: "", Ran: true, RC: 0},
	})
	repo := t.TempDir()
	for name, body := range map[string]string{"flake.nix": "{}", "go.mod": "module test\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	opts.PathExists = func(path string) bool { _, err := os.Stat(path); return err == nil }

	hostFloorRead := false
	var stagedRoot string
	opts.HostFloor = func(programs []hostfloor.Program) *hostfloor.Floor {
		for _, program := range programs {
			if program.Install.Bin == "pi" && program.Install.ForkedBy == "pi-fork" {
				stagedRoot = program.Install.ForkRoot
			}
		}
		return &hostfloor.Floor{
			Dir: filepath.Join(t.TempDir(), "floor"), GOOS: "darwin", GOARCH: "amd64",
			BuildActUnavailable: func(string, string) string { return "test floor has no build act" },
			Patched: func(program hostfloor.Program) hostfloor.PatchedState {
				if program.Install.Bin != "pi" || program.Install.ForkedBy != "pi-fork" {
					t.Errorf("host floor inspected unexpected patched program %+v", program)
					return hostfloor.PatchedState{}
				}
				hostFloorRead = true
				stagedRoot = program.Install.ForkRoot
				fork := packload.Fork{Pack: program.Install.ForkedBy, Bin: program.Install.Bin,
					Root: stagedRoot, Patches: program.Install.Patches}
				if _, err := fork.ReadSeries(); err != nil {
					return hostfloor.PatchedState{Reason: err.Error()}
				}
				return hostfloor.PatchedState{Recipe: "fixture-recipe"}
			},
		}
	}

	_ = Check(opts)
	if !hostFloorRead {
		t.Fatalf("Check did not hand the selected pi fork to the host floor:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "[PASS] pi-fork: 2 file(s) stage") {
		t.Fatalf("Packs did not report the staged fork as passing:\n%s", out.String())
	}
	if strings.Contains(out.String(), "the pack's root cannot be opened") {
		t.Fatalf("Check emitted the false pack-root warning after staging passed:\n%s", out.String())
	}
	if _, err := os.Stat(stagedRoot); !os.IsNotExist(err) {
		t.Errorf("Check left its staged pack tree at %q (stat error %v); want cleanup after the readers finish", stagedRoot, err)
	}
}
