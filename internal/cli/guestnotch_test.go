package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// guestnotch_test.go pins the host-side verbs of the guest notch on macOS (env-manager plan
// Phase 7.1, EMP-D1): there the notch is the macos-user backend, so `yolo apply --at guest`
// points at that launch, `yolo describe` resolves its mechanism, and the listing commands look
// at its sessions. The launch itself is internal/cli/run's notchgate_test.go.
//
// These verbs read the platform from paths.IsMacOS, so each test states the platform it is
// about with onPlatform rather than inheriting the machine's: check-macos runs this package on
// a Mac, where the Linux cases would otherwise be asking a different question.

// onPlatform makes paths.IsMacOS and paths.IsLinux answer for macOS (true) or Linux (false)
// for the rest of the test. The package's tests run serially, so the swap is the test's own.
func onPlatform(t *testing.T, macOS bool) {
	t.Helper()
	wasMac, wasLinux := paths.IsMacOS, paths.IsLinux
	paths.IsMacOS, paths.IsLinux = macOS, !macOS
	t.Cleanup(func() { paths.IsMacOS, paths.IsLinux = wasMac, wasLinux })
}

// TestApplyAtGuestOnMacOSPointsAtTheLaunch: on macOS the guest notch is built, so `yolo apply
// --at guest` is a pointer at the launch that provisions it, rc 0 — as the jail notch's apply
// is — and not the Linux refusal. The launch it names carries the flag when the notch came
// from it, and needs none when the config says `confinement: "guest"`.
func TestApplyAtGuestOnMacOSPointsAtTheLaunch(t *testing.T) {
	onPlatform(t, true)
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "guest"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --at guest on macOS rc=%d, want 0 (a pointer at the launch):\n%s%s",
			rc, out.String(), errw.String())
	}
	got := out.String()
	for _, want := range []string{"apply at confinement guest", "macos-user", "Seatbelt",
		"`yolo --at guest -- <cmd>`"} {
		if !strings.Contains(got, want) {
			t.Errorf("the macOS guest pointer does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, render.NotchUnbuilt("apply")) {
		t.Errorf("apply --at guest on macOS printed the Linux refusal, though the notch "+
			"launches there:\n%s", got)
	}

	// From the config: the launch to run is the bare one.
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"guest"}`)
	out.Reset()
	errw.Reset()
	if rc := applyMain(nil, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply at a configured macOS guest rc=%d:\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "`yolo -- <cmd>`") || strings.Contains(out.String(), "--at guest") {
		t.Errorf("a configured guest needs no flag on its launch, and the pointer should say "+
			"so:\n%s", out.String())
	}
}

// TestApplyAtGuestOnLinuxRefusesAndNamesTheNextStep: on Linux the guest notch has no backend,
// so apply keeps refusing with render.NotchUnbuilt's sentence and rc 1 — and now names what to
// run instead (EMP-D3).
func TestApplyAtGuestOnLinuxRefusesAndNamesTheNextStep(t *testing.T) {
	onPlatform(t, false)
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--at", "guest"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("apply --at guest on Linux rc=%d, want 1:\n%s%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{render.NotchUnbuilt("apply"), "yolo -- <cmd>", "yolo host -- <cmd>"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the Linux guest refusal does not say %q:\n%s", want, out.String())
		}
	}
}

// TestApplyAtAConfiguredGuestOnLinuxNamesTheConfigEdit: when the guest notch comes from the
// config, the bare jail launch is not a next step — `yolo -- <cmd>` reads the same config and
// refuses too — so the step is the config edit (or `--at jail` for one launch), with or without
// a `--at guest` typed over it. The host verbs stay offered.
func TestApplyAtAConfiguredGuestOnLinuxNamesTheConfigEdit(t *testing.T) {
	onPlatform(t, false)
	for _, args := range [][]string{nil, {"--at", "guest"}} {
		_, repo := withHomeAndCwd(t)
		writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"guest"}`)

		var out, errw bytes.Buffer
		if rc := applyMain(args, &out, &errw, false, nil); rc != 1 {
			t.Fatalf("apply %v at a configured Linux guest rc=%d, want 1:\n%s%s", args, rc,
				out.String(), errw.String())
		}
		got := out.String()
		for _, want := range []string{render.NotchUnbuilt("apply"),
			"set `confinement` to \"jail\" (or remove it)", "`yolo --at jail -- <cmd>`",
			"yolo host -- <cmd>", "yolo apply --at host"} {
			if !strings.Contains(got, want) {
				t.Errorf("apply %v: the refusal does not say %q:\n%s", args, want, got)
			}
		}
		if strings.Contains(got, "`yolo apply` alone points at that launch") {
			t.Errorf("apply %v offers the bare jail launch, which this config refuses:\n%s", args, got)
		}
	}
}

