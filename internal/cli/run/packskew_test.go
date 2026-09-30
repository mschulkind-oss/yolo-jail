package run

// packskew_test.go pins the attach's disposition for a PACK-SET skew: a running jail whose pack
// tree will not load, or whose packs cannot serve what this entry selects (packtree.go's
// attachPackSkew). Both are known differences between the jail and this entry, so both take the
// contract gate's disposition (docs/design/attach-skew-and-contract-guardrails.md, OQ-SK1): at a
// terminal the restart prompt, which continues into a fresh launch; elsewhere a refusal; and
// YOLO_ALLOW_ATTACH_SKEW, the one acknowledgment, which proceeds delivering nothing. Never the
// configured packs in the jail's place, which is what OQ-PK2 (c) keeps from a running jail.

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// packSkewRun is one Run into a workspace whose jail is running, driven by a skewRuntime (which a
// `stop` ends) behind the attach's own questions and a fake podman on PATH for its exec.
type packSkewRun struct {
	rc             int
	rt             *skewRuntime
	stdout, stderr string
	execed         bool
	envFile        string
	built          bool
}

// runPackSkew drives Run's attach against ws's running jail, whose frozen environment is env.
// tty sets both terminal predicates, stdin is what a prompt reads, and getenv is the process
// environment the attach sees beside YOLO_RUNTIME.
func runPackSkew(t *testing.T, ws, env string, tty bool, stdin string, getenv map[string]string,
	mutate func(*Options)) packSkewRun {
	t.Helper()
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "execed")
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\n: > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	r := packSkewRun{rt: &skewRuntime{env: env, execIDs: "1"}}
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Exec = func(argv []string, dir string, e []string, d time.Duration) ExecResult {
		return r.rt.exec(argv, dir, e, d)
	}
	o.IsTTYStdin = func() bool { return tty }
	o.IsTTYStdout = func() bool { return tty }
	o.Stdin = strings.NewReader(stdin)
	o.Getenv = func(k string) string {
		if k == "YOLO_RUNTIME" {
			return "podman"
		}
		return getenv[k]
	}
	// A restart continues into the fresh launch, which stops at the jail prefix: far enough to
	// prove it began, and no nix build.
	o.AcceptConfigChanges = true
	o.BuildJailPrefix = func(string) (string, []string) { r.built = true; return "", nil }
	if mutate != nil {
		mutate(o)
	}
	r.envFile = filepath.Join(paths.WorkspaceHomeState(ws), "yolo-user-env.sh")
	r.rc = Run(*o)
	r.stdout, r.stderr = stdout.String(), stderr.String()
	_, err := os.Stat(marker)
	r.execed = err == nil
	return r
}

