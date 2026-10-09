//go:build linux

package integration

// backgroundadvance_linux_test.go is the real Podman caller cell for XB-D19/XB-D20. The
// declared program/profile are local fakes, the upstream is git+file, and the only build is a
// bounded shell command in the ordinary sealed build jail. No Pi, agent, model or registry runs.
// Unlike the unit SIGTERM fixture, container removal and admission here use the actual CLI.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const (
	xbAgent   = "xb-agent"
	xbBin     = "xbfixture"
	xbExt     = "xb-ext"
	xbKey     = xbExt + "/tree"
	xbInto    = ".xbfixture/tree"
	xbProfile = "xb-offline"
	xbBuild   = `printf 'ready\n' > /workspace/build-ready.tmp; mv /workspace/build-ready.tmp /workspace/build-ready; for i in $(seq 1 3000); do [ -f /workspace/build-release ] && break; sleep 0.1; done; [ -f /workspace/build-release ] || exit 70; printf 'XB_LOCAL_BUILD\n' > built.txt`
)

type xbFixture struct {
	store, outcome, log string
	owned               []xbProcess
}

// Retain birth identity as well as PID: cleanup must never signal a recycled process.
type xbProcess struct {
	pid   int
	birth string
}

type xbOutcome struct {
	Key      string `json:"key"`
	State    string `json:"state"`
	Pid      int    `json:"pid"`
	Log      string `json:"log"`
	To       string `json:"to"`
	Retained string `json:"retained"`
}

func xbWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func xbManifests(t *testing.T, agent, ext, source string) {
	t.Helper()
	xbWrite(t, filepath.Join(agent, "install.sh"), "#!/bin/sh\nexit 1\n")
	// This is the fake declared client, read through bash, not an installed agent launcher.
	xbWrite(t, filepath.Join(agent, "client"), `#!/bin/sh
if [ -f "$HOME/`+xbInto+`/built.txt" ]; then cat "$HOME/`+xbInto+`/built.txt"; else echo XB_NO_TREE; fi
printf 'XB_SETTINGS_BEGIN\n'
cat "$HOME/.xbfixture/settings.json"
printf '\nXB_SETTINGS_END\n'
printf '\nXB_PROFILES=%s\n' "$YOLO_PROFILES"
`)
	xbWrite(t, filepath.Join(agent, "pack.json"), `{"name":"`+xbAgent+`","contributes":[
{"kind":"program","bin":"`+xbBin+`","via":"installer","url":"file:///ctx/packs/`+xbAgent+`/install.sh","protocols":["openai"]},
{"kind":"state","at":".xbfixture","scope":"workspace"},
{"kind":"files","from":"client","into":".xbfixture/client"},
{"kind":"config","config":[{"agent":"`+xbBin+`","name":"settings","codec":"json","path":"~/.xbfixture/settings.json"}]},
{"kind":"provider","name":"`+xbProfile+`","endpoints":{"openai":{"base_url":"http://127.0.0.1:1","wire_api":"openai-chat-completions"}}},
{"kind":"profile","name":"`+xbProfile+`","provider":"`+xbProfile+`"}]}`)
	build, _ := json.Marshal(xbBuild)
	xbWrite(t, filepath.Join(ext, "pack.json"), `{"name":"`+xbExt+`","contributes":[
{"kind":"files","into":"`+xbInto+`","source":"`+source+`","build":`+string(build)+`,"produces":["built.txt"],"fallback":"xb-local-unbuilt"},
{"kind":"config-list","surface":"`+xbBin+`/settings","path":"/packages","add":["~/`+xbInto+`"]}]}`)
}

