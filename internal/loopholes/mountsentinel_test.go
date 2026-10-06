package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sentinelState points StateDirFor at a fresh tree for one test and returns its root.
func sentinelState(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(root, name) }
	t.Cleanup(func() { StateDirFor = orig })
	return root
}

// sentinelModule writes a loophole with a jail daemon whose state_files lists only the mount
// sentinel, the shape aws-auth and openai-auth ship: the list is nonempty so the state dir's
// credential cache never crosses, and its one entry is the inert marker.
func sentinelModule(t *testing.T, md, name string, stateFiles []any) {
	t.Helper()
	mod := mkdir(t, filepath.Join(md, name))
	writeManifest(t, mod, map[string]any{
		"name": name, "description": "x", "transport": "none",
		"state_files": stateFiles,
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
}

// EVERY LOOPHOLE THAT DECLARES THE SENTINEL GETS IT, keyed on the declaration and never on a
// name: the writer used to be openai-auth's alone (gated on its loophole name), so aws-auth,
// which declares the same marker, warned "skipping state file, host source missing" on every
// launch. Written 0600 in a 0700 state dir, before the mounts are assembled, so the argv
// mounts the marker and the launch says nothing about it.
func TestPrepareMountSentinelsWritesEveryDeclaredMarker(t *testing.T) {
	unsetJail(t)
	root := sentinelState(t)
	md := modsDir(t)
	sentinelModule(t, md, "first", []any{MountSentinelName})
	sentinelModule(t, md, "second", []any{MountSentinelName})
	sentinelModule(t, md, "other", []any{"ca.crt"})
	warnings := captureWarnings(t)

	set := approvedSetFrom(md)
	if errs := set.PrepareMountSentinels(set.Enabled(), ""); len(errs) != 0 {
		t.Fatalf("PrepareMountSentinels() = %v", errs)
	}
	for _, name := range []string{"first", "second"} {
		p := filepath.Join(root, name, MountSentinelName)
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("%s: the declared sentinel was not written: %v", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: sentinel mode = %v, want a regular 0600 file", name, info.Mode())
		}
		dir, err := os.Stat(filepath.Join(root, name))
		if err != nil || dir.Mode().Perm() != 0o700 {
			t.Errorf("%s: state dir = %v (%v), want 0700", name, dir, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "other", MountSentinelName)); !os.IsNotExist(err) {
		t.Errorf("a loophole that does not declare the sentinel got one: %v", err)
	}

	args := set.RuntimeArgsFor(set.Enabled(), "")
	for _, name := range []string{"first", "second"} {
		want := filepath.Join(root, name, MountSentinelName) + ":/var/lib/yolo-jail/loopholes/" +
			name + "/" + MountSentinelName + ":ro"
		if !containsArg(args, want) {
			t.Errorf("%s: sentinel not mounted; want %q in %v", name, want, args)
		}
	}
	for _, w := range *warnings {
		if strings.Contains(w, "skipping state file") && strings.Contains(w, MountSentinelName) {
			t.Errorf("a prepared sentinel was still reported missing: %s", w)
		}
	}
}

// A sentinel is REPLACED, never written through: a symlink planted at its path is swapped for a
// regular file, so the bind mount cannot expose whatever host file the link named.
func TestPrepareMountSentinelsReplacesAPlantedSymlink(t *testing.T) {
	unsetJail(t)
	root := sentinelState(t)
	md := modsDir(t)
	sentinelModule(t, md, "linked", []any{MountSentinelName})
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("do not cross\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := mkdir(t, filepath.Join(root, "linked"))
	if err := os.Symlink(secret, filepath.Join(dir, MountSentinelName)); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	set := approvedSetFrom(md)
	if errs := set.PrepareMountSentinels(set.Enabled(), ""); len(errs) != 0 {
		t.Fatalf("PrepareMountSentinels() = %v", errs)
	}
	info, err := os.Lstat(filepath.Join(dir, MountSentinelName))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("the planted link was not replaced by a regular file: %v %v", info, err)
	}
	if got, _ := os.ReadFile(secret); string(got) != "do not cross\n" {
		t.Errorf("the write went through the link into %s: %q", secret, got)
	}
}

// Nothing is written for a record the argv would not mount: a disabled loophole, and a
// pack-shipped one with no origin gate behind it (the gate the mounts loop applies).
func TestPrepareMountSentinelsSkipsWhatTheArgvWouldNotMount(t *testing.T) {
	unsetJail(t)
	root := sentinelState(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "off"))
	writeManifest(t, mod, map[string]any{
		"name": "off", "description": "x", "transport": "none", "default_enabled": false,
		"state_files": []any{MountSentinelName},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	sentinelModule(t, md, "ungated", []any{MountSentinelName})

	approved := approvedSetFrom(md)
	// The disabled record must LOAD, or the assertion below holds of nothing: a manifest the
	// loader refuses (the retired `enabled` key, say) never reaches PrepareMountSentinels.
	var off *Loophole
	for _, m := range approved.All() {
		if m.Name == "off" {
			off = m
		}
	}
	if off == nil || off.Active() {
		t.Fatalf("the fixture's disabled loophole did not load as an inactive record: %+v", off)
	}
	approved.PrepareMountSentinels(approved.All(), "")
	if _, err := os.Lstat(filepath.Join(root, "off", MountSentinelName)); !os.IsNotExist(err) {
		t.Errorf("a disabled loophole got a sentinel: %v", err)
	}
	ungated := SetOf(approved.All())
	_ = captureWarnings(t)
	if err := os.RemoveAll(filepath.Join(root, "ungated")); err != nil {
		t.Fatal(err)
	}
	ungated.PrepareMountSentinels(ungated.All(), "")
	if _, err := os.Lstat(filepath.Join(root, "ungated", MountSentinelName)); !os.IsNotExist(err) {
		t.Errorf("a pack loophole with no origin gate got a sentinel: %v", err)
	}
}

// THE MISSING-STATE-FILE WARNING NAMES ITS NEXT STEP (docs/reference/happy-path-principle.md),
// and the step depends on who writes the file: a host-scoped daemon's file is restored by
// restarting that daemon, so the line names the one command that does it; a per-jail host
// daemon writes its file when this launch starts it, so the line says the next launch has it
// and names `yolo check` for when it does not; a file nothing yolo runs writes is the
// manifest's to fix; and the sentinel is yolo's own marker, whose failure to write was reported
// above it.
func TestMissingStateFileWarningNamesTheNextStep(t *testing.T) {
	unsetJail(t)
	root := sentinelState(t)
	md := modsDir(t)
	host := mkdir(t, filepath.Join(md, "hosted"))
	writeManifest(t, host, map[string]any{
		"name": "hosted", "description": "x", "transport": "loopback-tls",
		"state_files": []any{"leaf.crt"},
		"host_daemon": map[string]any{"cmd": []any{"/bin/true", "--socket", "{socket}"},
			"publishes": "socket", "scope": "host"},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	perJail := mkdir(t, filepath.Join(md, "perjail"))
	writeManifest(t, perJail, map[string]any{
		"name": "perjail", "description": "x", "transport": "loopback-tls",
		"state_files": []any{"leaf.crt"},
		"host_daemon": map[string]any{"cmd": []any{"/bin/true", "--socket", "{socket}"},
			"publishes": "socket", "scope": "jail"},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	sentinelModule(t, md, "bare", []any{"handmade.txt"})
	sentinelModule(t, md, "marked", []any{MountSentinelName})
	for _, name := range []string{"hosted", "perjail", "bare", "marked"} {
		mkdir(t, filepath.Join(root, name))
	}
	warnings := captureWarnings(t)

	set := approvedSetFrom(md)
	_ = set.RuntimeArgsFor(set.Enabled(), "")
	got := strings.Join(*warnings, "\n")
	for _, want := range []string{
		"loophole hosted: skipping state file, host source missing: " +
			filepath.Join(root, "hosted", "leaf.crt") +
			" — its host daemon writes it: restart that daemon with `yolo host-daemon restart hosted`, then launch again",
		"loophole perjail: skipping state file, host source missing: " +
			filepath.Join(root, "perjail", "leaf.crt") +
			" — its host daemon writes it once this launch starts it, after the mounts are assembled; " +
			"if this repeats on the next launch, run `yolo check` to see why perjail does not",
		"loophole bare: skipping state file, host source missing: " +
			filepath.Join(root, "bare", "handmade.txt") +
			" — nothing yolo runs writes it: create it, or remove \"handmade.txt\" from `state_files` in " +
			filepath.Join(md, "bare", "manifest.jsonc"),
		"loophole marked: skipping state file, host source missing: " +
			filepath.Join(root, "marked", MountSentinelName) +
			" — yolo writes this inert marker before every launch, so the warning above says why it could not; fix that, then launch again",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want the warning\n  %s\ngot:\n%s", want, got)
		}
	}
}
