package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
)

// runPackageProfileSection drives checkPackageProfile over an isolated HOME so the probe
// reads a fixture GC root rather than the developer's real state dir. materializes=true is
// the macos-user notch — the one whose launch really does build the profile; the notch that
// does not is covered end-to-end below, because its wording is the only thing that turns on
// the flag.
func runPackageProfileSection(t *testing.T, home string) string {
	t.Helper()
	t.Setenv("HOME", home)
	var out bytes.Buffer
	o := &Options{Stdout: &out, IsTTYStdout: func() bool { return false }}
	fillDefaults(o)
	r := newReporter(&out, false)
	o.checkPackageProfile(r, render.KindJail, true, 1)
	return out.String()
}

// rootLinkIn returns the GC-root path checkPackageProfile reads under home, with its parent
// dir created. Spelled from the same leaves darwinpkg.ProfileRootLink uses, so the fixture
// and the probe cannot disagree about where the root lives.
func rootLinkIn(t *testing.T, home string) string {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "yolo-jail", "build", "package-roots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "packages")
}

// The healthy state: a root that resolves is a PASS naming the store path, so a user can
// see which closure their agent's tools come from without running nix.
func TestPackageProfileSectionRooted(t *testing.T) {
	home := t.TempDir()
	store := t.TempDir() // stands in for the /nix/store profile
	if err := os.Symlink(store, rootLinkIn(t, home)); err != nil {
		t.Fatal(err)
	}
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[PASS]") || !strings.Contains(got, store) {
		t.Errorf("a resolved+rooted profile should PASS and name the store path:\n%s", got)
	}
}

// No root yet is the normal pre-first-run state, so it WARNs (a launch creates it) rather
// than failing — and the note must name the path a run will create.
func TestPackageProfileSectionAbsent(t *testing.T) {
	home := t.TempDir()
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[WARN]") {
		t.Errorf("an absent root is normal before the first run — expected WARN:\n%s", got)
	}
	if !strings.Contains(got, "package-roots") {
		t.Errorf("the note should name where the root will be created:\n%s", got)
	}
}

// The one genuinely bad state: the root exists but its target is GONE. That means a nix GC
// collected a ROOTED closure, which is the N1 defect recurring, so it is a FAIL — the whole
// reason to report the root at all is to make that observable rather than silent.
func TestPackageProfileSectionDanglingRootFails(t *testing.T) {
	home := t.TempDir()
	link := rootLinkIn(t, home)
	if err := os.Symlink(filepath.Join(t.TempDir(), "collected-away"), link); err != nil {
		t.Fatal(err)
	}
	got := runPackageProfileSection(t, home)
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("a dangling GC root means a rooted closure was collected — expected FAIL:\n%s", got)
	}
	if !strings.Contains(got, "no longer exists") {
		t.Errorf("the failure should say what is wrong:\n%s", got)
	}
}

// runCheckOverConfig drives the WHOLE of Check() over one workspace config, so what the
// tests below pin is THE GATE AT ITS CALL SITE rather than the report in isolation.
//
// That distinction is the point of them existing: the three tests above call
// checkPackageProfile directly, so every one of them stays green when the gate is inverted,
// when its `PrimBakedImage` term is deleted, or when the call vanishes from Check()
// altogether — the exact shape AGENTS.md names ("does it fail if I delete the call site?").
// Only a run of Check() can answer that, and only two runs differing in ONE config key can
// attribute the difference to the gate.
//
// The fixture is TestExitCodeCleanInJail's, which is the one already proven to reach the
// sections after the accumulated-fail gate: a live podman, nix present, a resolvable repo
// root, no build. YOLO_VERSION makes the host-side sections skip for determinism and is
// otherwise irrelevant here — the gate deliberately has no in-jail term, because a jail
// resolves to the jail notch through the same predicate as everything else.
func runCheckOverConfig(t *testing.T, cfgJSON string, isMacOS bool) string {
	t.Helper()
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.IsMacOS = isMacOS

	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "flake.nix"), []byte("{}"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module test\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(repo, "flake.lock"), []byte("{}"), 0o644))
	opts.RepoRoot = func() (reporoot.Resolution, bool) {
		return reporoot.Resolution{Root: repo, Source: reporoot.FromEnv}, true
	}
	// ⚠ `/nix` IS STUBBED, NOT STATTED, and that is the whole reason this fixture exists.
	// A real os.Stat made this suite pass in a jail (which bind-mounts /nix) and FAIL on a
	// GitHub runner (which has none) — CI run 34901094907, `[FAIL] Nix store: /nix not
	// found`, on a test whose subject is the packages-profile REPORT and not whether the
	// host has nix. LookPath is stubbed here for exactly the same reason; PathExists was
	// left real by oversight.
	opts.PathExists = func(p string) bool {
		if p == "/nix" || strings.HasPrefix(p, "/nix/") {
			return true // CI-condition probe
		}
		_, err := os.Stat(p)
		return err == nil
	}
	must(t, os.WriteFile(filepath.Join(opts.Workspace, "yolo-jail.jsonc"), []byte(cfgJSON), 0o644))
	opts.Getenv = func(k string) string {
		if k == "YOLO_VERSION" {
			return "0.1.0-test"
		}
		return ""
	}
	opts.LookPath = func(name string) (string, bool) {
		switch name {
		case "podman", "nix", "python3":
			return "/usr/bin/" + name, true
		}
		return "", false
	}
	opts.Exec = fakeExec(map[string]ExecResult{
		"podman --version": {Stdout: "podman version 5.0.0", Ran: true, RC: 0},
		"podman info":      {Stdout: "host: {}", Ran: true, RC: 0},
		"nix --version":    {Stdout: "nix (Nix) 2.30.0", Ran: true, RC: 0},
		"nix config show":  {Stdout: "", Ran: true, RC: 0},
		"python3":          {Stdout: "ok", Ran: true, RC: 0},
		"podman images":    {Stdout: "", Ran: true, RC: 0},
		"podman ps":        {Stdout: "", Ran: true, RC: 0},
	})

	Check(opts)
	return stripANSI(out.String())
}