// Cheap declaration/ownership check; -short never enters requireJail or starts TestMain warmup.
func TestBackgroundAdvanceFixtureDeclaresItsClientProfileAndTreeOwner(t *testing.T) {
	agent, ext := resolvedTempDir(t), resolvedTempDir(t)
	xbManifests(t, agent, ext, "git+file:///tmp/xb-local-upstream?ref=main")
	var packs []*packload.Pack
	for i, dir := range []string{agent, ext} {
		p, problems := packload.LoadDir(dir, []string{xbAgent, xbExt}[i])
		if len(problems) != 0 {
			t.Fatalf("fixture manifest: %v", problems)
		}
		packs = append(packs, p)
	}
	trees := packload.PatchedTrees(packs)
	if len(trees) != 1 || trees[0].Key() != xbKey || trees[0].Owner != xbAgent || !trees[0].ListedInJail {
		t.Fatalf("fixture tree has no declared in-jail owner: %+v", trees)
	}
	if len(packs[0].Decl.Profiles()) != 1 || packs[0].Decl.Profiles()[0].Name != xbProfile {
		t.Fatal("fixture does not declare its selected profile")
	}
}

func newXBFixture(t *testing.T) *xbFixture {
	t.Helper()
	if detectRuntime() != "podman" {
		t.Skip("background build slice 1 requires Podman")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	up := patchFixtureUpstream{t: t, dir: resolvedTempDir(t)}
	up.git("init", "-q", "-b", "main")
	up.release("1.0.0", "ten", "")
	agent, ext := resolvedTempDir(t), resolvedTempDir(t)
	xbManifests(t, agent, ext, "git+file://"+up.dir+"?ref=main")
	packHome(t, `{"packs":[{"source":"file://`+agent+`","name":"`+xbAgent+`"},{"source":"file://`+ext+`","name":"`+xbExt+`"}],"profile":{"`+xbBin+`":"`+xbProfile+`"},"agent_updates":{"`+xbAgent+`":"next-launch"}}`)
	withPrivateFixtureYoloStore(t)
	state := paths.GlobalStorage()
	f := &xbFixture{store: paths.CapturesDir(), outcome: filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(xbKey)+".json"), log: filepath.Join(state, "logs", "background-advance.log")}
	// Registered after the private HOME, before any launch. The fixture stops only its own
	// process identities and private staging containers before that HOME's cleanup can run.
	t.Cleanup(func() {
		// A launch can fail before ready observes its daemon. Recover only this private
		// outcome's owner, and only if its argv still identifies our exact advance.
		if o := f.readOutcome(); o.Pid > 0 && processHasArgs(o.Pid, "internal", "background-advance") && processHasArgs(o.Pid, "--key="+xbKey) {
			if _, stat, ok := readStat(o.Pid); ok && len(stat) >= 20 {
				f.owned = append(f.owned, xbProcess{o.Pid, stat[19]})
			}
		}
		for _, p := range f.owned {
			for _, pid := range processChildren(p.pid) {
				if processHasArgs(pid, "internal", "fork-build-jail") {
					if _, stat, ok := readStat(pid); ok && len(stat) >= 20 {
						f.owned = append(f.owned, xbProcess{pid, stat[19]})
					}
				}
			}
			if p.alive() {
				_ = syscall.Kill(p.pid, syscall.SIGTERM)
			}
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			allEnded := true
			for _, p := range f.owned {
				allEnded = allEnded && p.ended()
			}
			if allEnded {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		for _, p := range f.owned {
			if p.alive() {
				if group, err := syscall.Getpgid(p.pid); err == nil && group == p.pid {
					_ = syscall.Kill(-p.pid, syscall.SIGKILL)
				} else {
					_ = syscall.Kill(p.pid, syscall.SIGKILL)
				}
			}
		}
		staged, _ := filepath.Glob(filepath.Join(f.store, "staging", "fork-*"))
		for _, ws := range staged {
			if info, err := os.Stat(ws); err != nil || !info.IsDir() {
				continue
			}
			if !run.ForceRemoveContainer(naming.FromWorkspace(ws), "podman") {
				t.Errorf("fixture cleanup could not remove %s", ws)
			}
			if err := run.WaitForKeeper(ws, detachedWriterWait); err != nil {
				t.Error(err)
			}
			if err := run.WaitForScratchRemovers(ws, detachedWriterWait); err != nil {
				t.Error(err)
			}
		}
	})
	return f
}

func (f *xbFixture) readOutcome() xbOutcome {
	var o xbOutcome
	if b, err := os.ReadFile(f.outcome); err == nil {
		_ = json.Unmarshal(b, &o)
	}
	return o
}

func xbAwait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(jailTimeout())
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s: %s", jailTimeout(), what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (f *xbFixture) launch(t *testing.T, probe string) result {
	t.Helper()
	return runCommand(t, writeProject(t, `{}`), append(jailRunArgs(), "--", "bash", "-c", probe), withoutFixtureProgramInstall(), withHostSemantics())
}

func xbRemember(t *testing.T, pid int) xbProcess {
	t.Helper()
	_, stat, ok := readStat(pid)
	if !ok || len(stat) < 20 {
		t.Fatalf("cannot read owned process %d", pid)
	}
	return xbProcess{pid, stat[19]}
}

func (p xbProcess) alive() bool {
	alive, _ := xbOwnedThreadGroup(p, xbReadLeaderStat, xbReadTaskGroup)
	return alive
}

func (p xbProcess) ended() bool {
	_, ended := xbOwnedThreadGroup(p, xbReadLeaderStat, xbReadTaskGroup)
	return ended
}

type xbProcStat struct {
	state string
	birth string
}

type xbTaskState struct {
	tid   int
	state string
	birth string
}

// xbOwnedThreadGroup returns an answer only for p's matching birth identity. A dead leader
// alone does not end its process: any live task keeps the owned thread group alive. Unknown or
// changing proc snapshots are neither alive proof (cleanup must not signal uncertain identities)
// nor death proof (the wait must keep polling).
func xbOwnedThreadGroup(
	p xbProcess,
	readLeader func(int) (xbProcStat, bool, error),
	readTasks func(int) ([]xbTaskState, bool, error),
) (alive, ended bool) {
	leader, absent, err := readLeader(p.pid)
	if absent {
		return false, true
	}
	if err != nil {
		return false, false
	}
	if leader.birth != p.birth {
		return false, true
	}
	if !xbTerminalTaskState(leader.state) {
		return true, false
	}

	tasks, stable, err := readTasks(p.pid)
	if err != nil || !stable {
		return false, false
	}
	liveTask := false
	for _, task := range tasks {
		if task.tid <= 0 || task.birth == "" {
			return false, false
		}
		if !xbTerminalTaskState(task.state) {
			liveTask = true
		}
	}
	current, absent, err := readLeader(p.pid)
	if absent || (err == nil && current.birth != p.birth) {
		return false, true
	}
	if err != nil || current.state != leader.state {
		return false, false
	}
	if liveTask {
		return true, false
	}
	return false, true
}

func xbTerminalTaskState(state string) bool {
	return state == "Z" || state == "X" || state == "x"
}

func xbReadLeaderStat(pid int) (xbProcStat, bool, error) {
	return xbReadProcStat(fmt.Sprintf("/proc/%d/stat", pid))
}

func xbReadProcStat(path string) (xbProcStat, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return xbProcStat{}, true, nil
	}
	if err != nil {
		return xbProcStat{}, false, err
	}
	stat, err := xbParseProcStat(b)
	return stat, false, err
}

