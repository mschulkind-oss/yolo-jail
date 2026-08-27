package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// shippedLoopholes names every loophole yolo SHIPS and which official pack carries it.
//
// IT HAD A SECOND SHAPE OF ROW UNTIL 2026-08-19 — an empty pack name meant
// `bundled_loopholes/`, the channel yolo's own loophole manifests used to live in. The
// sprint emptied it one conversion at a time and `claude-oauth-broker` was the last one
// out (docs/design/broker-as-a-pack.md OQ-BP4), so every row now names a pack and the
// column that recorded WHICH CHANNEL is gone with the choice it recorded.
//
// The broker's row is the one to read twice: its pack is `claude`, the AGENT pack, not a
// `claude-oauth-broker` pack of its own (loophole-activation.md OQ-A10). The dependency
// is structural — the broker exists to serve claude — so selecting the claude pack IS
// the dependency, and a pack of its own would reinstate a second selection step.
var shippedLoopholes = []struct{ name, pack string }{
	{"claude-oauth-broker", "claude"},
	{"audio", "audio"},
	{"host-processes", "host-processes"},
	{"journal", "journal"},
	{"cgroup-delegate", "cgroup-delegate"},
	{"serial", "serial"},
}

// shippedLoopholeModule resolves one shipped loophole's on-disk module directory.
//
// The REPO tree rather than an embed, unlike its loopholedecl twin, because these
// tests drive the loader — which reads a directory, resolves {loophole_dir}
// against it, and (for the broker) needs sibling files to exist.
func shippedLoopholeModule(t *testing.T, name, pack string) string {
	t.Helper()
	return filepath.Join(repoRootDir(t), "packs", pack, "loopholes", name)
}

// TestShippedBrokerNeverMountsItsPrivateKey is the INVARIANT for issue #33,
// pinned against the manifest that actually ships. The broker's state dir holds
// the CA's private key; only ca.crt / server.crt / server.key are read in-jail
// (NODE_EXTRA_CA_CERTS, and the terminator's TLS pair). Signing is host-side in
// internal/oauthbroker/cert.go, so ca.key must never appear in the runtime argv.
//
// The state dir is faked with all seven real filenames, so dropping the
// manifest's `state_files` key — or widening it — fails here rather than in a
// jail nobody inspects.
//
// IT GOES THROUGH THE GATED Set.RuntimeArgsFor NOW, and that is the pack move's doing
// rather than a style choice. The record used to be labelled SourceBundled by hand,
// which the ungated package-level RuntimeArgsFor honors unconditionally; a
// pack-contributed record is honored only when the caller evaluated the origin gate
// (gateAdmitsCrossing), so a hand-labelled record would produce an EMPTY argv and every
// assertion below would pass vacuously.
func TestShippedBrokerNeverMountsItsPrivateKey(t *testing.T) {
	unsetJail(t)
	isolateModules(t)

	stateRoot := t.TempDir()
	origState := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	t.Cleanup(func() { StateDirFor = origState })

	const name = "claude-oauth-broker"
	stateDir := filepath.Join(stateRoot, name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Every file the live broker state dir carries (measured 2026-08-12).
	all := []string{"ca.crt", "ca.key", "ca.srl", "leaf.cnf", "refresh.lock", "server.crt", "server.key"}
	for _, f := range all {
		if err := os.WriteFile(filepath.Join(stateDir, f), []byte(f), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	module := shippedLoopholeModule(t, name, "claude")
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: module, HostExecApproved: true}}})
	lp, ok := set.Lookup(name)
	if !ok {
		t.Fatalf("the shipped broker manifest at %s was not discovered — a pack-shipped "+
			"loophole yolo cannot load is a jail with no credential path", module)
	}

	args := set.RuntimeArgsFor([]*Loophole{lp}, "podman")
	joined := strings.Join(args, " ")

	// 1. The private key — and every other host-side-only file — stays host-side.
	for _, secret := range []string{"ca.key", "ca.srl", "leaf.cnf", "refresh.lock"} {
		if strings.Contains(joined, secret) {
			t.Errorf("%s must not cross into the jail (issue #33); argv: %v", secret, args)
		}
	}
	// 2. Which is only true because the whole state DIR is no longer mounted.
	if strings.Contains(joined, stateDir+":/var/lib/yolo-jail/loopholes/"+name+":ro") {
		t.Errorf("whole state dir mounted; the narrowing regressed: %v", args)
	}
	// 3. And the three files the jail genuinely needs are still there.
	for _, f := range []string{"ca.crt", "server.crt", "server.key"} {
		want := filepath.Join(stateDir, f) + ":/var/lib/yolo-jail/loopholes/" + name + "/" + f + ":ro"
		if !containsStr(args, want) {
			t.Errorf("required jail file missing: want %q in %v", want, args)
		}
	}
	// 4. The CA still resolves through the state mount, so the in-jail trust
	//    bundle (NODE_EXTRA_CA_CERTS -> $HOME/.yolo-ca-bundle.crt) is unchanged.
	wantEnv := "NODE_EXTRA_CA_CERTS=/var/lib/yolo-jail/loopholes/" + name + "/ca.crt"
	if !containsStr(args, wantEnv) {
		t.Errorf("want %q in %v", wantEnv, args)
	}
}