// THE GATE IS THE BAKED IMAGE, NOT THE PLATFORM (provisioner-sets.md §9 step 2 / OQ-NX9).
// Two runs of Check() over identical fixtures — same Linux host, same resolvable GC root,
// same declared packages — differing only in `confinement`, must disagree about the profile
// report, because the jail notch composes PrimBakedImage and the host notch does not.
//
// It fails if the gate is removed (the jail run starts naming a closure its image supplies)
// and it fails if the call leaves Check() (the host run stops reporting at all), which is
// what the direct-call tests above cannot do.
func TestPackageProfileGateIsTheBakedImageNotThePlatform(t *testing.T) {
	run := func(t *testing.T, confinement string) (string, string) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		store := t.TempDir()
		if err := os.Symlink(store, rootLinkIn(t, home)); err != nil {
			t.Fatal(err)
		}
		cfg := `{"confinement": "` + confinement + `", "packages": ["ripgrep"]}`
		return runCheckOverConfig(t, cfg, false), store
	}

	t.Run("jail: the image is the closure, so nothing is reported", func(t *testing.T) {
		got, store := run(t, "jail")
		if strings.Contains(got, "Declared packages") {
			t.Errorf("a jail notch composes PrimBakedImage — the profile section must not "+
				"run, its tools come from the image:\n%s", got)
		}
		if strings.Contains(got, store) {
			t.Errorf("check named the nix profile %s at the jail notch, which no launch there "+
				"uses:\n%s", store, got)
		}
	})

	t.Run("host: no image, so the profile is the answer", func(t *testing.T) {
		got, store := run(t, "host")
		if !strings.Contains(got, "Declared packages") {
			t.Errorf("a notch with no baked image must get the profile report on ANY "+
				"platform — this is Linux:\n%s", got)
		}
		if !strings.Contains(got, "[PASS]") || !strings.Contains(got, store) {
			t.Errorf("expected the resolved root reported as a PASS naming %s:\n%s", store, got)
		}
	})
}

// A notch with no image AND no provisioner must not borrow the macos-user remedy. "A run
// materializes it" is true of the macos-user backend and false of `guest`/`host`, where
// nothing builds the profile at all — so this cell states the inertness and says there is
// nothing to run (report-tiers P2).
func TestPackageProfileWarnsThatNothingMaterializesTheDeclaredPackages(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no GC root anywhere under it
	got := runCheckOverConfig(t, `{"confinement": "host", "packages": ["ripgrep"]}`, false)
	if !strings.Contains(got, "Declared packages") || !strings.Contains(got, "[WARN]") {
		t.Fatalf("declared packages at a notch with no package layer must be reported:\n%s", got)
	}
	if !strings.Contains(got, "nothing materializes") {
		t.Errorf("the WARN must say nothing materializes them here:\n%s", got)
	}
	if strings.Contains(got, "A run materializes it") {
		t.Errorf("that note is the macos-user backend's and is false at this notch — a "+
			"remedy the user cannot run:\n%s", got)
	}
}

// Nothing declared and nothing that would provision it: no closure to describe, so the
// honest report is NO report — not a [WARN] about a profile the user never asked for, and
// not a [PASS] for work that did not happen.
func TestPackageProfileSaysNothingWhenNothingIsDeclaredAndNothingProvisions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := runCheckOverConfig(t, `{"confinement": "host"}`, false)
	if strings.Contains(got, "Declared packages") {
		t.Errorf("no packages and no provisioner is not a finding:\n%s", got)
	}
}

// THE macos-user PATH IS UNCHANGED BY THE MOVE, including the case that motivates it: the
// profile is the FLOOR plus the declared packages (darwinpkg.Materialize: "mise, node, git
// and ripgrep reach the agent here or nowhere"), so it is reported even when `packages:`
// declares nothing. A `declared == 0` short-circuit applied to every notch would have
// silently dropped the report on the one host that has always had it.
func TestPackageProfileStillReportsTheMacosUserFloorProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := t.TempDir()
	if err := os.Symlink(store, rootLinkIn(t, home)); err != nil {
		t.Fatal(err)
	}
	got := runCheckOverConfig(t, `{"runtime": "macos-user"}`, true)
	if !strings.Contains(got, "Declared packages") || !strings.Contains(got, store) {
		t.Errorf("the macos-user notch materializes a floor profile with or without "+
			"`packages:` — it must still be reported:\n%s", got)
	}
}
