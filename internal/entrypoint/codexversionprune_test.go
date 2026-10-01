package entrypoint

// codexversionprune_test.go runs A7, the V-axis prune (docs/reference/agent-cli-copies.md), over
// the layout CODEX's installer actually produces, through the launcher generated from the
// SHIPPED codex pack.
//
// WHY IT EXISTS: the prune used to look only in ~/.local/share/<bin>/versions, which is claude's
// layout. Codex's installer (https://chatgpt.com/codex/install.sh) keeps its releases somewhere
// else entirely, so every superseded codex release stayed on disk. The agent-directory-map
// survey measured three of them, 1.2 GiB, in one workspace
// (docs/design/agent-directory-map.md, Appendix B).
//
// THE LAYOUT, read from the installer and from a real jail's home (2026-09-28):
//
//	~/.codex/packages/standalone/releases/<version>-<target>/bin/codex   the program
//	~/.codex/packages/standalone/releases/<version>-<target>/codex       -> bin/codex
//	~/.codex/packages/standalone/current                                  -> releases/<version>-<target>
//	~/.local/bin/codex                                                    -> ~/.codex/packages/standalone/current/bin/codex
//
// Two differences from claude's, and each one alone was enough to make the prune a no-op: the
// directory is not under ~/.local/share, and ~/.local/bin/codex points at the vendor's
// `current` SELECTOR rather than into the releases directory, so a single readlink never lands
// inside it. The live release is only found by resolving the whole chain.
//
// Each cell fails if the codex pack stops declaring its versions directory, if the projection
// or the launcher splice stops carrying it (the prune then looks in ~/.local/share/codex/versions
// and removes nothing), or if the live-release guard stops resolving the `current` selector.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// codexProbe is one temp HOME holding a codex install tree, and the launcher the shipped codex
// pack generates for it.
type codexProbe struct {
	home, releases, script, log string
}

// shippedCodexInstall returns the codex program the shipped codex pack declares.
func shippedCodexInstall(t *testing.T) *packdecl.Install {
	t.Helper()
	for _, p := range testPacksForAgent(t, "codex") {
		installs, _ := p.HonoredInstalls()
		for i := range installs {
			if installs[i].Bin == "codex" {
				return &installs[i]
			}
		}
	}
	t.Fatal("the shipped codex pack installs no codex program")
	return nil
}

// seedCodexReleases lays out codex's installer tree with one release per name (oldest first,
// mtimes staggered so "newest" is decidable), selects names[liveIdx] through `current`, and
// writes the shipped pack's launcher with updates enabled and no stamp, so a run takes the
// update arm: the fake codex answers the declared verb with success, and the prune follows.
func seedCodexReleases(t *testing.T, names []string, liveIdx int) *codexProbe {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	standalone := filepath.Join(home, ".codex", "packages", "standalone")
	p := &codexProbe{
		home:     home,
		releases: filepath.Join(standalone, "releases"),
		script:   filepath.Join(home, "launch-codex"),
		log:      filepath.Join(home, "argv.log"),
	}
	body := "#!/bin/bash\nprintf '%s\\n' \"RAN:$*\" >> " + shellQuoteForTest(p.log) + "\n"
	var dirs []string
	for i, name := range names {
		dir := filepath.Join(p.releases, name+"-x86_64-unknown-linux-musl")
		if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", "codex"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "codex-package.json"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("bin/codex", filepath.Join(dir, "codex")); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(len(names)-i) * time.Hour)
		if err := os.Chtimes(dir, when, when); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	if err := os.Symlink(dirs[liveIdx], filepath.Join(standalone, "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(standalone, "auto-update-version"),
		[]byte(filepath.Base(dirs[liveIdx])), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(standalone, "current", "bin", "codex"),
		filepath.Join(binDir, "codex")); err != nil {
		t.Fatal(err)
	}

	launcher := nativeAgentLauncher("probe", shippedCodexInstall(t), filepath.Join(home, "stamps"),
		filepath.Join(home, "ws", ".yolo", "receipts.jsonl"), "", true, launcherServers{}, nil)
	if err := os.WriteFile(p.script, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *codexProbe) run(t *testing.T) string {
	t.Helper()
	// HERMETIC against the jail this suite may run in: a jail whose packs select codex exports
	// the codex prelaunch login's variables, and the launcher would then ask the real `yolo`
	// for an OpenAI credential. A fake `yolo` first on PATH, and none of those variables, so
	// nothing here reaches a broker or the network.
	fakeBin := filepath.Join(p.home, "fake-bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "yolo"), []byte("#!/bin/bash\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range launcherHermeticEnv() {
		if !strings.HasPrefix(kv, "YOLO_AUTH_PRELAUNCH_") && !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	cmd := exec.Command(p.script)
	cmd.Dir = p.home
	cmd.Env = append(env, "HOME="+p.home,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the codex launcher failed: %v\n%s", err, out)
	}
	return string(out)
}

// remainingReleases lists the release directories still on disk, by version, sorted.
func (p *codexProbe) remainingReleases(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(p.releases)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, strings.TrimSuffix(e.Name(), "-x86_64-unknown-linux-musl"))
	}
	sort.Strings(out)
	return out
}

// TestCodexVersionPruneKeepsNewestKOfItsReleases is the rule claude already gets, applied to
// codex's tree: four releases in, the newest two out, and codex still runs afterwards through
// the vendor's own `current` selector.
func TestCodexVersionPruneKeepsNewestKOfItsReleases(t *testing.T) {
	p := seedCodexReleases(t, []string{"0.156.1", "0.157.0", "0.158.0", "0.159.0"}, 3)
	out := p.run(t)

	got := p.remainingReleases(t)
	want := []string{"0.158.0", "0.159.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("keep-newest-2 over codex's releases left %v, want %v — superseded codex "+
			"releases are not being pruned\n%s", got, want, out)
	}
	if !strings.Contains(out, "codex: removed superseded version 0.156.1-x86_64-unknown-linux-musl") {
		t.Errorf("the prune must name what it removed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(p.home, ".local", "bin", "codex")); err != nil {
		t.Errorf("~/.local/bin/codex no longer resolves after the prune: %v", err)
	}
	if log := readLines(t, p.log); len(log) != 2 || log[0] != "RAN:update" || log[1] != "RAN:" {
		t.Errorf("want the declared update verb and then the launch, got %v\n%s", log, out)
	}
}

// TestCodexVersionPruneNeverRemovesTheSelectedRelease is the rollback shape: `current` selects
// the OLDEST release, so "newest K" and "the live one" disagree. The selected release must
// survive, which it can only do if the guard resolves ~/.local/bin/codex through `current`
// all the way to a releases entry; a guard that stopped at the first link would find no live
// entry at all.
func TestCodexVersionPruneNeverRemovesTheSelectedRelease(t *testing.T) {
	p := seedCodexReleases(t, []string{"0.156.1", "0.157.0", "0.158.0", "0.159.0"}, 0)
	out := p.run(t)

	got := p.remainingReleases(t)
	want := []string{"0.156.1", "0.158.0", "0.159.0"}
	if !slices.Equal(got, want) {
		t.Fatalf("with the oldest release selected the prune left %v, want %v\n%s", got, want, out)
	}
	if log := readLines(t, p.log); len(log) == 0 || log[len(log)-1] != "RAN:" {
		t.Errorf("codex must still run after the prune, argv log %v\n%s", log, out)
	}
}

// readLines returns the non-empty lines of a file, or nil when it does not exist.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
