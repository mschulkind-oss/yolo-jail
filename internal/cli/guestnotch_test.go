package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// guestnotch_test.go pins the host-side verbs of a Seatbelt setup (the guest notch; env-manager
// plan Phase 7.1, EMP-D1, under named setups, named-setups-plan.md slice 4a): on macOS it is the
// macos-user backend, so `yolo apply --setup seatbelt-yolo-account` points at that launch,
// `yolo describe` resolves its mechanism, and the listing commands look at its sessions. The
// launch itself is internal/cli/run's setupgate_test.go.
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

// TestApplyAtASeatbeltSetupOnMacOSPointsAtTheLaunch: on macOS a Seatbelt setup is built, so
// `yolo apply --setup seatbelt-yolo-account` is a pointer at the launch that provisions it,
// rc 0 — as a container setup's apply is — and not the Linux refusal. The launch it names carries
// the flag when the setup came from it, and needs none when the config chose it.
func TestApplyAtASeatbeltSetupOnMacOSPointsAtTheLaunch(t *testing.T) {
	onPlatform(t, true)
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--setup", "seatbelt-yolo-account"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --setup seatbelt-yolo-account on macOS rc=%d, want 0 (a pointer at the launch):\n%s%s",
			rc, out.String(), errw.String())
	}
	got := out.String()
	for _, want := range []string{"apply at confinement guest", "macos-user", "Seatbelt",
		"`yolo --setup seatbelt-yolo-account -- <cmd>`"} {
		if !strings.Contains(got, want) {
			t.Errorf("the macOS Seatbelt pointer does not say %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, render.NotchUnbuilt("apply")) {
		t.Errorf("apply --setup seatbelt-yolo-account on macOS printed the Linux refusal, though "+
			"the setup launches there:\n%s", got)
	}

	// From the user config's `setup`: the launch to run is the bare one.
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"setup":"seatbelt-yolo-account"}`)
	out.Reset()
	errw.Reset()
	if rc := applyMain(nil, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply at a configured macOS guest rc=%d:\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "`yolo -- <cmd>`") || strings.Contains(out.String(), "--setup") {
		t.Errorf("a configured Seatbelt setup needs no flag on its launch, and the pointer should "+
			"say so:\n%s", out.String())
	}
}

// TestApplyAtASeatbeltSetupOnLinuxRefusesAndNamesTheNextStep: on Linux a Seatbelt setup has no
// backend, so apply refuses with render.NotchUnbuilt's sentence and rc 1, and names what to run
// instead (EMP-D3): the default setup's launch and the host verb.
func TestApplyAtASeatbeltSetupOnLinuxRefusesAndNamesTheNextStep(t *testing.T) {
	onPlatform(t, false)
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--setup", "seatbelt-self"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("apply --setup seatbelt-self on Linux rc=%d, want 1:\n%s%s", rc, out.String(), errw.String())
	}
	for _, want := range []string{render.NotchUnbuilt("apply"), "podman-self", "yolo -- <cmd>",
		"yolo host -- <cmd>", "yolo apply --setup host-self"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the Linux Seatbelt refusal does not say %q:\n%s", want, out.String())
		}
	}
}

// TestApplyAtAConfiguredGuestOnLinuxNamesTheConfigEdit: when the Seatbelt setup comes from the
// user config's `setup` key, the bare launch is not a next step — `yolo -- <cmd>` reads the same
// config and refuses too — so the step is the `yolo config set setup` that moves it (or
// `--setup podman-self` for one launch), with or without a `--setup` typed over it. The host
// verbs stay offered.
func TestApplyAtAConfiguredGuestOnLinuxNamesTheConfigEdit(t *testing.T) {
	onPlatform(t, false)
	for _, args := range [][]string{nil, {"--setup", "seatbelt-yolo-account"}} {
		home, repo := withHomeAndCwd(t)
		writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"setup":"seatbelt-yolo-account"}`)

		var out, errw bytes.Buffer
		if rc := applyMain(args, &out, &errw, false, nil); rc != 1 {
			t.Fatalf("apply %v at a configured Linux guest rc=%d, want 1:\n%s%s", args, rc,
				out.String(), errw.String())
		}
		got := out.String()
		for _, want := range []string{render.NotchUnbuilt("apply"),
			"seatbelt-yolo-account is selected by your user config", "`yolo config set setup podman-self --user`",
			"`yolo --setup podman-self -- <cmd>`",
			"yolo host -- <cmd>", "yolo apply --setup host-self"} {
			if !strings.Contains(got, want) {
				t.Errorf("apply %v: the refusal does not say %q:\n%s", args, want, got)
			}
		}
		if strings.Contains(got, "`yolo apply` alone points at that launch") {
			t.Errorf("apply %v offers the bare jail launch, which this config refuses:\n%s", args, got)
		}
	}
}

