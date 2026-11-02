package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grantcrossing_test.go pins the UNMEDIATED half of docs/design/loophole-access-approval.md §4.4
// (LG-D6): a grant-declaring loophole whose crossing is a bind, a device or a socket the launch
// publishes has no daemon to refuse a call, so the crossing itself is what a grant admits. With
// no grant nothing crosses; with one, all of it does.

// audioShapedSet writes a pack module shaped like the shipped audio loophole (two socket binds
// under ${XDG_RUNTIME_DIR}, a shipped config file, a device) that declares one grant kind, `use`,
// and returns the set a launch reads, with the two socket paths it binds.
func audioShapedSet(t *testing.T) (Set, []string) {
	t.Helper()
	unsetJail(t)
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	pulse := filepath.Join(runtimeDir, "pulse", "native")
	pipewire := filepath.Join(runtimeDir, "pipewire-0")
	for _, p := range []string{pulse, pipewire} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	module := filepath.Join(t.TempDir(), "audio")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "asound.conf"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No default_enabled: a grant-declaring loophole runs because its pack is selected.
	manifest := `name = "audio"
description = "an audio-shaped fixture"
version = 1
transport = "none"
lifecycle = "external"
access = "use the host's microphone and speakers"
host_devices = ["/dev/null"]

[[host_bind_mounts]]
host = "${XDG_RUNTIME_DIR}/pulse/native"
container = "/run/pulse/native"
readonly = true

[[host_bind_mounts]]
host = "${XDG_RUNTIME_DIR}/pipewire-0"
container = "/run/pipewire/pipewire-0"
readonly = true

[[host_bind_mounts]]
host = "{loophole_dir}/asound.conf"
container = "/etc/alsa/conf.d/50-yolo-audio-alsa.conf"
readonly = true

[[grants]]
kind = "use"
text = "use your microphone and speakers"
`
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: module, Pack: "audio", HostExecApproved: true}}})
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != "audio" {
		t.Fatalf("the selected grant-declaring fixture does not run on selection: %+v", enabled)
	}
	return set, []string{pulse, pipewire}
}

func TestAnUnmediatedCrossingIsAddedOnlyUnderAGrant(t *testing.T) {
	set, sockets := audioShapedSet(t)
	crosses := func(args []string) []string {
		joined := strings.Join(args, " ")
		var got []string
		for _, want := range []string{sockets[0] + ":/run/pulse/native", sockets[1] + ":/run/pipewire/pipewire-0",
			"/etc/alsa/conf.d/50-yolo-audio-alsa.conf", "--device /dev/null"} {
			if strings.Contains(joined, want) {
				got = append(got, want)
			}
		}
		return got
	}

	for _, tc := range []struct {
		name    string
		granted map[string][]string
	}{
		{"no grants handed", nil},
		{"another loophole's grant", map[string][]string{"journal": {"read"}}},
		{"the kind not granted", map[string][]string{"audio": nil}},
	} {
		without := set.WithGrants(tc.granted)
		if got := crosses(without.RuntimeArgsFor(without.Enabled(), "podman")); len(got) != 0 {
			t.Errorf("%s: an ungranted unmediated loophole crossed %v", tc.name, got)
		}
		if names := without.JailBoundNames(without.Enabled(), "podman"); len(names) != 0 {
			t.Errorf("%s: an ungranted loophole is served by its binds: %v", tc.name, names)
		}
	}

	with := set.WithGrants(map[string][]string{"audio": {"use"}})
	if got := crosses(with.RuntimeArgsFor(with.Enabled(), "podman")); len(got) != 4 {
		t.Errorf("with `use` granted the crossing is %v, want both sockets, the ALSA file and the device", got)
	}
	if names := with.JailBoundNames(with.Enabled(), "podman"); len(names) != 1 || names[0] != "audio" {
		t.Errorf("with `use` granted the bound loopholes are %v, want audio", names)
	}
}

// A loophole that declares no grants is ungated, so the grant check never withholds its crossing:
// absent `[[grants]]` is today's behavior (plan PD-12).
func TestAnUngatedLoopholesCrossingNeedsNoGrant(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "snd-like"))
	writeManifest(t, mod, map[string]any{
		"name": "snd-like", "description": "x", "transport": "none",
		"host_devices": []any{"/dev/null"},
	})
	set := approvedSetFrom(md).WithGrants(nil)
	if !hasPair(set.RuntimeArgsFor(set.Enabled(), ""), "--device", "/dev/null") {
		t.Errorf("an ungated loophole's device was withheld for want of a grant")
	}
}
