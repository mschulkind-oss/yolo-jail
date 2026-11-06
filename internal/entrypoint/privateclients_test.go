package entrypoint

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func fixturePrivateBinding() PrivateClientBinding {
	return PrivateClientBinding{Bin: "semver", Generation: strings.Repeat("a", 32), CaptureKey: strings.Repeat("b", 16),
		RuntimeRoot: ".local/lib/yolo-npm/id", Entrypoint: ".local/lib/yolo-npm/id/node_modules/semver/bin/semver.js",
		EntrypointSHA256: "sha256:" + strings.Repeat("c", 64), RuntimeFiles: "sha256:" + strings.Repeat("d", 64)}
}

func writeSemverPack(t *testing.T) string {
	t.Helper()
	packRoot := t.TempDir()
	dir := filepath.Join(packRoot, "audited-semver")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"audited-semver","contributes":[` +
		`{"kind":"program","bin":"semver","via":"npm","package":"semver@7.7.2"},` +
		`{"kind":"program","bin":"unselected-client","via":"npm","package":"unselected-client"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return packRoot
}

// The generated launcher of a bound bin starts exactly the shared bootstrap argv, with no
// installer, refresh or PATH lookup; an unbound bin keeps its ordinary npm launcher.
func TestGeneratedLauncherStartsBoundPrivateClientThroughBootstrap(t *testing.T) {
	home, packRoot := t.TempDir(), writeSemverPack(t)
	b := fixturePrivateBinding()
	value, err := MarshalPrivateClients([]PrivateClientBinding{b})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, PrivateHTTPClientsEnv: value})
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.LaunchDir(), "semver"))
	if err != nil {
		t.Fatal(err)
	}
	want := "exec " + shquote.Join(PrivateClientArgv(b)) + " \"$@\"\n"
	if !strings.HasSuffix(string(got), want) || strings.Contains(string(got), "npm install") || strings.Contains(string(got), "npm view") {
		t.Fatalf("bound launcher is not the bootstrap argv:\n%s", got)
	}
	if PrivateClientArgv(b)[0] != "/opt/yolo-jail/bin/yolo" {
		t.Fatalf("bootstrap is not the absolute shipped binary: %q", PrivateClientArgv(b)[0])
	}
	other, err := os.ReadFile(filepath.Join(e.LaunchDir(), "unselected-client"))
	if err != nil || strings.Contains(string(other), PrivateClientExecVerb) || !strings.Contains(string(other), "npm") {
		t.Fatalf("an unbound bin lost its ordinary launcher (%v):\n%s", err, other)
	}

	// Without the env, the same bin gets the ordinary launcher (ordinary invocations unchanged).
	plain := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": packRoot})
	if err := GenerateAgentLaunchers(plain); err != nil {
		t.Fatal(err)
	}
	ordinary, _ := os.ReadFile(filepath.Join(plain.LaunchDir(), "semver"))
	if strings.Contains(string(ordinary), PrivateClientExecVerb) {
		t.Fatal("an unactivated launch bound the bin to the private bootstrap")
	}
}

func TestGeneratedLaunchersRefuseMalformedPrivateBindings(t *testing.T) {
	for name, value := range map[string]string{
		"garbage":     "{",
		"unknown key": `[{"bin":"semver","extra":1}]`,
		"invalid":     `[{"bin":"semver","generation":"x","capture_key":"","runtime_root":"","entrypoint":"","entrypoint_sha256":"","runtime_files":""}]`,
	} {
		t.Run(name, func(t *testing.T) {
			e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": writeSemverPack(t), PrivateHTTPClientsEnv: value})
			err := GenerateAgentLaunchers(e)
			if err == nil || !strings.Contains(err.Error(), "rerun yolo") {
				t.Fatalf("malformed bindings were not refused with a repair: %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(e.LaunchDir(), "semver")); statErr == nil {
				t.Fatal("a launcher was written beside a refused binding")
			}
		})
	}
}

func TestPrivateClientArgvRoundTripsExactly(t *testing.T) {
	b := fixturePrivateBinding()
	argv := append(PrivateClientArgv(b), "--version")
	got, rest, err := ParsePrivateClientArgs(argv[3:])
	if err != nil || got != b || !slices.Equal(rest, []string{"--version"}) {
		t.Fatalf("round trip = %+v %v %v", got, rest, err)
	}
	for i := 3; i < len(argv)-1; i++ {
		mutated := slices.Clone(argv)
		mutated[i] = "--x" + mutated[i]
		if _, _, err := ParsePrivateClientArgs(mutated[3:]); err == nil {
			t.Errorf("argv word %d renamed (%q) was accepted", i, mutated[i])
		}
		swapped := slices.Clone(argv)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		if _, _, err := ParsePrivateClientArgs(swapped[3:]); err == nil {
			t.Errorf("argv words %d and %d swapped were accepted", i, i+1)
		}
	}
	if _, _, err := ParsePrivateClientArgs(argv[3 : len(argv)-2]); err == nil {
		t.Error("an argv without its -- separator was accepted")
	}
}

func TestVerifyPrivateClientChecksEntrypointAndRuntimeFiles(t *testing.T) {
	store := t.TempDir()
	b := fixturePrivateBinding()
	tree := filepath.Join(store, "entries", b.CaptureKey, "tree")
	entry := filepath.Join(tree, filepath.FromSlash(b.Entrypoint))
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(tree, ".local/lib/yolo-npm/id/node_modules/semver/package.json")
	if err := os.WriteFile(pkg, []byte(`{"version":"7.7.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../semver/bin/semver.js", filepath.Join(tree, ".local/lib/yolo-npm/id/node_modules/.bin")); err != nil {
		t.Fatal(err)
	}
	var err error
	if b.EntrypointSHA256, err = PrivateClientFileDigest(entry); err != nil {
		t.Fatal(err)
	}
	if b.RuntimeFiles, err = PrivateClientRuntimeDigest(tree, b.RuntimeRoot); err != nil {
		t.Fatal(err)
	}
	if got, err := VerifyPrivateClient(store, b); err != nil || got != entry {
		t.Fatalf("verified = %q %v", got, err)
	}
	// A changed runtime file refuses, though the entrypoint is unchanged.
	if err := os.WriteFile(pkg, []byte(`{"version":"7.7.3"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPrivateClient(store, b); err == nil || !strings.Contains(err.Error(), "runtime files") {
		t.Fatalf("a changed runtime file was accepted: %v", err)
	}
	if err := os.WriteFile(pkg, []byte(`{"version":"7.7.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A changed entrypoint refuses.
	if err := os.WriteFile(entry, []byte("console.log(2)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPrivateClient(store, b); err == nil || !strings.Contains(err.Error(), "entrypoint") {
		t.Fatalf("a changed entrypoint was accepted: %v", err)
	}
	// A link along the entrypoint path refuses even when it reaches identical bytes.
	if err := os.WriteFile(entry, []byte("console.log(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Dir(entry)
	moved := binDir + "-real"
	if err := os.Rename(binDir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(moved), binDir); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyPrivateClient(store, b); err == nil {
		t.Fatal("a symlinked entrypoint directory was accepted")
	}
}
