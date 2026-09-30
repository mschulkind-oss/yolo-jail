package entrypoint

// forkpacks_test.go pins the fork rewrite's IN-JAIL call site (loadPackRoot, under LoadJailPacks;
// docs/design/forked-programs-as-packs.md FP-D5): the staged tree holds the base pack's pack.json
// as its author wrote it, so without the rewrite every reader here sees the base's upstream
// delivery for a program a fork builds.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// forkPackTree stages a base pack declaring `pi` via npm and a fork pack building it from source,
// in the no-record layout a hand-built tree has, and returns the tree's root.
func forkPackTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(dir, manifest string) {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pi", `{"name":"pi","contributes":[{"kind":"program","bin":"pi","via":"npm","package":"pi-coding-agent"}]}`)
	write("pi-matt", `{"name":"pi-matt","contributes":[{"kind":"program","bin":"pi","via":"source",
		"fork_of":"pi","source":"git+https://example.invalid/pi-fork?ref=main","build":"make install",
		"produces":[".local/bin/pi"]}]}`)
	return root
}

// TestTheJailsPackLoaderAppliesForks: LoadJailPacks hands every reader the base's program with the
// fork's delivery. Red if loadPackRoot stops calling packload.ApplyForks.
func TestTheJailsPackLoaderAppliesForks(t *testing.T) {
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": forkPackTree(t)})
	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, p := range packs {
		for _, in := range p.Decl.InstallContributions() {
			kinds = append(kinds, p.Name+":"+in.Bin+":"+in.Kind+":"+in.ForkedBy)
		}
	}
	if strings.Join(kinds, ",") != "pi:pi:"+packdecl.InstallKindSource+":pi-matt" {
		t.Errorf("the jail's programs = %v, want pi's one program carrying pi-matt's build", kinds)
	}
}

// forkLauncher generates the launchers for forkPackTree with the given host decision
// (ForkBuildsEnv) and a capture store dir, and returns the home, the launcher and a fake-bin dir
// holding a `yolo` that logs its argv to the returned log and, for capture-materialize, puts a
// program at ~/.local/bin/pi that prints which key it came from.
func forkLauncher(t *testing.T, deliveries string) (home, launcher, fakeBin, argvLog string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home = t.TempDir()
	vars := map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws"),
		"YOLO_PACK_ROOT": forkPackTree(t), CapturesDirEnv: t.TempDir(),
	}
	if deliveries != "" {
		vars[ForkBuildsEnv] = deliveries
	}
	e := NewEnv(vars)
	var stderr bytes.Buffer
	e.Stderr = &stderr
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	fakeBin = filepath.Join(home, "fakebin")
	argvLog = filepath.Join(home, "yolo-argv.log")
	body := `#!/bin/bash
printf '%s\n' "$*" >> ` + shq(argvLog) + `
key=""
for a in "$@"; do case "$a" in --key=*) key="${a#--key=}";; esac; done
mkdir -p "$HOME/.local/bin"
printf '#!/bin/bash\necho FORK_BUILD_RAN %s\n' "$key" > "$HOME/.local/bin/pi"
chmod +x "$HOME/.local/bin/pi"
`
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(e.LaunchDir(), "pi"), fakeBin, argvLog
}

// runForkLauncher runs the launcher with HOME and the fake bin on PATH.
func runForkLauncher(t *testing.T, home, launcher, fakeBin string) (string, int) {
	t.Helper()
	cmd := exec.Command(launcher)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + fakeBin + ":" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("the launcher could not be run: %v\n%s", err, out)
	}
	return string(out), rc
}

// TestTheSourceLauncherNeverDeliversTheBasesProgram: with no key from the host, the forked
// program's launcher prints why and exits non-zero — never the base's npm install in its place.
// Before the rewrite the generator wrote the base's npm launcher here: the upstream program,
// under the fork's name.
func TestTheSourceLauncherNeverDeliversTheBasesProgram(t *testing.T) {
	home, launcher, fakeBin, argvLog := forkLauncher(t, `{"pi":{"reason":"the build of commit abc failed"}}`)
	body, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatalf("no launcher for the forked program: %v", err)
	}
	if strings.Contains(string(body), "npm install") {
		t.Fatalf("the base's npm launcher was written for a forked program:\n%s", body)
	}
	out, rc := runForkLauncher(t, home, launcher, fakeBin)
	if rc == 0 || !strings.Contains(out, "pi is not available in this jail: the build of commit abc failed") {
		t.Errorf("rc=%d, want a refusal naming the host's reason:\n%s", rc, out)
	}
	if _, err := os.Stat(argvLog); err == nil {
		t.Error("the launcher materialized something with no key")
	}
}