func xbParseProcStat(b []byte) (xbProcStat, error) {
	s := string(b)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return xbProcStat{}, fmt.Errorf("malformed proc stat: missing command terminator")
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 20 || len(fields[0]) != 1 || fields[19] == "" {
		return xbProcStat{}, fmt.Errorf("malformed proc stat: missing task state or birth")
	}
	return xbProcStat{state: fields[0], birth: fields[19]}, nil
}

func xbReadTaskGroup(pid int) ([]xbTaskState, bool, error) {
	dir := fmt.Sprintf("/proc/%d/task", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	tasks := make([]xbTaskState, 0, len(entries))
	for _, entry := range entries {
		tid, err := strconv.Atoi(entry.Name())
		if err != nil || tid <= 0 {
			return nil, false, fmt.Errorf("invalid task id %q in %s", entry.Name(), dir)
		}
		stat, _, err := xbReadProcStat(fmt.Sprintf("%s/%s/stat", dir, entry.Name()))
		if err != nil {
			return nil, false, err
		}
		// A task disappearing between ReadDir and stat is a transient snapshot, not proof
		// that all owned tasks are terminal. The next bounded xbAwait poll retries it.
		if stat.birth == "" {
			return nil, false, fmt.Errorf("task %d disappeared while reading %s", tid, dir)
		}
		tasks = append(tasks, xbTaskState{tid: tid, state: stat.state, birth: stat.birth})
	}
	again, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	if len(entries) != len(again) {
		return tasks, false, nil
	}
	for i := range entries {
		if entries[i].Name() != again[i].Name() {
			return tasks, false, nil
		}
		current, absent, err := xbReadProcStat(fmt.Sprintf("%s/%s/stat", dir, again[i].Name()))
		if absent || err != nil || current.birth != tasks[i].birth {
			return tasks, false, nil
		}
	}
	return tasks, true, nil
}

// xbWaitOwnedProcessEnd is the signal fixture's actual bounded wait caller; keep xbAwait's
// jailTimeout/poll budget and the separate post-wait pidlock.Held assertion unchanged.
func xbWaitOwnedProcessEnd(t *testing.T, what string, p xbProcess) {
	t.Helper()
	xbAwait(t, what, p.ended)
}

// ready waits for the build's actual marker, never the boot's echo of its command.
func (f *xbFixture) ready(t *testing.T) (xbProcess, xbProcess, string) {
	t.Helper()
	var bg xbProcess
	xbAwait(t, "the CLI's detached background owner", func() bool {
		o := f.readOutcome()
		if o.State != "running" || o.Pid <= 0 {
			return false
		}
		bg = xbRemember(t, o.Pid)
		return true
	})
	f.owned = append(f.owned, bg)
	var child xbProcess
	var staging string
	xbAwait(t, "the sealed local build marker (background log "+f.log+")", func() bool {
		for _, pid := range processChildren(bg.pid) {
			argv, _ := processArgs(pid)
			if !containsArg(argv, "fork-build-jail") {
				continue
			}
			for _, arg := range argv {
				if ws, ok := strings.CutPrefix(arg, "--workspace="); ok {
					staging = ws
				}
			}
			if staging == "" {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(staging, "build-ready")); err == nil && string(b) == "ready\n" {
				child = xbRemember(t, pid)
				return true
			}
		}
		return false
	})
	f.owned = append(f.owned, child)
	if !strings.HasPrefix(staging, filepath.Join(f.store, "staging", "fork-")) {
		t.Fatalf("build staging escaped private store: %s", staging)
	}
	if group, err := syscall.Getpgid(child.pid); err != nil || group != child.pid {
		t.Fatalf("build child pid=%d pgid=%d err=%v", child.pid, group, err)
	}
	present, known := run.ProbeExistingContainer(naming.FromWorkspace(staging), "podman", 5*time.Second)
	if !known || !present {
		t.Fatalf("real sealed build container missing: present=%v known=%v", present, known)
	}
	return bg, child, staging
}

func (f *xbFixture) assertNoBuildFailure(t *testing.T) {
	t.Helper()
	rec, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord(xbKey)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Good != nil {
		t.Fatalf("interrupted/retained build admitted a good tree: %+v", rec.Good)
	}
	for _, o := range rec.Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Fatalf("ownership interruption recorded a compiler failure: %+v", o)
		}
	}
}

