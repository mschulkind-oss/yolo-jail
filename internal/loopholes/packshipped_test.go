package loopholes

// The HOST-SIDE half of the pack-shipped subset: LoadPackLoophole, which is the
// subset applied over the TOLERANT read discovery uses, and PackShippedProblems,
// which is the same subset read off a resolved record.
//
// Every refusal test asserts the message NAMES THE FIX. These land on a pack author
// who cannot read this repo's design docs, so the sentence is the whole interface.

import (
	"path/filepath"
	"strings"
	"testing"
)

// packMod writes a module dir under a fresh loopholes root and returns its path.
func packMod(t *testing.T, name string, manifest map[string]any) string {
	t.Helper()
	mod := mkdir(t, filepath.Join(modsDir(t), name))
	manifest["name"] = name
	writeManifest(t, mod, manifest)
	return mod
}

// R1. jail_env is refused for a pack-shipped loophole and the refusal reaches the
// author through the loader they actually call, not only through the schema package.
func TestLoadPackLoopholeRefusesJailEnv(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"jail_env": map[string]any{"PULSE_SERVER": "unix:/run/pulse/native"},
	})
	_, err := LoadPackLoophole(mod)
	if err == nil {
		t.Fatal("a pack-shipped jail_env loaded")
	}
	for _, want := range []string{`"kind": "env"`, "PULSE_SERVER", "UNCONDITIONAL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q: %v", want, err)
		}
	}
	// The SAME manifest through the ordinary loader is fine: the subset is
	// pack-scoped, and a bundled or user-placed loophole keeps jail_env.
	if _, err := LoadLoophole(mod); err != nil {
		t.Errorf("the non-pack loader refused a jail_env manifest: %v", err)
	}
}

// R2/R3. The bind-mount constraints reach the same loader, on the axis that matters
// (path scope) and the one that is narrower than it looks (writability).
func TestLoadPackLoopholeRefusesOutOfScopeAndWritableBinds(t *testing.T) {
	for _, tc := range []struct {
		what  string
		mount map[string]any
		want  []string
	}{
		{"absolute", map[string]any{"host": "/var/run/docker.sock", "container": "/ctx/d"},
			[]string{"absolute host path", "{loophole_dir}/<file>", "relative to your home"}},
		{"env var", map[string]any{"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/ctx/p"},
			[]string{"expands an environment variable", "rule about spelling"}},
		{"writable", map[string]any{"host": "{loophole_dir}/x", "container": "/ctx/x", "readonly": false},
			[]string{"readonly = false", "omit the key, which defaults to true", "non-REG/DIR/LNK"}},
	} {
		t.Run(tc.what, func(t *testing.T) {
			mod := packMod(t, "acme", map[string]any{"host_bind_mounts": []any{tc.mount}})
			_, err := LoadPackLoophole(mod)
			if err == nil {
				t.Fatalf("a %s bind loaded", tc.what)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not carry %q: %v", want, err)
				}
			}
		})
	}
}

// R4. publishes must be "socket", including when it is merely DEFAULTED.
func TestLoadPackLoopholeRefusesSelfPublishing(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"host_daemon": map[string]any{"cmd": []any{"python3", "{loophole_dir}/srv.py"}},
	})
	_, err := LoadPackLoophole(mod)
	if err == nil {
		t.Fatal("a self-publishing pack daemon loaded")
	}
	for _, want := range []string{`"publishes": "socket"`, "{socket}", "framework", "BUNDLED with yolo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not carry %q: %v", want, err)
		}
	}
}

// The legal pack-shipped shape loads, and resolution still happens: the tokens are
// substituted and the record is usable. A subset that refused the legal shape, or
// admitted it un-resolved, would be worse than no subset.
func TestLoadPackLoopholeResolvesALegalManifest(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"host_daemon": map[string]any{
			"cmd":       []any{"python3", "{loophole_dir}/srv.py", "--socket", "{socket}"},
			"publishes": "socket",
		},
		"host_bind_mounts": []any{map[string]any{"host": "{loophole_dir}/conf", "container": "/etc/acme"}},
	})
	lp, err := LoadPackLoophole(mod)
	if err != nil {
		t.Fatalf("a legal pack-shipped manifest was refused: %v", err)
	}
	if got := lp.HostDaemon.Cmd[1]; strings.Contains(got, "{loophole_dir}") {
		t.Errorf("cmd[1] = %q; LoadPackLoophole must still resolve tokens", got)
	}
	if got := lp.HostBindMount[0].Host; strings.Contains(got, "{loophole_dir}") {
		t.Errorf("bind host = %q; still unresolved", got)
	}
	if !lp.HostBindMount[0].Readonly {
		t.Error("a bind with no readonly key must default to :ro")
	}
}

// VERSION SKEW IS ORTHOGONAL TO THE SUBSET, and this is the test that says so. A
// pack crosses the version boundary by construction (the host CLI and the baked
// entrypoint come from different places), so an unknown key must be SKIPPED and
// reported — never refused — while a field the pack may not ship is refused in the
// same read.
func TestLoadPackLoopholeToleratesSkewWhileEnforcingTheSubset(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"future_key":  "whatever a newer yolo does with this",
		"host_daemon": map[string]any{"cmd": []any{"s", "{socket}"}, "publishes": "socket"},
	})
	lp, err := LoadPackLoophole(mod)
	if err != nil {
		t.Fatalf("an unknown key refused a pack-shipped loophole — that is the `tier` incident: %v", err)
	}
	if len(lp.SkewNotes) != 1 || !strings.Contains(lp.SkewNotes[0], "future_key") {
		t.Errorf("SkewNotes = %v, want one note naming future_key", lp.SkewNotes)
	}

	mod2 := packMod(t, "acme2", map[string]any{
		"future_key": "x",
		"jail_env":   map[string]any{"A": "1"},
	})
	if _, err := LoadPackLoophole(mod2); err == nil || !strings.Contains(err.Error(), "jail_env") {
		t.Fatalf("err = %v; the subset must still fire on a manifest that also has skew", err)
	}
}

// A structural error is still a structural error through this loader: the subset is
// an ADDITION to validation, not a replacement for it.
func TestLoadPackLoopholeStillRefusesAMalformedManifest(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{"transport": "unix-socket"})
	if _, err := LoadPackLoophole(mod); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("err = %v, want the retired-transport refusal with its migration hint", err)
	}
}

// PackShippedProblems on a RESOLVED record is the reporting face (the footprint, the
// pre-flight). It must agree with the loader about what a pack may ship — two
// checkers over one subset is how a refusal and a consent string come to disagree.
func TestPackShippedProblemsOnARecordAgreesWithTheLoader(t *testing.T) {
	mod := packMod(t, "acme", map[string]any{
		"jail_env":    map[string]any{"A": "1"},
		"host_daemon": map[string]any{"cmd": []any{"s"}},
	})
	lp, err := LoadLoophole(mod) // the non-pack loader, so we HAVE a record
	if err != nil {
		t.Fatal(err)
	}
	probs := lp.PackShippedProblems()
	if len(probs) != 2 {
		t.Fatalf("problems = %v, want jail_env + publishes", probs)
	}
	if _, err := LoadPackLoophole(mod); err == nil {
		t.Fatal("the loader admitted what the report refuses")
	}
}
