package entrypoint

// compilecache_test.go RUNS the compile caches (compilecache.go; pi-extension-store-builds.md
// XB-D30 to XB-D32) through the production generators: Node's compile cache and a pack's
// temporary-directory caches land in the workspace's own home state, survive a jail restart (a
// fresh, empty temporary directory), never replace what is already there, and are pruned.
//
// The program is a script that reports what it was handed; the "node" the launcher resolves for a
// node_floor is a script that runs it. No agent is started and nothing reaches a network. None
// needs a terminal, so macOS's check runs them under its /bin/bash 3.2.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// cacheHarness is one generated launcher for a program that reports its NODE_COMPILE_CACHE and
// what <tmpdir>/jiti is.
type cacheHarness struct {
	home, script, report string
	template             string // "npm", "native" or "fork"
	inst                 packdecl.Install
}

func newCacheHarness(t *testing.T, template string, nodeFloor string, tempCaches ...string) *cacheHarness {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	home := t.TempDir()
	h := &cacheHarness{home: home, script: filepath.Join(home, "launch-tool"),
		report: filepath.Join(home, "report"), template: template,
		inst: packdecl.Install{Bin: "tool", NodeFloor: nodeFloor, TempCaches: tempCaches}}
	realBin := filepath.Join(home, ".local", "bin", "tool")
	switch template {
	case "npm":
		h.inst.Kind, h.inst.Package = "npm", "tool"
		realBin = filepath.Join(home, ".npm-global", "bin", "tool")
	case "native":
		h.inst.Kind, h.inst.InstallerURL = "native", "https://example.invalid/never-fetched.sh"
	case "fork":
		h.inst.Kind, h.inst.ForkedBy = packdecl.InstallKindSource, "forkpack"
		h.inst.Produces = []string{".local/bin/tool"}
		keyDir := filepath.Join(home, ".local", "state", "yolo", "fork-keys")
		mustMkdir(t, keyDir)
		mustWrite(t, filepath.Join(keyDir, "tool"), "k\n", 0o644)
	}
	mustMkdir(t, filepath.Dir(realBin))
	mustWrite(t, realBin, `#!/bin/bash
{
    printf 'NCC=%s\n' "${NODE_COMPILE_CACHE:-}"
    if [ -L "$TMPDIR/jiti" ]; then printf 'JITI=link:%s\n' "$(readlink "$TMPDIR/jiti")"
    elif [ -d "$TMPDIR/jiti" ]; then echo JITI=dir
    else echo JITI=none; fi
    printf 'ARGS=%s\n' "$*"
} > `+shellQuoteForTest(h.report)+`
`, 0o755)
	return h
}