func xbContainerGone(t *testing.T, staging string) {
	t.Helper()
	xbAwait(t, "known removal of fixture build jail", func() bool {
		present, known := run.ProbeExistingContainer(naming.FromWorkspace(staging), "podman", 5*time.Second)
		return known && !present
	})
	if err := run.WaitForKeeper(staging, detachedWriterWait); err != nil {
		t.Fatal(err)
	}
}

func (f *xbFixture) finish(t *testing.T, bg xbProcess, staging string) {
	t.Helper()
	xbWrite(t, filepath.Join(staging, "build-release"), "release\n")
	xbAwait(t, "background admission and exit", func() bool { return f.readOutcome().State == "moved" && bg.ended() })
	xbContainerGone(t, staging)
	xbAwait(t, "staging collected after known removal", func() bool { _, err := os.Stat(staging); return os.IsNotExist(err) })
	o := f.readOutcome()
	if o.Key != xbKey || o.To == "" || o.Log != f.log {
		t.Fatalf("background move lost identity/log: %+v", o)
	}
	// To is the upstream's display label, not a content-addressed capture key. The
	// admitted check record owns that key; both live under this fixture's private HOME.
	rec, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord(xbKey)
	if err != nil || rec.Good == nil || rec.Good.Entry == "" {
		t.Fatalf("background move has no admitted good build: record=%+v err=%v", rec, err)
	}
	if want := run.WithPatches(run.GoodBuildLabel(rec.Good), rec.Good.Patches); o.To != want {
		t.Fatalf("background move label %q differs from admitted build %q", o.To, want)
	}
	key := rec.Good.Entry
	receipts, receiptBytes := buildReceiptSnapshot(t, f.store, key)
	if receipts[0].Fork != xbKey || receipts[0].Bin != "tree" || receipts[0].Revision != rec.Good.Commit {
		t.Fatalf("wrong background build receipt: %+v", receipts)
	}
	// This is a fresh bash command through the supported declared profile/program selection,
	// never an actual Pi load. Deleting the real spawn/advance/delivery caller makes it fail.
	r := f.launch(t, `bash "$HOME/.xbfixture/client"; if touch "$HOME/`+xbInto+`/write-probe" 2>/dev/null; then echo XB_WRITABLE; else echo XB_READONLY; fi`)
	if r.rc != 0 {
		t.Fatalf("next fresh launch rc=%d:\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"XB_LOCAL_BUILD", "XB_READONLY", "~/" + xbInto, "built " + o.To, "available for this launch"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("next launch lacks %q:\n%s", want, r.combined())
		}
	}
	if err := xbClientDeliveryProblem(r, "~/"+xbInto); err != nil {
		t.Errorf("%v:\n%s", err, r.combined())
	}
	if strings.Contains(r.combined(), "a background advance is checking and building") {
		t.Errorf("next launch spawned a duplicate despite the completed hourly check:\n%s", r.combined())
	}
	if got := newCaptureEntries(t, f.store, nil, "tree"); len(got) != 1 || got[0] != key {
		t.Errorf("next launch changed the admitted build identity/count: %v, want only %s", got, key)
	}
	if _, after := buildReceiptSnapshot(t, f.store, key); string(after) != string(receiptBytes) {
		t.Error("next launch changed the admitted build's execution receipt")
	}
}