// TestTheSourceLauncherMaterializesTheHostsKeyOncePerKey: a key is materialized once and the
// program exec'd; the next run execs without materializing; a new key (the pin moved)
// materializes again. Red if GenerateAgentLaunchers stops reading ForkBuildsEnv or the template
// stops asking for the key.
func TestTheSourceLauncherMaterializesTheHostsKeyOncePerKey(t *testing.T) {
	home, launcher, fakeBin, argvLog := forkLauncher(t, `{"pi":{"key":"k1"}}`)
	out, rc := runForkLauncher(t, home, launcher, fakeBin)
	if rc != 0 || !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Fatalf("rc=%d, want the materialized build of k1 run:\n%s", rc, out)
	}
	calls, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(calls), "internal capture-materialize") || !strings.Contains(string(calls), "--key=k1") ||
		!strings.Contains(string(calls), "--bin=pi") || !strings.Contains(string(calls), "--declared=git+https://example.invalid/pi-fork?ref=main") {
		t.Errorf("the materialize call = %q", calls)
	}
	if out, _ := runForkLauncher(t, home, launcher, fakeBin); !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Errorf("the second run did not exec the build:\n%s", out)
	}
	if calls2, _ := os.ReadFile(argvLog); strings.Count(string(calls2), "capture-materialize") != 1 {
		t.Errorf("the second run materialized again: %q", calls2)
	}

	// The pin moved: the next boot's launcher carries k2, and the home's k1 build is replaced.
	_, launcher2, _, _ := forkLauncherInHome(t, home, `{"pi":{"key":"k2"}}`)
	out, rc = runForkLauncher(t, home, launcher2, fakeBin)
	if rc != 0 || !strings.Contains(out, "FORK_BUILD_RAN k2") {
		t.Errorf("rc=%d, want k2's build after the pin moved:\n%s", rc, out)
	}
}

// TestTheSourceLauncherKeysItsBuildToItsOwnHome: the record of which key a home holds is the
// home's, not the machine's. ~/.cache is bound from the machine's shared cache into every jail
// (run's podmanBaseMounts), while the materialized program lives in the workspace's own home, so
// a key record kept in ~/.cache lets one workspace's materialize vouch for another's files. Here a
// second workspace, sharing the first's ~/.cache as every jail on a machine does, still holds the
// BASE's upstream program from before the fork was selected: its launcher must put the fork's
// build in place, never exec the upstream program under the fork's name.
func TestTheSourceLauncherKeysItsBuildToItsOwnHome(t *testing.T) {
	home1, launcher1, fakeBin, argvLog := forkLauncher(t, `{"pi":{"key":"k1"}}`)
	if out, rc := runForkLauncher(t, home1, launcher1, fakeBin); rc != 0 || !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Fatalf("rc=%d, the first workspace did not run k1's build:\n%s", rc, out)
	}

	home2 := t.TempDir()
	if err := os.Symlink(filepath.Join(home1, ".cache"), filepath.Join(home2, ".cache")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home2, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home2, ".local", "bin", "pi"),
		[]byte("#!/bin/bash\necho UPSTREAM_PI_RAN\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, launcher2, _, _ := forkLauncherInHome(t, home2, `{"pi":{"key":"k1"}}`)
	out, rc := runForkLauncher(t, home2, launcher2, fakeBin)
	if rc != 0 || strings.Contains(out, "UPSTREAM_PI_RAN") || !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Errorf("rc=%d, the second workspace ran %q, want k1's build put in its own home:\n%s", rc,
			strings.TrimSpace(out), out)
	}
	if calls, _ := os.ReadFile(argvLog); strings.Count(string(calls), "capture-materialize") != 2 {
		t.Errorf("want one materialize per home, got: %q", calls)
	}
}

// forkLauncherInHome regenerates the launchers into an existing home, as the next boot does.
func forkLauncherInHome(t *testing.T, home, deliveries string) (string, string, string, string) {
	t.Helper()
	e := NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws"), "YOLO_PACK_ROOT": forkPackTree(t),
		CapturesDirEnv: t.TempDir(), ForkBuildsEnv: deliveries,
	})
	e.Stderr = &bytes.Buffer{}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(e.LaunchDir(), "pi"), "", ""
}

// The catalog accounts for a fork's outputs, so `programs.autoprune` never removes them.
func TestTheCatalogAccountsForAForksOutputs(t *testing.T) {
	in := packdecl.Install{Kind: packdecl.InstallKindSource, Bin: "pi", Produces: []string{
		".npm-global/bin/pi", ".npm-global/lib/node_modules/@acme/pi-fork", ".local/bin/pi-helper", "go/bin/pigo"}}
	for dir, want := range map[string]string{
		".npm-global/lib/node_modules": "@acme/pi-fork", ".local/bin": "pi-helper", "go/bin": "pigo",
	} {
		got := forkOutputsUnder(in, dir, dir == ".npm-global/lib/node_modules")
		if len(got) != 1 || got[0] != want {
			t.Errorf("under %s: %v, want [%s]", dir, got, want)
		}
	}
	if got := forkOutputsUnder(packdecl.Install{Kind: "npm", Produces: in.Produces}, ".local/bin", false); got != nil {
		t.Errorf("a program that is not a fork's declared outputs %v", got)
	}
}

// Through the catalog's production entry: a fork's materialized program in ~/.local/bin is not an
// orphan. Red if the local-bin finder stops reading a fork's outputs.
func TestTheCatalogListsNoForkFile(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": forkPackTree(t)})
	if err := os.MkdirAll(e.LocalBin(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.LocalBin(), "pi"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, o := range InstalledOrphans(e) {
		if o.Name == "pi" {
			t.Errorf("the fork's materialized program is cataloged as an orphan: %+v", o)
		}
	}
}