// run renders the launcher (a node_floor's interpreter is a script that runs the program) and runs
// it with HOME, PATH and the given TMPDIR, plus env, returning the program's report.
func (h *cacheHarness) run(t *testing.T, tmp string, env ...string) map[string]string {
	t.Helper()
	stamps := filepath.Join(h.home, ".cache", "yolo-agent-stamps")
	receipts := filepath.Join(h.home, "ws", ".yolo", "receipts.jsonl")
	node := filepath.Join(h.home, "fake-node")
	mustWrite(t, node, "#!/bin/bash\nexec \"$@\"\n", 0o755)
	var body string
	switch h.template {
	case "npm":
		body = strings.Join(npmAgentLauncherSegments("probe", &h.inst, stamps, receipts, false,
			launcherServers{}, nil), execPrefixFor(node))
	case "native":
		body = nativeAgentLauncher("probe", &h.inst, stamps, receipts, "", false, launcherServers{}, nil)
	case "fork":
		body = strings.Join(sourceAgentLauncherSegments(&h.inst, ForkDelivery{Key: "k"}, stamps,
			filepath.Join(h.home, ".local", "state", "yolo", "fork-keys"), receipts, "", false,
			launcherServers{}, nil), execPrefixFor(node))
	}
	mustWrite(t, h.script, body, 0o755)
	_ = os.Remove(h.report)
	cmd := exec.Command(h.script, "--a", "b c")
	cmd.Dir = h.home
	cmd.Env = append([]string{"HOME=" + h.home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp}, env...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the launcher failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(h.report)
	if err != nil {
		t.Fatalf("the program did not run: %v\n%s", err, out)
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		k, v, _ := strings.Cut(l, "=")
		got[k] = v
	}
	if got["ARGS"] != "--a b c" {
		t.Errorf("the program was handed %q, want its own argv", got["ARGS"])
	}
	return got
}

func (h *cacheHarness) cacheDir(parts ...string) string {
	return filepath.Join(append([]string{h.home, CompileCacheDirRel}, parts...)...)
}

// TestCompileCachesSurviveARestart is the change's whole claim, in both templates that run a
// program under Node: the program is handed NODE_COMPILE_CACHE in the workspace's home state, and
// <tmpdir>/jiti links into it; and after a "restart" (a new, empty temporary directory) the next
// start links the same directory again, so what the last start compiled is still there.
func TestCompileCachesSurviveARestart(t *testing.T) {
	for _, template := range []string{"npm", "fork"} {
		t.Run(template, func(t *testing.T) {
			h := newCacheHarness(t, template, "22.19", "jiti")
			first := t.TempDir()
			got := h.run(t, first)
			if got["NCC"] != h.cacheDir("node") {
				t.Errorf("NODE_COMPILE_CACHE = %q, want %q", got["NCC"], h.cacheDir("node"))
			}
			if fi, err := os.Stat(h.cacheDir("node")); err != nil || !fi.IsDir() {
				t.Errorf("Node's compile cache directory was not made: %v", err)
			}
			if got["JITI"] != "link:"+h.cacheDir("tmp", "jiti") {
				t.Errorf("<tmpdir>/jiti is %q, want a link to %s", got["JITI"], h.cacheDir("tmp", "jiti"))
			}
			// What a start compiles lands in the workspace's directory, through the link...
			mustWrite(t, filepath.Join(first, "jiti", "compiled.mjs"), "x", 0o644)
			// ...and a restart's empty temporary directory gets it back.
			second := t.TempDir()
			got = h.run(t, second)
			if got["JITI"] != "link:"+h.cacheDir("tmp", "jiti") {
				t.Errorf("after a restart <tmpdir>/jiti is %q, want the same link", got["JITI"])
			}
			if _, err := os.Stat(filepath.Join(second, "jiti", "compiled.mjs")); err != nil {
				t.Errorf("what the last start compiled is gone after a restart: %v", err)
			}
		})
	}
}

// TestACompileCacheNeverReplacesWhatIsThere: a <tmpdir>/jiti a program already made is left as it
// is, and so is a NODE_COMPILE_CACHE the environment already names.
func TestACompileCacheNeverReplacesWhatIsThere(t *testing.T) {
	h := newCacheHarness(t, "npm", "22.19", "jiti")
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "jiti"))
	mustWrite(t, filepath.Join(tmp, "jiti", "theirs"), "x", 0o644)
	got := h.run(t, tmp, "NODE_COMPILE_CACHE=/elsewhere")
	if got["JITI"] != "dir" {
		t.Errorf("an existing <tmpdir>/jiti became %q", got["JITI"])
	}
	if _, err := os.Stat(filepath.Join(tmp, "jiti", "theirs")); err != nil {
		t.Errorf("what was in the existing <tmpdir>/jiti is gone: %v", err)
	}
	if got["NCC"] != "/elsewhere" {
		t.Errorf("NODE_COMPILE_CACHE the environment named became %q", got["NCC"])
	}
	// A link someone else made is not replaced either, even a dangling one.
	tmp = t.TempDir()
	if err := os.Symlink(filepath.Join(tmp, "nowhere"), filepath.Join(tmp, "jiti")); err != nil {
		t.Fatal(err)
	}
	if got = h.run(t, tmp); got["JITI"] != "link:"+filepath.Join(tmp, "nowhere") {
		t.Errorf("an existing link became %q", got["JITI"])
	}
}

// TestNodesCompileCacheIsOnlyForANodeProgram: a program its launcher does not run under a resolved
// Node (no node_floor, or a native one) is not handed NODE_COMPILE_CACHE, and one with no cache to
// keep makes nothing at all; a native program's declared temporary-directory cache is still kept.
func TestNodesCompileCacheIsOnlyForANodeProgram(t *testing.T) {
	h := newCacheHarness(t, "npm", "")
	if got := h.run(t, t.TempDir()); got["NCC"] != "" || got["JITI"] != "none" {
		t.Errorf("a program with no node_floor and no cache was handed %q, %q", got["NCC"], got["JITI"])
	}
	if _, err := os.Stat(h.cacheDir()); !os.IsNotExist(err) {
		t.Errorf("a program with no cache to keep made %s (err=%v)", h.cacheDir(), err)
	}
	h = newCacheHarness(t, "native", "22.19", "jiti")
	got := h.run(t, t.TempDir())
	if got["NCC"] != "" {
		t.Errorf("a native program was handed NODE_COMPILE_CACHE %q", got["NCC"])
	}
	if got["JITI"] != "link:"+h.cacheDir("tmp", "jiti") {
		t.Errorf("a native program's temporary-directory cache is %q, want the link", got["JITI"])
	}
}