// TestConfigAtASeatbeltSetupNamesWhatItCanRead: the read verbs refuse a Seatbelt `--setup` on
// both platforms with render.NotchUnbuilt's sentence, and say what to read instead — the verb
// with no --setup, which on macOS is where that setup renders (EMP-D2), and `--setup host-self`.
// The step is a line of its own, so the sentence line stays the one `yolo apply` prints.
func TestConfigAtASeatbeltSetupNamesWhatItCanRead(t *testing.T) {
	for _, macOS := range []bool{false, true} {
		onPlatform(t, macOS)
		scratchHostHome(t)
		t.Setenv("YOLO_VERSION", "")
		withWorkspaceCwd(t)

		var out, errw bytes.Buffer
		if rc := surfaceRunW([]string{"ls", "--setup", "seatbelt-yolo-account"}, &out, &errw); rc != 1 {
			t.Fatalf("macOS=%v: `surface ls --setup seatbelt-yolo-account` rc=%d, want 1:\n%s%s",
				macOS, rc, out.String(), errw.String())
		}
		got := errw.String()
		wants := []string{render.NotchUnbuilt("config"), "with no --setup", "`--setup host-self`"}
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
// json` stays the host setup's alone at a macOS Seatbelt setup too — rc 2 and nothing on stdout.
func TestApplyAtGuestStillRefusesJSONOnMacOS(t *testing.T) {
	onPlatform(t, true)
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"setup":"seatbelt-yolo-account"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--format", "json"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("apply --format json at a macOS guest rc=%d, want 2:\n%s%s", rc, out.String(), errw.String())
	}
	if out.Len() != 0 {
		t.Errorf("a refused --format json wrote to stdout:\n%s", out.String())
	}
}

// TestDescribeOnMacOSResolvesTheGuestNotchToMacosUser: describe resolves the user config's
// Seatbelt setup's mechanism on macOS to macos-user — the separate user and Seatbelt, and a
// package profile a run materializes. The packages line is the discriminator: the vector alone
// is the macOS guest preset either way, while "a run materializes it" is true only of the
// backend that builds the profile, which the mechanism names.
func TestDescribeOnMacOSResolvesTheGuestNotchToMacosUser(t *testing.T) {
	onPlatform(t, true)
	home, _ := withHomeAndCwd(t)
	t.Setenv("YOLO_SETUP", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"setup":"seatbelt-yolo-account","packages":["ripgrep"]}`)

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

// TestDescribeNeverTakesAProjectsRemovedConfinementAsTheGuestNotch is the control: a workspace's
// `confinement: "guest"` once chose the Seatbelt setup; now it chooses nothing (a project cannot
// choose where its agent runs, named-setups-plan.md slice 5), so describe in a session, where the
// key is a warning, reports this OS's default setup at the jail notch, on either platform.
func TestDescribeNeverTakesAProjectsRemovedConfinementAsTheGuestNotch(t *testing.T) {
	for _, macOS := range []bool{false, true} {
		onPlatform(t, macOS)
		_, repo := withHomeAndCwd(t)
		t.Setenv("YOLO_SETUP", "")
		t.Setenv("YOLO_VERSION", "9.9.9-test")
		writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"guest","packages":["ripgrep"]}`)

		var out, errw bytes.Buffer
		if rc := describeMain(nil, &out, &errw, false); rc != 0 {
			t.Fatalf("macOS=%v: describe rc=%d: %s", macOS, rc, errw.String())
		}
		want := setup.Default(platformGOOS()).Name
		if line := describeLine(t, out.String(), "setup "); !strings.Contains(line, want) {
			t.Errorf("macOS=%v: describe took the project's removed key as a selection, want %s:\n%s",
				macOS, want, line)
		}
		if strings.Contains(describeVector(t, out.String()), "Seatbelt") {
			t.Errorf("macOS=%v: describe composed Seatbelt from the project's removed key:\n%s", macOS, out.String())
		}
	}
}

// TestListingRuntimeFollowsTheMacosGuestNotch: `yolo ps` and `yolo prune` resolve the backend of
// the setup a launch would select, so under a Seatbelt setup a Mac lists the macos-user sessions
// its launches run, while a workspace's removed `confinement: "guest"` lists nothing of the kind.
func TestListingRuntimeFollowsTheMacosGuestNotch(t *testing.T) {
	t.Setenv("YOLO_SETUP", "")
	t.Setenv("YOLO_VERSION", "") // in a session an inherited setup this OS lacks is dropped (SUP-D12)
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"setup":"seatbelt-yolo-account"}`)

	onPlatform(t, true)
	if got := detectListingRuntime(ws); got != "macos-user" {
		t.Errorf("a macOS guest workspace's listing runtime = %q, want macos-user", got)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{}`)
	writeFile(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"confinement":"guest"}`)
	if got := detectListingRuntime(ws); got == "macos-user" {
		t.Errorf("a workspace's removed `confinement: guest` lists macos-user; it chooses nothing")
	}
}