// Read only the settings the fake client actually consumed, not launch disclosures
// that describe its declared fallback even when a built tree was handed.
func xbClientDeliveryProblem(r result, want string) error {
	_, settings, ok := strings.Cut(r.stdout, "XB_SETTINGS_BEGIN\n")
	if !ok {
		return fmt.Errorf("client did not print its settings")
	}
	settings, _, ok = strings.Cut(settings, "\nXB_SETTINGS_END\n")
	if !ok {
		return fmt.Errorf("client did not finish printing its settings")
	}
	var cfg struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal([]byte(settings), &cfg); err != nil {
		return fmt.Errorf("client settings are not readable JSON: %w", err)
	}
	if len(cfg.Packages) != 1 || cfg.Packages[0] != want {
		return fmt.Errorf("client packages = %v, want only %q", cfg.Packages, want)
	}
	return nil
}

// Exercise the fake client's actual script and the same delivery assertion finish uses,
// with a real pack disclosure but without a jail, installer, agent or network.
func TestBackgroundAdvanceClientDeliveryUsesRenderedSettingsNotDisclosure(t *testing.T) {
	agent, ext, home := resolvedTempDir(t), resolvedTempDir(t), resolvedTempDir(t)
	xbManifests(t, agent, ext, "git+file:///tmp/xb-local-upstream?ref=main")
	p, problems := packload.LoadDir(ext, xbExt)
	if len(problems) != 0 {
		t.Fatalf("fixture manifest: %v", problems)
	}
	var disclosure string
	for _, claim := range packload.FootprintOf(p).Claims {
		disclosure += claim.LaunchDisclosureSentence() + "\n"
	}
	if !strings.Contains(disclosure, "falling back to xb-local-unbuilt where no tree is handed") {
		t.Fatal("fixture no longer reproduces the unconditional fallback disclosure")
	}
	if err := os.MkdirAll(filepath.Join(home, xbInto), 0o755); err != nil {
		t.Fatal(err)
	}
	xbWrite(t, filepath.Join(home, xbInto, "built.txt"), "XB_LOCAL_BUILD\n")
	for _, tc := range []struct {
		name, settings, want string
		bad                  bool
	}{
		{"built tree", `{"packages":["~/` + xbInto + `"]}`, "~/" + xbInto, false},
		{"fallback before admission", `{"packages":["xb-local-unbuilt"]}`, "xb-local-unbuilt", false},
		{"fallback after admission", `{"packages":["xb-local-unbuilt"]}`, "~/" + xbInto, true},
		{"tree before admission", `{"packages":["~/` + xbInto + `"]}`, "xb-local-unbuilt", true},
		{"tree and fallback together", `{"packages":["~/` + xbInto + `","xb-local-unbuilt"]}`, "~/" + xbInto, true},
		{"wrong entry despite disclosure naming the tree", `{"packages":["elsewhere"]}`, "~/" + xbInto, true},
		{"malformed settings", `{"packages":`, "~/" + xbInto, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xbWrite(t, filepath.Join(home, ".xbfixture", "settings.json"), tc.settings)
			cmd := exec.Command("bash", filepath.Join(agent, "client"))
			cmd.Env = append(os.Environ(), "HOME="+home, `YOLO_PROFILES={"xb-offline":{"provider":"xb-offline"}}`)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("local fake client: %v", err)
			}
			r := result{stdout: string(out), stderr: disclosure}
			if err := xbClientDeliveryProblem(r, tc.want); (err != nil) != tc.bad {
				t.Fatalf("client delivery error=%v, want error=%v\n%s", err, tc.bad, r.combined())
			}
		})
	}
	if err := xbClientDeliveryProblem(result{stderr: disclosure}, "~/"+xbInto); err == nil {
		t.Error("a declaration without client output passed as delivery")
	}
}