// TestCompileCachesArePruned: a file no program has written for COMPILE_CACHE_MAX_AGE days is
// removed, at most once a day; a fresh one stays.
func TestCompileCachesArePruned(t *testing.T) {
	h := newCacheHarness(t, "npm", "22.19", "jiti")
	old := h.cacheDir("tmp", "jiti", "old.mjs")
	fresh := h.cacheDir("node", "v24", "fresh")
	mustMkdir(t, filepath.Dir(old))
	mustMkdir(t, filepath.Dir(fresh))
	mustWrite(t, old, "x", 0o644)
	mustWrite(t, fresh, "x", 0o644)
	backdatePath(t, old, 10*24*time.Hour)
	h.run(t, t.TempDir())
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a cache file ten days old was not pruned (err=%v)", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a fresh cache file was pruned: %v", err)
	}
	// Once a day: a second start the same day prunes nothing...
	mustWrite(t, old, "x", 0o644)
	backdatePath(t, old, 10*24*time.Hour)
	h.run(t, t.TempDir())
	if _, err := os.Stat(old); err != nil {
		t.Errorf("a second start within a day pruned again: %v", err)
	}
	// ...and a start a day later does.
	backdatePath(t, h.cacheDir(".yolo-pruned"), 25*time.Hour)
	h.run(t, t.TempDir())
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a start a day after the last prune did not prune (err=%v)", err)
	}
}

// TestTheCompileCachesComeAfterTheGateAndBeforeTheExec pins the step's place in every template:
// after the tree gate (a launch it stops makes nothing) and the native template's install-only
// exit (a capture never records a cache), and before the exec.
func TestTheCompileCachesComeAfterTheGateAndBeforeTheExec(t *testing.T) {
	inst := packdecl.Install{Bin: "tool", NodeFloor: "22.19", TempCaches: []string{"jiti"}}
	npm, native, fork := inst, inst, inst
	npm.Kind, npm.Package = "npm", "tool"
	native.Kind, native.InstallerURL = "native", "https://example.invalid/i.sh"
	fork.Kind = packdecl.InstallKindSource
	for name, body := range map[string]string{
		"npm":    strings.Join(npmAgentLauncherSegments("p", &npm, "/s", "/r", true, launcherServers{}, nil), ""),
		"native": nativeAgentLauncher("p", &native, "/s", "/r", "", true, launcherServers{}, nil),
		"fork": strings.Join(sourceAgentLauncherSegments(&fork, ForkDelivery{Key: "k"}, "/s", "/k", "/r", "",
			true, launcherServers{}, nil), ""),
	} {
		step := strings.Index(body, "\n_yolo_compile_caches || true\n")
		gate := strings.Index(body, `if [ -n "$TREE_GATE" ]; then`)
		exec := strings.LastIndex(body, `exec "$REAL_BIN"`)
		if step < 0 || gate < 0 || exec < 0 || !(gate < step && step < exec) {
			t.Errorf("%s: the compile caches must run after the tree gate and before the exec "+
				"(gate %d, step %d, exec %d)", name, gate, step, exec)
		}
		if only := strings.Index(body, `if [ "${`+InstallOnlyEnv+`:-}" = "1" ]; then`); name == "native" &&
			(only < 0 || only > step) {
			t.Errorf("native: the compile caches must come after the install-only exit")
		}
		if !strings.Contains(body, "\nHAS_TEMP_CACHES=1\nTEMP_CACHES=(jiti)\n") {
			t.Errorf("%s: the temporary-directory caches are not baked", name)
		}
		want := map[string]string{"npm": "1", "native": "0", "fork": "1"}[name]
		if !strings.Contains(body, "\nNODE_COMPILE="+want+"\n") {
			t.Errorf("%s: NODE_COMPILE is not %s", name, want)
		}
	}
}

// TestShippedPiLauncherKeepsItsCompileCaches is the CALL-SITE cell: the SHIPPED pi pack declares
// jiti's cache and a node_floor, so `pi` through its generated launcher is handed
// NODE_COMPILE_CACHE in the workspace's home state and finds <tmpdir>/jiti linked there. Red if
// packs/pi drops `temp_caches`, the projection or the generator drops it, or the template stops
// running the step.
func TestShippedPiLauncherKeepsItsCompileCaches(t *testing.T) {
	home, launcher, log := shippedPiLauncher(t)
	tmp := t.TempDir()
	cmd := exec.Command(launcher, "--version")
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp}
	cmd.Stdin = strings.NewReader("")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("the shipped pi launcher failed: %v\n%s", err, out.String())
	}
	ncc, err := os.ReadFile(log + ".ncc")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(ncc)), filepath.Join(home, CompileCacheDirRel, "node"); got != want {
		t.Errorf("pi ran with NODE_COMPILE_CACHE %q, want %q", got, want)
	}
	link, err := os.Readlink(filepath.Join(tmp, "jiti"))
	if want := filepath.Join(home, CompileCacheDirRel, "tmp", "jiti"); err != nil || link != want {
		t.Errorf("<tmpdir>/jiti is %q (%v), want a link to %s", link, err, want)
	}
}
