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

func withRepoBundled(t *testing.T) string {
	t.Helper()
	root := repoRootDir(t)
	orig := BundledLoopholesDir
	dir := filepath.Join(root, "bundled_loopholes")
	BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { BundledLoopholesDir = orig })
	return dir
}

// TestBundledBrokerNeverMountsItsPrivateKey is the INVARIANT for issue #33,
// pinned against the manifest that actually ships. The broker's state dir holds
// the CA's private key; only ca.crt / server.crt / server.key are read in-jail
// (NODE_EXTRA_CA_CERTS, and the terminator's TLS pair). Signing is host-side in
// internal/oauthbroker/cert.go, so ca.key must never appear in the runtime argv.
//
// The state dir is faked with all seven real filenames, so dropping the
// manifest's `state_files` key — or widening it — fails here rather than in a
// jail nobody inspects.
func TestBundledBrokerNeverMountsItsPrivateKey(t *testing.T) {
	unsetJail(t)
	dir := withRepoBundled(t)

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

	lp, err := LoadLoophole(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	// The shipped manifest requires `claude` on PATH; the MOUNT SHAPE is what is
	// under test, and it must hold on any host, CI included.
	lp.Requires = Requires{}

	args := RuntimeArgsFor([]*Loophole{lp}, "podman")
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

func TestBundledManifestsParse(t *testing.T) {
	dir := withRepoBundled(t)
	for _, name := range []string{"audio", "claude-oauth-broker", "host-processes"} {
		lp, err := LoadLoophole(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("load %s: %v", name, err)
			continue
		}
		if lp.Name != name {
			t.Errorf("%s: name = %q", name, lp.Name)
		}
	}
}