// TestConfigAtGuestNamesTheNotchesItCanRead: the read verbs keep refusing `--at guest` on both
// platforms with render.NotchUnbuilt's sentence, and now say what to read instead — `--at jail`,
// which on macOS is where the guest notch renders (EMP-D2), and `--at host`. The step is a line
// of its own, so the sentence line stays the one `yolo apply` prints
// (TestAtGuestIsRefusedWithApplysOwnSentence).
func TestConfigAtGuestNamesTheNotchesItCanRead(t *testing.T) {
	for _, macOS := range []bool{false, true} {
		onPlatform(t, macOS)
		scratchHostHome(t)
		t.Setenv("YOLO_VERSION", "")
		withWorkspaceCwd(t)

		var out, errw bytes.Buffer
		if rc := configRunW([]string{"ls", "--at", "guest"}, &out, &errw); rc != 1 {
			t.Fatalf("macOS=%v: `config ls --at guest` rc=%d, want 1:\n%s%s", macOS, rc, out.String(), errw.String())
		}
		got := errw.String()
		wants := []string{render.NotchUnbuilt("config"), "`yolo config --at jail`", "`--at host`"}
		if macOS {
			wants = append(wants, "renders as the jail notch does")
		}
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("macOS=%v: the refusal does not say %q:\n%s", macOS, want, got)
			}
		}
		if lines := strings.Split(strings.TrimSpace(got), "\n"); len(lines) < 2 {
			t.Errorf("macOS=%v: the next step is not a line of its own:\n%s", macOS, got)
		}
	}
}

// TestApplyAtGuestStillRefusesJSONOnMacOS: a pointer is prose, not a document, so `--format
// json` stays the host notch's alone at the macOS guest too — rc 2 and nothing on stdout.
func TestApplyAtGuestStillRefusesJSONOnMacOS(t *testing.T) {
	onPlatform(t, true)
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"guest"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--format", "json"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("apply --format json at a macOS guest rc=%d, want 2:\n%s%s", rc, out.String(), errw.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused --format json wrote to stdout:\n%s", out.String())
	}
}

// TestDescribeOnMacOSResolvesTheGuestNotchToMacosUser: with no `runtime` key, describe resolves
// a macOS guest's mechanism to macos-user — the separate user and Seatbelt, and a package
// profile a run materializes. The packages line is the discriminator: the vector alone is the
// macOS guest preset either way, while "a run materializes it" is true only of the backend
// that builds the profile, which the mechanism names.
func TestDescribeOnMacOSResolvesTheGuestNotchToMacosUser(t *testing.T) {
	onPlatform(t, true)
	home, _ := withHomeAndCwd(t)
	t.Setenv("YOLO_RUNTIME", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"confinement":"guest","packages":["ripgrep"]}`)

	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d: %s", rc, errw.String())
	}
	got := out.String()
	enforced := describeVector(t, got)
	for _, want := range []string{"separate OS user", "Seatbelt"} {
		if !strings.Contains(enforced, want) {
			t.Errorf("a macOS guest's vector should compose %q:\n%s", want, enforced)
		}
	}
	if line := describeLine(t, got, "packages "); !strings.Contains(line, "a run materializes it") {
		t.Errorf("describe did not resolve the macOS guest's mechanism to macos-user, the "+
			"backend that materializes the profile:\n%s", line)
	}
}

// TestDescribeOnLinuxLeavesTheGuestNotchUnprovisioned is the control: a Linux guest has no
// backend, so describe still calls its declared packages inert.
func TestDescribeOnLinuxLeavesTheGuestNotchUnprovisioned(t *testing.T) {
	onPlatform(t, false)
	home, _ := withHomeAndCwd(t)
	t.Setenv("YOLO_RUNTIME", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"confinement":"guest","packages":["ripgrep"]}`)

	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d: %s", rc, errw.String())
	}
	if line := describeLine(t, out.String(), "packages "); !strings.Contains(line, "inert") {
		t.Errorf("a Linux guest has no package layer, so its packages are inert:\n%s", line)
	}
}

// TestListingRuntimeFollowsTheMacosGuestNotch: `yolo ps` and `yolo prune` resolve the runtime
// a launch would, so a macOS guest workspace lists the macos-user sessions its launches run,
// and a Linux one does not name a backend Linux lacks.
func TestListingRuntimeFollowsTheMacosGuestNotch(t *testing.T) {
	t.Setenv("YOLO_RUNTIME", "")
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"confinement":"guest"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())

	onPlatform(t, true)
	if got := detectListingRuntime(ws); got != "macos-user" {
		t.Errorf("a macOS guest workspace's listing runtime = %q, want macos-user", got)
	}
	onPlatform(t, false)
	if got := detectListingRuntime(ws); got == "macos-user" {
		t.Errorf("a Linux guest workspace's listing runtime is macos-user, which Linux lacks")
	}
}