// jailWithoutZai stages a fresh launch of ["claude"] as the running jail's tree, then adds zai
// to the config: a zai selection only the configured packs can satisfy.
func jailWithoutZai(t *testing.T) (home, ws, cname, jailTree string) {
	t.Helper()
	home = packHome(t)
	emptyLoopholeDirs(t)
	writeUserPacks(t, home, `["claude"]`)
	ws = t.TempDir()
	cname = yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	fresh := dispatchOptions(t, ws, "podman", &out, &out, nil)
	cfg, ok := fresh.loadAndValidateConfig()
	if !ok {
		t.Fatalf("config:\n%s", out.String())
	}
	fresh.stagingCfg = cfg
	staged, ok := fresh.stageRunPacks(cname)
	if !ok {
		t.Fatalf("staging:\n%s", out.String())
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["claude", "zai"]`)
	return home, ws, cname, staged.root
}

// headline is the first stderr line carrying prefix.
func headline(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, prefix) {
			return l
		}
	}
	return ""
}

// TestAnAttachWhoseJailLacksTheSelectedPackTakesTheDisposition: the jail was launched without
// zai, and this entry selects zai's profile, by -p or by the config's persistent profile.
// Nothing can be composed over the jail's packs, so the attach never proceeds on its own: without a
// terminal it refuses, headed by the pack the jail lacks rather than the composition's own remedy
// (which tells the user to declare a profile their configured zai already declares); the
// acknowledgment proceeds and writes nothing; at a terminal, no refuses and yes restarts the jail
// and continues into the fresh launch.
func TestAnAttachWhoseJailLacksTheSelectedPackTakesTheDisposition(t *testing.T) {
	typed := func(o *Options) { o.ProfileName = "zai" }
	current := "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
	for _, tc := range []struct {
		name    string
		env     string
		tty     bool
		stdin   string
		getenv  map[string]string
		persist bool
		want    string
	}{
		{name: "no terminal", env: current, want: "refused"},
		{name: "no terminal, a jail v0.10.0 launched", env: preGateEnv, want: "refused"},
		{name: "no terminal, the config's profile", env: current, persist: true, want: "refused"},
		{name: "the acknowledgment", env: current, tty: true, stdin: "n\n",
			getenv: map[string]string{AllowAttachSkewEnv: "1"}, want: "acknowledged"},
		{name: "a terminal, declined", env: current, tty: true, stdin: "n\n", want: "declined"},
		{name: "a terminal, yes", env: current, tty: true, stdin: "y\n", want: "restarted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, ws, cname, jailTree := jailWithoutZai(t)
			mutate := typed
			if tc.persist {
				dir := filepath.Join(home, ".config", "yolo-jail")
				body := "{\n  \"packs\": [\"claude\", \"zai\"],\n  \"profile\": {\"claude\": \"zai\"}\n}\n"
				if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				mutate = nil
			}
			r := runPackSkew(t, ws, tc.env, tc.tty, tc.stdin, tc.getenv, mutate)
			switch tc.want {
			case "refused":
				if r.rc != 1 || r.execed || len(r.rt.stops) != 0 {
					t.Fatalf("rc=%d execed=%v stops=%v, want a refusal\nstdout:\n%s\nstderr:\n%s",
						r.rc, r.execed, r.rt.stops, r.stdout, r.stderr)
				}
				head := headline(r.stderr, "Refusing to attach")
				if !strings.Contains(head, "launched without zai") {
					t.Errorf("the refusal's headline does not name the pack the jail lacks:\n%s", head)
				}
				if strings.Contains(head, "must be declared") {
					t.Errorf("the headline is the composition's generic remedy:\n%s", head)
				}
				for _, want := range []string{"'yolo stop'", AllowAttachSkewEnv + "=1", "Added to your config since it launched: zai"} {
					if !strings.Contains(r.stderr, want) {
						t.Errorf("the refusal does not say %q:\n%s", want, r.stderr)
					}
				}
			case "acknowledged":
				if r.rc != 0 || !r.execed || len(r.rt.stops) != 0 {
					t.Fatalf("the acknowledgment must proceed: rc=%d execed=%v stops=%v\n%s", r.rc, r.execed, r.rt.stops, r.stderr)
				}
				if strings.Contains(r.stdout, "Restart jail now") {
					t.Errorf("the acknowledgment asked:\n%s", r.stdout)
				}
				for _, want := range []string{AllowAttachSkewEnv + " is set", "launched without zai", "delivers nothing",
					"The jail's briefing names this difference as well"} {
					if !strings.Contains(r.stderr, want) {
						t.Errorf("the acknowledgment's disclosure does not say %q:\n%s", want, r.stderr)
					}
				}
				// And the briefing the jail's own claude pack gets names it for the session (SK-D15).
				body := claudeBriefingOf(t, cname)
				for _, want := range []string{skewSectionHeading, "launched without zai",
					"- Added to your config since it launched: zai."} {
					if !strings.Contains(body, want) {
						t.Errorf("the refreshed briefing does not say %q:\n%s", want, body)
					}
				}
			case "declined":
				if r.rc != 1 || r.execed || len(r.rt.stops) != 0 {
					t.Fatalf("a declined restart must refuse: rc=%d execed=%v stops=%v\n%s", r.rc, r.execed, r.rt.stops, r.stderr)
				}
				if !strings.Contains(r.stdout, "Restart jail now? [Y/n]") || !strings.Contains(r.stdout, "launched without zai") {
					t.Errorf("the terminal was not asked, naming the pack:\n%s", r.stdout)
				}
			case "restarted":
				if !slices.Equal(r.rt.stops, []string{cname}) {
					t.Fatalf("a yes did not stop the jail: stops=%v\nstdout:\n%s\nstderr:\n%s", r.rt.stops, r.stdout, r.stderr)
				}
				if !r.built || r.execed || strings.Contains(r.stdout, "Attaching to existing jail") {
					t.Errorf("a restart must continue into the fresh launch (built=%v), not attach (execed=%v):\n%s",
						r.built, r.execed, r.stdout)
				}
			}
			if _, err := os.Stat(r.envFile); !os.IsNotExist(err) {
				t.Errorf("the attach wrote the live channel file (%v): nothing composed over packs the jail "+
					"lacks may reach it", err)
			}
			if tc.want != "restarted" {
				if trees := packTreesUnder(t, cname); len(trees) != 1 || trees[0] != jailTree {
					t.Errorf("after the attach the pack-tree root holds %v, want only the jail's %s", trees, jailTree)
				}
			}
		})
	}
}

// legacyTreeThisBuildRefuses leaves what a jail an older yolo launched binds, the shared tree,
// holding a pack whose manifest this build's loader refuses: the retired `launch` kind, the
// same class as v0.10.0's claude, whose `claude_plugins` hook this build removed.
func legacyTreeThisBuildRefuses(t *testing.T, cname string) string {
	t.Helper()
	legacy := paths.LegacyPackStagingDir(cname)
	dir := filepath.Join(legacy, officialStagingDir, "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"claude","contributes":[{"kind":"launch","bin":"claude","flags":["--x"]}]}`)
	if _, err := loadUnrecordedPackTree(legacy); err == nil {
		t.Fatal("this build reads the fixture's manifest, so it no longer stands for one it refuses")
	}
	return legacy
}