func TestBackgroundAdvanceDetachedLaunchAdmitsOnlyForTheNextFreshJail(t *testing.T) {
	requireJail(t)
	f := newXBFixture(t)
	r := f.launch(t, `bash "$HOME/.xbfixture/client"`)
	if r.rc != 0 {
		t.Fatalf("fallback launch rc=%d:\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"XB_NO_TREE", "xb-local-unbuilt", xbProfile, "a background advance", f.log} {
		if !strings.Contains(r.combined(), want) {
			t.Fatalf("first piped launch lacks %q:\n%s", want, r.combined())
		}
	}
	if err := xbClientDeliveryProblem(r, "xb-local-unbuilt"); err != nil {
		t.Fatalf("first piped launch did not hand the fallback: %v\n%s", err, r.combined())
	}
	bg, _, staging := f.ready(t)
	if !pidlock.Held(filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(xbKey)+".lock")) {
		t.Fatal("running detached advance holds no kernel key lock")
	}
	_, stat, ok := readStat(bg.pid)
	if !ok || stat[2] != strconv.Itoa(bg.pid) || stat[3] != strconv.Itoa(bg.pid) {
		t.Fatalf("background pid %d does not own its session/group: %v", bg.pid, stat)
	}
	_, parentStat, _ := readStat(os.Getpid())
	if len(parentStat) < 17 || stat[16] != parentStat[16] {
		t.Fatalf("background priority changed: bg=%v parent=%v", stat, parentStat)
	}
	for fd, want := range map[int]string{0: "/dev/null", 1: f.log, 2: f.log} {
		if got, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%d", bg.pid, fd)); err != nil || got != want {
			t.Errorf("detached fd %d = %q (%v), want %q", fd, got, err, want)
		}
	}
	if b, err := os.ReadFile(f.log); err != nil || !strings.Contains(string(b), "background advance started") {
		t.Fatalf("detached log is not its actual output: %v\n%s", err, b)
	}
	f.finish(t, bg, staging)
}