// TestShippedManifestsParse reads every shipped manifest through the PACK loader, which
// is the loader every one of them is now read by in production.
//
// LoadPackLoophole rather than LoadLoophole, and the difference is the subset: a manifest
// yolo ships that exceeds what a pack may ship would load fine through the permissive
// loader here and then VANISH at launch, taking its daemon, its endpoint and its env var
// with it and reporting nothing.
func TestShippedManifestsParse(t *testing.T) {
	for _, s := range shippedLoopholes {
		lp, err := LoadPackLoophole(shippedLoopholeModule(t, s.name, s.pack))
		if err != nil {
			t.Errorf("load %s (packs/%s): %v", s.name, s.pack, err)
			continue
		}
		if lp.Name != s.name {
			t.Errorf("%s: name = %q", s.name, lp.Name)
		}
	}
}

// TestEveryShippedLoopholeHasARow is the forcing function the table needs to be worth
// having: every loophole directory under packs/*/loopholes/ must be listed.
//
// Without it the table is a whitelist of what somebody remembered, and both tests above
// iterate it — so a NEW shipped loophole would be parsed by neither and nothing would say
// so. Adding a loophole is not a defect, so the failure asks for a ROW.
func TestEveryShippedLoopholeHasARow(t *testing.T) {
	listed := map[string]string{}
	for _, s := range shippedLoopholes {
		listed[s.name] = s.pack
	}

	packsRoot := filepath.Join(repoRootDir(t), "packs")
	packDirs, err := os.ReadDir(packsRoot)
	if err != nil {
		t.Fatalf("reading %s: %v", packsRoot, err)
	}
	found := 0
	for _, p := range packDirs {
		if !p.IsDir() {
			continue
		}
		// Most official packs ship no loophole at all (the agent packs), so an
		// unreadable loopholes/ dir is the ordinary case rather than a fault.
		mods, err := os.ReadDir(filepath.Join(packsRoot, p.Name(), "loopholes"))
		if err != nil {
			continue
		}
		for _, m := range mods {
			if !m.IsDir() {
				continue
			}
			found++
			pack, ok := listed[m.Name()]
			if !ok {
				t.Errorf("packs/%s/loopholes/%s ships and shippedLoopholes has no row for it — "+
					"every test in this file iterates that table, so an unlisted loophole's "+
					"manifest is never loaded and never name-checked. Add the row.",
					p.Name(), m.Name())
				continue
			}
			if pack != p.Name() {
				t.Errorf("shippedLoopholes says %q lives in packs/%s, but it is in packs/%s — a "+
					"stale row sends every reader of it to the wrong tree", m.Name(), pack, p.Name())
			}
		}
	}
	if found == 0 {
		t.Fatal("no loophole modules found under packs/*/loopholes — this test would then " +
			"pass over an empty tree, which is the one outcome it must not have")
	}
	if found != len(shippedLoopholes) {
		t.Errorf("packs/ carries %d loophole modules and the table has %d rows", found, len(shippedLoopholes))
	}
}