// TestAnAttachToAJailWhoseTreeWillNotLoadTakesTheDisposition: a jail's tree that is THERE and will
// not load is a known difference, not "cannot say". Composing from the configured packs would
// deliver into that jail what it does not have (for a v0.10.0 jail, claude's list-valued
// api_key_env_name, which its derive reads as a string), so without a terminal the attach refuses,
// saying it could not read the tree, and the acknowledgment proceeds writing nothing at all: no
// channel, and no skills or briefing refreshed from the configured packs. The jail's tree is left
// byte-identical either way.
func TestAnAttachToAJailWhoseTreeWillNotLoadTakesTheDisposition(t *testing.T) {
	for _, tc := range []struct {
		name   string
		getenv map[string]string
	}{{"no terminal", nil}, {"the acknowledgment", map[string]string{AllowAttachSkewEnv: "1"}}} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			emptyLoopholeDirs(t)
			writeUserPacks(t, home, `["claude"]`)
			ws := t.TempDir()
			cname := yoloruntime.FromWorkspace(ws)
			legacy := legacyTreeThisBuildRefuses(t, cname)
			agents := filepath.Join(paths.AgentsDir(), cname)
			// The attach's own staging goes under pack-trees and is checked apart, below.
			notOwnStaging := func(rel string) bool { return strings.HasPrefix(rel, "pack-trees") }
			before := snapshotTree(t, agents, notOwnStaging)

			r := runPackSkew(t, ws, preGateEnv, false, "", tc.getenv, nil)

			if tc.getenv == nil {
				if r.rc != 1 || r.execed {
					t.Fatalf("rc=%d execed=%v, want a refusal\nstdout:\n%s\nstderr:\n%s", r.rc, r.execed, r.stdout, r.stderr)
				}
				if head := headline(r.stderr, "Refusing to attach"); !strings.Contains(head, "cannot read") {
					t.Errorf("the refusal's headline does not say the jail's packs could not be read:\n%s", head)
				}
			} else {
				if r.rc != 0 || !r.execed {
					t.Fatalf("the acknowledgment must proceed: rc=%d execed=%v\n%s", r.rc, r.execed, r.stderr)
				}
				for _, want := range []string{"delivers nothing", "skills and briefing are not refreshed",
					"No briefing records this difference: the jail's packs could not be read"} {
					if !strings.Contains(r.stderr, want) {
						t.Errorf("the acknowledgment's disclosure does not say %q:\n%s", want, r.stderr)
					}
				}
			}
			if !strings.Contains(r.stderr, "could not read the pack tree it booted from, "+legacy) {
				t.Errorf("the account does not name the tree it could not read:\n%s", r.stderr)
			}
			if strings.Contains(r.stderr, "could not find the pack tree") ||
				strings.Contains(r.stderr, "composes from your configured packs") {
				t.Errorf("a tree that is there and will not load was treated as one that cannot be found:\n%s", r.stderr)
			}
			if _, err := os.Stat(r.envFile); !os.IsNotExist(err) {
				t.Errorf("the attach wrote the live channel file (%v)", err)
			}
			// Nothing under the jail's AGENTS_DIR entry changed: its tree, and the skills and
			// briefing staging it binds. The attach's own staging, under pack-trees, is gone again.
			after := snapshotTree(t, agents, notOwnStaging)
			if d := diffSnapshots(before, after); len(d) != 0 {
				t.Errorf("the attach changed what the jail binds:\n  %s", strings.Join(d, "\n  "))
			}
			if trees := packTreesUnder(t, cname); len(trees) != 0 {
				t.Errorf("the attach left its own staging behind: %v", trees)
			}
		})
	}
}