func TestBackgroundAdvanceSignalsFenceAndRecoverItsRealBuildWorkspace(t *testing.T) {
	requireJail(t)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		t.Run(sig.String(), func(t *testing.T) {
			f := newXBFixture(t)
			r := f.launch(t, "true")
			if r.rc != 0 {
				t.Fatalf("fallback launch rc=%d:\n%s", r.rc, r.combined())
			}
			bg, child, staging := f.ready(t)
			sentinel := filepath.Join(staging, "ownership-sentinel")
			xbWrite(t, sentinel, "must survive until known removal\n")
			if err := syscall.Kill(bg.pid, sig); err != nil {
				t.Fatal(err)
			}
			xbWaitOwnedProcessEnd(t, "signalled background owner exit", bg)
			if pidlock.Held(filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(xbKey)+".lock")) {
				t.Fatalf("dead background owner kept its kernel key lock\n%s", f.keyLockDiagnostics(bg, child))
			}
			if sig == syscall.SIGTERM {
				xbAwait(t, "cancelled build child's group exit", func() bool { return child.ended() && xbGroupEnded(child.pid) })
				xbContainerGone(t, staging)
				if o := f.readOutcome(); o.State != "interrupted" {
					t.Fatalf("SIGTERM outcome = %+v", o)
				}
			} else {
				// SIGKILL bypasses the daemon's signal handler. The active owner/keeper must
				// fence same-ID Stage, not erase a directory the real container still binds.
				present, known := run.ProbeExistingContainer(naming.FromWorkspace(staging), "podman", 5*time.Second)
				if !known || !present {
					t.Fatalf("SIGKILL left no real orphan container to exercise recovery: present=%v known=%v", present, known)
				}
				r = f.retry(t)
				if r.rc != 0 || f.readOutcome().State != "retained" || f.readOutcome().Retained == "" {
					t.Fatalf("retry under live orphan not retained: rc=%d outcome=%+v\n%s", r.rc, f.readOutcome(), r.combined())
				}
				if !child.alive() {
					t.Fatal("SIGKILL fixture has no surviving build child to fence")
				}
				if !run.ForceRemoveContainer(naming.FromWorkspace(staging), "podman") {
					t.Fatal("could not remove fixture's own orphan container")
				}
				xbContainerGone(t, staging)
				xbAwait(t, "orphan build child ending after its container removal", func() bool { return child.ended() && xbGroupEnded(child.pid) })
			}
			f.assertNoBuildFailure(t)
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "must survive until known removal\n" {
				t.Fatalf("ownership interruption cleared retained staging: %q (%v)", b, err)
			}
			// The runtime is now actually absent, but make only this CLI's probe unknown.
			// A private executable seam, not a production knob; no real runtime state changes.
			realPodman, err := exec.LookPath("podman")
			if err != nil {
				t.Fatal(err)
			}
			bin := resolvedTempDir(t)
			xbWrite(t, filepath.Join(bin, "podman"), "#!/bin/sh\nif [ \"${1-}\" = ps ]; then echo xb-fixture-runtime-unknown >&2; exit 125; fi\nexec "+shquote.Quote(realPodman)+" \"$@\"\n")
			if err := os.Chmod(filepath.Join(bin, "podman"), 0o755); err != nil {
				t.Fatal(err)
			}
			r = f.retry(t, withEnv("PATH="+bin+":"+os.Getenv("PATH")))
			if r.rc != 0 || f.readOutcome().State != "retained" || !strings.Contains(f.readOutcome().Retained, "Restore runtime access") {
				t.Fatalf("unknown runtime not retained: rc=%d outcome=%+v\n%s", r.rc, f.readOutcome(), r.combined())
			}
			if b, err := os.ReadFile(sentinel); err != nil || string(b) != "must survive until known removal\n" {
				t.Fatalf("unknown runtime cleared owned bytes: %q (%v)", b, err)
			}
			f.assertNoBuildFailure(t)
			// A supported fresh launch claims the interrupted/retained report and starts
			// the next background owner. Its successful runtime proof permits same-ID reuse.
			r = f.launch(t, "true")
			if r.rc != 0 {
				t.Fatalf("recovery launch rc=%d:\n%s", r.rc, r.combined())
			}
			newBG, _, newStage := f.ready(t)
			if newStage != staging {
				t.Fatalf("retry escaped deterministic staging: %s != %s", newStage, staging)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("known-gone retry did not clear old staging: %v", err)
			}
			f.finish(t, newBG, newStage)
		})
	}
}

