package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// liveapplygrants_test.go pins `yolo config apply` for grant-declaring loopholes
// (docs/reference/loophole-grants.md §4.6, LG-D7; plan T11): a mediated loophole's running
// daemon has its grant file rewritten with what the gate approved, and an unmediated one's grant
// is recorded and said to land at the next restart, since its crossing is container argv.

// grantApplyFixture is a running podman jail of a workspace selecting a journal-shaped (mediated)
// and an audio-shaped (unmediated) grant-declaring pack, launched with no request, its grant
// record written by that launch's gate and the journal daemon's live grant file in its state dir.
// The workspace config then becomes wsTOML. It returns the apply's Options, its output, and that
// grant file's path.
func grantApplyFixture(t *testing.T, userTOML, wsTOML string) (*Options, *strings.Builder, string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "pulse"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "pulse", "native"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	emptyLoopholeDirs(t)
	redirectState(t)
	var modules []loopholes.PackModule
	for name, manifest := range map[string]string{
		"journal": journalShaped(filepath.Join(t.TempDir(), "unused.marker")),
		"audio":   audioShapedManifest,
	} {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "loophole.toml"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "asound.conf"), []byte("# fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		modules = append(modules, loopholes.PackModule{Dir: dir, Pack: name, HostExecApproved: true})
	}
	loopholes.SetPackModules(modules)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigTOMLName), []byte("packs = [\"audio\", \"journal\"]\n"+userTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cname := runtime.FromWorkspace(ws)

	// THE LAUNCH: an empty workspace config, its gate accepted, its effective-config record, and
	// the grant file it handed the journal daemon, which this process stands in for.
	if _, err := config.WriteWorkspaceBootBaseline(ws, jsonx.NewOrderedMap()); err != nil {
		t.Fatal(err)
	}
	launch, launchOut := grantApplyOptions(t, ws)
	launch.AcceptConfigChanges = true
	if !launch.checkConfigChanges(jsonx.NewOrderedMap(), "podman") {
		t.Fatalf("the fixture launch's gate stopped:\n%s", launchOut.String())
	}
	var journal *loopholes.Loophole
	for _, lp := range grantDeclaring(loopholes.NewHostSet(nil)) {
		if lp.Name == "journal" {
			journal = lp
		}
	}
	if journal == nil {
		t.Fatal("the journal fixture is not selected")
	}
	body, err := loopholegrant.Merge(nil, launch.loopholeGrantBlock(journal, cname))
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := loopholes.WritePrivateSettingsSnapshot(journal, body)
	if err != nil {
		t.Fatal(err)
	}
	if _, b, err := loopholegrant.Read(live); err != nil || b.Kinds["read"].Standing != loopholegrant.Requestable {
		t.Fatalf("the launch's grant file is not the fixture's premise (read requestable): %+v, %v", b, err)
	}

	// THE EDIT, then the apply's own Options, a fresh process against the running jail.
	if err := os.WriteFile(filepath.Join(ws, config.WorkspaceTOMLConfigName),
		[]byte(wsTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	o, buf := grantApplyOptions(t, ws)
	return o, buf, live
}

// grantApplyOptions is a host-side Options over ws against a fake podman whose jail is running,
// built as applyFixture builds its own without touching HOME or the selected packs.
func grantApplyOptions(t *testing.T, ws string) (*Options, *strings.Builder) {
	t.Helper()
	buf := &strings.Builder{}
	o := &Options{}
	fillDefaults(o)
	o.Workspace, o.Stdout, o.Stderr = ws, buf, buf
	o.Getenv = func(string) string { return "" }
	o.IsMacOS, o.IsLinux = false, true
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStderr = func() bool { return false }
	o.PerfLoggingConfig = func() bool { return false }
	o.LookPath = func(name string) (string, bool) {
		if name == "podman" {
			return "/usr/bin/podman", true
		}
		return "", false
	}
	answeringPodman(o, minimalPodmanInfo)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "podman" {
			switch argv[1] {
			case "ps":
				return ExecResult{Ran: true, Stdout: "abc123\n"}
			case "inspect":
				return ExecResult{Ran: true}
			}
		}
		return ExecResult{Ran: false}
	}
	o.runtime = "podman"
	return o, buf
}

// bothRequests is a project asking for the journal's read and audio's use; allowAudio is the
// user's ceiling letting a project ask for audio (the journal's read is requestable by default).
const (
	bothRequests = "[requests.journal]\nread = true\n\n[requests.audio]\nuse = true\n"
	allowAudio   = "[allow.audio]\non_request = [\"use\"]\n"
)

func TestConfigApplyRewritesTheGrantFileAndSaysRestartForAudio(t *testing.T) {
	o, buf, live := grantApplyFixture(t, allowAudio, bothRequests)
	answer(o, "y")
	rc := o.configApply(false)
	if rc != 3 {
		t.Errorf("rc = %d, want 3 (audio's grant waits for a restart)\n%s", rc, buf.String())
	}
	_, block, err := loopholegrant.Read(live)
	if err != nil || block == nil {
		t.Fatalf("the live grant file does not read after the apply: %v", err)
	}
	if got := block.Kinds["read"].Standing; got != loopholegrant.Granted {
		t.Errorf("after the apply the running journal daemon's file holds read %q, want granted\n%s", got, buf.String())
	}
	if got := block.Kinds["read_all"].Standing; got != loopholegrant.NotAllowed {
		t.Errorf("read_all became %q, want not_allowed kept", got)
	}
	if block.Pack != "journal" || block.Workspace != o.Workspace || block.PID != os.Getpid() {
		t.Errorf("the rewrite changed the file's identity: %+v", block)
	}
	result := buf.String()
	if i := strings.Index(result, "Config apply result:"); i >= 0 {
		result = result[i:]
	} else {
		t.Fatalf("no result block:\n%s", buf.String())
	}
	var journalLine, audioLine string
	for _, l := range strings.Split(result, "\n") {
		switch {
		case strings.Contains(l, "journal grants"):
			journalLine = l
		case strings.Contains(l, "audio grants"):
			audioLine = l
		}
	}
	if !strings.Contains(journalLine, "+read") || !strings.Contains(journalLine, "applied now") {
		t.Errorf("journal's line %q, want +read applied now\n%s", journalLine, result)
	}
	if !strings.Contains(audioLine, "+use") || !strings.Contains(audioLine, "at next restart") {
		t.Errorf("audio's line %q, want +use at next restart\n%s", audioLine, result)
	}
	if got := config.RecordedLoopholeGrants(o.Workspace); !slicesHave(got["audio"], "use") || !slicesHave(got["journal"], "read") {
		t.Errorf("the apply did not record both grants: %v", got)
	}
}

// A decline rewrites nothing: the daemon keeps what the launch's gate approved.
func TestConfigApplyDeclineLeavesTheGrantFile(t *testing.T) {
	o, buf, live := grantApplyFixture(t, allowAudio, bothRequests)
	answer(o, "n")
	if rc := o.configApply(false); rc != 1 {
		t.Errorf("rc = %d, want 1\n%s", rc, buf.String())
	}
	if _, block, err := loopholegrant.Read(live); err != nil || block.Kinds["read"].Standing != loopholegrant.Requestable {
		t.Errorf("a declined apply rewrote the grant file: %+v, %v", block, err)
	}
}

// The step writes the gate's in-memory result through the seam, never a re-read.
func TestConfigApplyGrantStepGoesThroughTheSeam(t *testing.T) {
	o, buf, live := grantApplyFixture(t, "", bothRequests)
	answer(o, "y")
	var replaced []string
	swapSeam(t, &replaceGrantFileSeam, func(_ context.Context, path string, _, next loopholegrant.Block) error {
		replaced = append(replaced, path+"="+string(next.Kinds["read"].Standing))
		return nil
	})
	o.configApply(false)
	if len(replaced) != 1 || replaced[0] != live+"=granted" {
		t.Errorf("replaced %v, want [%s=granted]\n%s", replaced, live, buf.String())
	}
}

// A grant that only a mediated daemon needs is wholly applied: exit 0, and the `requests` key
// folds into the effective-config record, so `yolo config drift` stops listing it. A request the
// ceiling skips (audio, with no allow line) changes no grant this jail holds and owes no restart.
func TestConfigApplyOfAMediatedGrantOwesNoRestartAndFolds(t *testing.T) {
	o, buf, live := grantApplyFixture(t, "", bothRequests)
	answer(o, "y")
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if _, block, err := loopholegrant.Read(live); err != nil || block.Kinds["read"].Standing != loopholegrant.Granted {
		t.Errorf("the journal daemon's file was not rewritten: %+v, %v", block, err)
	}
	if !strings.Contains(buf.String(), "requests.audio: applied now") {
		t.Errorf("no line for the skipped audio request:\n%s", buf.String())
	}
	boot, ok, err := config.ReadWorkspaceBootBaseline(o.Workspace)
	if err != nil || !ok {
		t.Fatalf("no effective-config record: %v", err)
	}
	if _, has := boot.Get("requests"); !has {
		t.Errorf("the applied requests were not folded into the effective-config record\n%s", buf.String())
	}
}

// A running daemon that already holds what the gate approves is left alone, and the apply says
// there is nothing to apply for it.
func TestConfigApplyLeavesAGrantFileThatAlreadyMatches(t *testing.T) {
	o, buf, _ := grantApplyFixture(t, "", "")
	answer(o, "y")
	swapSeam(t, &replaceGrantFileSeam, func(context.Context, string, loopholegrant.Block, loopholegrant.Block) error {
		t.Error("a grant file that already matches was rewritten")
		return nil
	})
	if rc := o.configApply(false); rc != 0 {
		t.Fatalf("rc = %d, want 0\n%s", rc, buf.String())
	}
	if strings.Contains(buf.String(), "journal grants") {
		t.Errorf("a line for a loophole whose grants did not move:\n%s", buf.String())
	}
}

// With no running daemon found, a mediated grant is recorded and waits, naming why.
func TestConfigApplyMediatedGrantWithNoDaemonWaits(t *testing.T) {
	o, buf, _ := grantApplyFixture(t, "", "[requests.journal]\nread = true\n")
	answer(o, "y")
	swapSeam(t, &liveGrantFilesSeam, func(context.Context, string, string, string) ([]loopholegrant.LiveFile, error) {
		return nil, nil
	})
	if rc := o.configApply(false); rc != 3 {
		t.Fatalf("rc = %d, want 3\n%s", rc, buf.String())
	}
	if !strings.Contains(buf.String(), "journal grants (+read): at next restart (yolo stop, then yolo) — no running daemon of this loophole was found") {
		t.Errorf("no waiting line naming the missing daemon:\n%s", buf.String())
	}
}

func slicesHave(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