// TestAnAttachThatCannotFindTheJailsTreeSaysSo: with no record of the jail's tree, or a record
// naming one that is gone, and no shared tree either, the attach cannot say what the jail has. It
// then composes from the configured packs, as every attach did before per-launch trees, and says
// so: the warning is the only sign it did. What it execs and delivers are the configured packs':
// copilot's launch flag, and zai's profile in the live channel file.
func TestAnAttachThatCannotFindTheJailsTreeSaysSo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
	}{{"no record", ""}, {"a record naming a gone tree", "20260101T000000Z-gone"}} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			emptyLoopholeDirs(t)
			writeUserPacks(t, home, `["claude", "zai", "copilot"]`)
			ws := t.TempDir()
			cname := yoloruntime.FromWorkspace(ws)
			if tc.record != "" {
				if err := os.MkdirAll(paths.PackTreeRoot(cname), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(paths.LivePackTreeRecord(cname), []byte(tc.record+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			current := "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
			bin := t.TempDir()
			argvFile := filepath.Join(t.TempDir(), "argv")
			r := runPackSkew(t, ws, current, false, "", map[string]string{"ZAI_API_KEY": "sk-test"}, func(o *Options) {
				o.ProfileName = "zai"
				o.Args = []string{"copilot", "chat"}
				script := "#!/bin/sh\nprintf '%s ' \"$@\" > '" + argvFile + "'\n"
				if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+":/bin:/usr/bin")
			})
			if r.rc != 0 {
				t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
			}
			if !strings.Contains(r.stderr, "could not find the pack tree this jail booted with") {
				t.Errorf("an attach that fell back to the configured packs did not say so:\n%s", r.stderr)
			}
			argv, err := os.ReadFile(argvFile)
			if err != nil || !strings.Contains(string(argv), "copilot --yolo chat") {
				t.Errorf("the attach exec'd %q (%v), want the configured copilot's flag", argv, err)
			}
			body, err := os.ReadFile(r.envFile)
			if err != nil || !strings.Contains(string(body), "zai") {
				t.Errorf("the attach did not deliver the configured zai profile (%v):\n%s", err, body)
			}
		})
	}
}

// TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong is the regression on the LAST
// RELEASE'S REAL PACKS, where the synthetic tree above stands for them: a jail that release
// launched binds the one shared tree, holding that release's packs as its launch staged them
// (`git archive <tag> packs`, the tree packs/releasedecode_test.go reads). An attach by this
// build must leave that tree byte-identical, which is the guard the release-decode allowlist
// cites, and must never compose from the configured packs in its place: it either reads the tree
// as the jail's packs, or, where this build's loader refuses them (v0.10.0's claude declares a
// hook this build removed), takes the disposition, which refuses here with no terminal.
func TestAnAttachToAJailTheLastReleaseLaunchedNeverRidesAlong(t *testing.T) {
	root, release := lastReleaseTag(t)
	home := packHome(t)
	emptyLoopholeDirs(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	legacy := paths.LegacyPackStagingDir(cname)
	archiveReleasePacks(t, root, release, filepath.Join(legacy, officialStagingDir))
	_, loadErr := loadUnrecordedPackTree(legacy)
	before := snapshotTree(t, legacy, nil)

	r := runPackSkew(t, ws, "YOLO_VERSION="+strings.TrimPrefix(release, "v")+"\n", false, "", nil, nil)

	if d := diffSnapshots(before, snapshotTree(t, legacy, nil)); len(d) != 0 {
		t.Errorf("an attach changed the shared tree a jail %s launched binds:\n  %s", release, strings.Join(d, "\n  "))
	}
	if strings.Contains(r.stderr, "composes from your configured packs") {
		t.Errorf("the attach composed from the configured packs in place of the ones a jail %s "+
			"launched has:\n%s", release, r.stderr)
	}
	if loadErr == nil {
		t.Logf("this build reads %s's packs, so the attach reads them as the jail's", release)
		if strings.Contains(r.stderr, "could not read the pack tree") {
			t.Errorf("a tree this build reads was reported unreadable:\n%s", r.stderr)
		}
		return
	}
	t.Logf("this build cannot read %s's packs (%v), so the attach takes the disposition", release, loadErr)
	if r.rc != 1 || r.execed {
		t.Fatalf("rc=%d execed=%v, want a refusal without a terminal\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.execed, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "could not read the pack tree it booted from") {
		t.Errorf("the refusal does not say it could not read the jail's packs:\n%s", r.stderr)
	}
	if _, err := os.Stat(r.envFile); !os.IsNotExist(err) {
		t.Errorf("the attach wrote the live channel file (%v)", err)
	}
}

// lastReleaseTag finds the module root and the newest release tag before this commit, as
// packs/releasedecode_test.go does, and skips outside a git checkout with tags. Under GitHub
// Actions a missing tag fails instead, as it does there.
func lastReleaseTag(t *testing.T) (root, tag string) {
	t.Helper()
	unavailable := func(format string, args ...any) {
		t.Helper()
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf(format+" — CI must fetch full history and tags", args...)
		}
		t.Skipf(format, args...)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		unavailable("no git on PATH to read the last release from")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(wd, "..", "..", "..")
	out, err := exec.Command(git, "-C", root, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*", "HEAD^").Output()
	if err != nil {
		unavailable("no release tag is reachable from HEAD^ (a shallow clone, or no tags fetched)")
	}
	return root, strings.TrimSpace(string(out))
}

// archiveReleasePacks writes each pack directory of the release's packs/ into dest/<name>: the
// shape that release's launch staged its embedded packs in, under the shared tree's _official.
func archiveReleasePacks(t *testing.T, root, tag, dest string) {
	t.Helper()
	raw, err := exec.Command("git", "-C", root, "archive", "--format=tar", tag, "packs").Output()
	if err != nil {
		t.Fatalf("git archive %s packs: %v", tag, err)
	}
	tr := tar.NewReader(bytes.NewReader(raw))
	packs := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading %s's packs: %v", tag, err)
		}
		rel, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(h.Name)), "packs/")
		if !ok || !strings.Contains(rel, "/") && h.Typeflag != tar.TypeDir {
			continue // packs/ itself, or a file beside the pack directories (the embed's Go)
		}
		if !filepath.IsLocal(rel) {
			continue
		}
		name, _, _ := strings.Cut(rel, "/")
		packs[name] = true
		path := filepath.Join(dest, filepath.FromSlash(rel))
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, os.FileMode(h.Mode).Perm()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(packs) == 0 {
		t.Fatalf("%s's packs/ holds no pack directory", tag)
	}
}