func (f *xbFixture) retry(t *testing.T, opts ...runOption) result {
	t.Helper()
	opts = append([]runOption{withoutFixtureProgramInstall(), withHostSemantics()}, opts...)
	return runCommand(t, resolvedTempDir(t), []string{"internal", "background-advance", "--runtime=podman", "--platform=linux/" + goruntime.GOARCH, "--key=" + xbKey}, opts...)
}

// Failure-only, bounded to identities this fixture already owns. Never dump the
// environment, argv or unrelated descriptors, and never signal a discovered holder.
func (f *xbFixture) keyLockDiagnostics(owner, child xbProcess) string {
	lock := filepath.Join(paths.BackgroundAdvanceDir(), run.PatchedCopySlug(xbKey)+".lock")
	var out strings.Builder
	fmt.Fprintf(&out, "fixture key=%s lock=%s holder-text=%d (informational) outcome=%+v\n", xbKey, lock, pidlock.Holder(lock), f.readOutcome())
	for _, p := range []xbProcess{owner, child} {
		_, stat, ok := readStat(p.pid)
		if !ok || len(stat) < 20 {
			fmt.Fprintf(&out, "owned pid=%d expected-birth=%s: stat unavailable\n", p.pid, p.birth)
			continue
		}
		fmt.Fprintf(&out, "owned pid=%d expected-birth=%s observed-birth=%s state=%s\n", p.pid, p.birth, stat[19], stat[0])
		if stat[19] != p.birth {
			continue // a recycled PID is no longer ours to inspect
		}
		tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", p.pid))
		fmt.Fprintf(&out, "  tasks=%d read-error=%v\n", len(tasks), err)
		for i, task := range tasks {
			if i == 32 {
				out.WriteString("  remaining task states omitted\n")
				break
			}
			b, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/stat", p.pid, task.Name()))
			end := strings.LastIndexByte(string(b), ')')
			if err == nil && end >= 0 {
				fields := strings.Fields(string(b[end+1:]))
				if len(fields) >= 20 {
					fmt.Fprintf(&out, "  tid=%s state=%s birth=%s\n", task.Name(), fields[0], fields[19])
				}
			}
		}
		fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", p.pid))
		fmt.Fprintf(&out, "  fd-directory read-error=%v\n", err)
		matches := 0
		for i, fd := range fds {
			if i == 64 {
				out.WriteString("  remaining descriptors not inspected\n")
				break
			}
			target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", p.pid, fd.Name()))
			if err != nil || target != lock {
				continue
			}
			matches++
			fmt.Fprintf(&out, "  matching key fd=%s path=%s", fd.Name(), target)
			info, _ := os.ReadFile(fmt.Sprintf("/proc/%d/fdinfo/%s", p.pid, fd.Name()))
			for _, line := range strings.Split(string(info), "\n") {
				if strings.HasPrefix(line, "flags:") {
					fmt.Fprintf(&out, " %s", line)
				}
			}
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "  matching key descriptors=%d\n", matches)
	}
	return out.String()
}

func xbGroupEnded(group int) bool {
	for _, row := range processTable() {
		_, stat, ok := readStat(row.pid)
		if ok && len(stat) > 2 && stat[2] == strconv.Itoa(group) && stat[0] != "Z" && stat[0] != "X" {
			return false
		}
	}
	return true
}