// TestAnAcknowledgedPackSkewExecsWithTheJailsOwnLaunchFlags: under the acknowledgment an attach
// whose selection the jail's packs cannot serve delivers nothing, and the command it execs still
// carries the launch flags of the packs the jail booted with. Here the jail has copilot, which the
// config has since dropped for zai, so copilot keeps its flag; the configured packs would run it
// bare.
func TestAnAcknowledgedPackSkewExecsWithTheJailsOwnLaunchFlags(t *testing.T) {
	home := packHome(t)
	emptyLoopholeDirs(t)
	writeUserPacks(t, home, `["claude", "copilot"]`)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	var out bytes.Buffer
	fresh := dispatchOptions(t, ws, "podman", &out, &out, nil)
	cfg, ok := fresh.loadAndValidateConfig()
	if !ok {
		t.Fatalf("config:\n%s", out.String())
	}
	fresh.stagingCfg = cfg
	staged, ok := fresh.stageRunPacks(cname)
	if !ok {
		t.Fatalf("staging:\n%s", out.String())
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatal(err)
	}
	body := "{\n  \"packs\": [\"claude\", \"zai\"],\n  \"profile\": {\"claude\": \"zai\"}\n}\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, argvFile := t.TempDir(), filepath.Join(t.TempDir(), "argv")
	current := "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
	r := runPackSkew(t, ws, current, false, "",
		map[string]string{AllowAttachSkewEnv: "1", "ZAI_API_KEY": "sk-test"}, func(o *Options) {
			o.Args = []string{"copilot", "chat"}
			script := "#!/bin/sh\nprintf '%s ' \"$@\" > '" + argvFile + "'\n"
			if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":/bin:/usr/bin")
		})
	if r.rc != 0 || !strings.Contains(r.stderr, "launched without zai") {
		t.Fatalf("the acknowledged attach did not proceed past a pack skew: rc=%d\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	argv, err := os.ReadFile(argvFile)
	if err != nil || !strings.Contains(string(argv), "copilot --yolo chat") {
		t.Errorf("the attach exec'd %q (%v), want the jail's own copilot flag", argv, err)
	}
	if _, err := os.Stat(r.envFile); !os.IsNotExist(err) {
		t.Errorf("the acknowledged attach wrote the live channel file (%v)", err)
	}
}
