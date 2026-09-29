package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE GITHUB BROKER, END TO END (docs/design/boundary-broker.md §11 step 1, §12).
//
// A real jail selecting the `github` pack, with the `github-broker` loophole enabled, runs a
// bare `gh` — which the pack's `intercept` contribution routes to `yolo gh` — and the host
// broker runs the host's `gh`. Nothing reaches GitHub: the host's `gh` here is a FAKE on
// the launcher's PATH, which the per-jail daemon inherits, and which prints the argv it was
// handed and records it in a log the test reads back.
//
// What the chain proves, link by link: the launch read the workspace's remotes and the
// approval record's scope part held them (a `yolo check --accept-config-changes` recorded
// it, so the launch itself passes no flag); the spawn handed the daemon that launch's scope
// file; the jail's `gh` reached the broker; a read ran the host gh with the canonical argv;
// a refused command, an out-of-scope one and a write never reached the host gh; and every
// call is one `yolo audit` line.
//
// > [!WARNING]
// > Under podman-in-podman the CLI forces `--net=host`, the one mode in which a
// > loopback-forwarding bug cannot reproduce (reachability_test.go's header). A green nested
// > run proves the chain is WIRED; only a real jail on a rootless host, or CI, proves the hop.
//
// podman only: Apple Container is inert for every loopback-tls loophole, and macos-user is
// unmeasured on a Mac.

const fakeGHToken = "gho_yoloIntegrationFakeToken0123456789"

// githubBrokerFixture is one prepared workspace: a git checkout whose origin is on
// github.com, a user config selecting the pack and enabling the loophole, a private state
// dir, and a fake host `gh` first on the launcher's PATH.
type githubBrokerFixture struct {
	dir     string
	opts    []runOption
	argvLog string
	// env is opts' launcher environment as KEY=VALUE pairs, for startYoloBackground.
	env []string
}

func newGitHubBrokerFixture(t *testing.T) githubBrokerFixture {
	t.Helper()
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("the github-broker chain needs podman, and this runtime is %q", rt)
	}
	dir := writeProject(t, `{}`)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = packsrc.CleanGitEnv(os.Environ())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("remote", "add", "origin", "https://github.com/yolo-it/app.git")
	packHome(t, `{
		"packs": ["github"],
		"loopholes": {"github-broker": {"enabled": true}}
	}`)
	macArchivePrivateState(t)

	bin := t.TempDir()
	argvLog := filepath.Join(bin, "argv.log")
	// The stand-in for the host's gh: the version the classifier was measured against, a
	// token for the broker's one start-up read (redaction), and an echo of every other argv.
	// `leak` prints the token, to prove the redaction backstop end to end.
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> '" + argvLog + "'\n" +
		"case \"$1\" in\n" +
		"  --version) echo 'gh version 2.101.0 (yolo integration fake)'; exit 0 ;;\n" +
		"  auth) if [ \"$2\" = token ]; then echo '" + fakeGHToken + "'; exit 0; fi ;;\n" +
		"esac\n" +
		"case \"$*\" in\n" +
		"  *leak*) echo \"fake gh ran: $* " + fakeGHToken + "\"; exit 0 ;;\n" +
		"esac\n" +
		"echo \"fake gh ran: $*\"\n" +
		"echo \"cwd=$(pwd) GH_HOST=$GH_HOST GH_TOKEN=${GH_TOKEN:-unset}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"YOLO_NO_AUTO_IMAGE_REAP=1",
	}
	return githubBrokerFixture{dir: dir, argvLog: argvLog, env: env, opts: []runOption{withEnv(env...)}}
}

// recordScope approves the workspace's current remotes from the host, as a human would with
// `yolo check --accept-config-changes`, and fails unless the check says it recorded want.
func (fx githubBrokerFixture) recordScope(t *testing.T, want string) {
	t.Helper()
	r := runCommand(t, fx.dir, []string{"check", "--no-build", config.AcceptConfigChangesFlag}, fx.opts...)
	if line := "github-broker repository scope recorded: " + want; r.rc != 0 || !strings.Contains(r.combined(), line) {
		t.Fatalf("yolo check --accept-config-changes did not record %q:\nrc %d\n%s", line, r.rc, r.combined())
	}
}

// addRemote adds a remote to the fixture's checkout.
func (fx githubBrokerFixture) addRemote(t *testing.T, name, url string) {
	t.Helper()
	cmd := exec.Command("git", "remote", "add", name, url)
	cmd.Dir = fx.dir
	cmd.Env = packsrc.CleanGitEnv(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adding remote %s: %v\n%s", name, err, out)
	}
}

// scopeFiles counts the launches' scope files on the host.
func scopeFiles(t *testing.T) int {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(paths.GlobalStorageUnder(os.Getenv("HOME")), "broker", "github", "scope"))
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

// brokerDaemonLog is the host daemon's log, for a failure message.
func brokerDaemonLog(t *testing.T) string {
	t.Helper()
	p := filepath.Join(paths.GlobalStorageUnder(os.Getenv("HOME")), "logs", "host-service-github-broker.log")
	b, err := os.ReadFile(p)
	if err != nil {
		return "\n(no host daemon log at " + p + ": " + err.Error() + ")"
	}
	return "\n--- " + p + " ---\n" + string(b)
}

// ghScript runs each gh command in the jail and prints its exit code after a marker, so one
// launch measures them all.
func ghScript(cmds ...string) string {
	var b strings.Builder
	for i, c := range cmds {
		b.WriteString(c + "; echo \"RC" + string(rune('A'+i)) + "=$?\"\n")
	}
	return b.String()
}

func TestGitHubBrokerRunsAReadAgainstTheHostGH(t *testing.T) {
	fx := newGitHubBrokerFixture(t)

	// The HOST check records the scope part, where a launch here would start the broker
	// (BB-D30) — so the launch below passes no --accept-config-changes and still starts.
	fx.recordScope(t, "yolo-it/app")

	script := ghScript(
		"gh pr view 32",                         // A: a read, repository from origin
		"gh pr list -R yolo-it/app --state all", // B: a read, explicit -R
		"gh auth token",                         // C: refused
		"gh pr view 1 -R other/private",         // D: out of scope
		"gh pr comment 32 --body hi",            // E: a write, exit 77
		"gh api repos/yolo-it/app/leak",         // F: a read whose output carries the token
		"type gh",                               // G: gh resolves to the intercept
	)
	r := runCommand(t, fx.dir, []string{"run", "--", "bash", "-lc", script}, fx.opts...)
	if r.rc != 0 {
		t.Fatalf("the launch failed: rc %d\n%s%s", r.rc, r.combined(), brokerDaemonLog(t))
	}
	out := r.stdout
	want := map[string]string{"RCA": "0", "RCB": "0", "RCC": "64", "RCD": "64", "RCE": "77", "RCF": "0", "RCG": "0"}
	for k, v := range want {
		if got := kvLine(out, k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if t.Failed() {
		t.Fatalf("exit codes:\n%s%s", r.combined(), brokerDaemonLog(t))
	}
	// A read ran the host gh with the canonical argv, the repository made explicit, in an
	// empty broker-owned directory, with GH_HOST pinned and no token in its environment.
	for _, s := range []string{
		"fake gh ran: pr view 32 --repo=yolo-it/app",
		"fake gh ran: pr list --repo=yolo-it/app --state=all",
		"GH_HOST=github.com GH_TOKEN=unset",
		"/broker/github/run/",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q:\n%s", s, out)
		}
	}
	if strings.Contains(r.combined(), fakeGHToken) {
		t.Errorf("the host token crossed into the jail:\n%s", r.combined())
	}
	if !strings.Contains(out, "[redacted by yolo]") {
		t.Errorf("the token in F's output was not redacted:\n%s", out)
	}
	if !strings.Contains(out, "block/gh") {
		t.Errorf("`type gh` did not resolve to the intercept in the block dir:\n%s", out)
	}

	// The host gh saw the reads and the broker's own start-up reads, and nothing else.
	logged, _ := os.ReadFile(fx.argvLog)
	for _, line := range strings.Split(strings.TrimSpace(string(logged)), "\n") {
		switch {
		case line == "--version", line == "auth token --hostname github.com",
			strings.HasPrefix(line, "pr view 32"), strings.HasPrefix(line, "pr list"),
			strings.HasPrefix(line, "api repos/yolo-it/app/leak"):
		default:
			t.Errorf("the host gh was run with %q, which the broker must never run", line)
		}
	}

	// Every call is one audit line on the host, and `yolo audit` reads them.
	events, _, err := brokeraudit.Read(filepath.Join(paths.GlobalStorageUnder(os.Getenv("HOME")), "broker", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var sets []string
	for _, e := range events {
		sets = append(sets, e.Set+"/"+e.Outcome)
	}
	for _, s := range []string{"read-only/ran", "refused/refused", "out-of-scope/refused", "read-write/denied"} {
		if !strings.Contains(strings.Join(sets, " "), s) {
			t.Errorf("no %s audit line; have %v", s, sets)
		}
	}
	a := runYoloCLI(t, fx.dir, "audit", "--json", "--set", "read-only")
	var n int
	for _, line := range strings.Split(strings.TrimSpace(a.stdout), "\n") {
		var e brokeraudit.Event
		if json.Unmarshal([]byte(line), &e) == nil && e.Set == "read-only" {
			n++
			if e.Jail == "" || e.Jail == "unknown" {
				t.Errorf("an audit line without the preamble's jail id: %s", line)
			}
		}
	}
	if n != 3 {
		t.Errorf("yolo audit --set read-only listed %d calls, want 3:\n%s", n, a.combined())
	}

	// The launch's scope file went with its daemon.
	scopeDir := filepath.Join(paths.GlobalStorageUnder(os.Getenv("HOME")), "broker", "github", "scope")
	if entries, _ := os.ReadDir(scopeDir); len(entries) != 0 {
		t.Errorf("a scope file outlived its launch: %v", entries)
	}
}

// §12 done criterion 9: a remote added since the approval is not in scope until a fresh
// launch's diff is approved. With no terminal the launch refuses, naming the scope block,
// the git config file and --accept-config-changes; approved, the new repository is readable.
func TestGitHubBrokerScopeChangeIsApprovedAtTheNextFreshLaunch(t *testing.T) {
	fx := newGitHubBrokerFixture(t)
	// The first approval, which the rest of the test is a change against. A check that
	// recorded nothing would leave both remotes reading as added, and the refusal below
	// would still name the new one, so the recording itself is asserted.
	fx.recordScope(t, "yolo-it/app")
	fx.addRemote(t, "upstream", "git@github.com:yolo-up/lib.git")

	r := runCommand(t, fx.dir, []string{"run", "--", "true"}, fx.opts...)
	if r.rc == 0 {
		t.Fatalf("a launch whose scope changed started with no approval:\n%s", r.combined())
	}
	for _, s := range []string{"repository scope", "yolo-up/lib", `remote "upstream"`, "added",
		filepath.Join(fx.dir, ".git", "config"), "--accept-config-changes"} {
		if !strings.Contains(r.combined(), s) {
			t.Errorf("the refusal does not name %q:\n%s", s, r.combined())
		}
	}
	// The approved remote is context, not news: the block marks it unchanged.
	origin := false
	for _, line := range strings.Split(r.combined(), "\n") {
		if strings.Contains(line, "yolo-it/app") && strings.Contains(line, `remote "origin"`) {
			origin = true
			if !strings.Contains(line, "unchanged") || strings.Contains(line, "added") {
				t.Errorf("the approved origin is not shown as unchanged: %q", line)
			}
		}
	}
	if !origin {
		t.Errorf("the refusal's scope block has no row for the approved origin:\n%s", r.combined())
	}

	r = runYolo(t, fx.dir, ghScript("gh pr view 1 -R yolo-up/lib"), fx.opts...)
	if r.rc != 0 || kvLine(r.stdout, "RCA") != "0" {
		t.Fatalf("an approved remote was not readable: rc %d\n%s%s", r.rc, r.combined(), brokerDaemonLog(t))
	}
}

// OQ-BB7 and BB-D32: an ATTACH reads no remotes, never refuses over the scope, and joins the
// broker the fresh launch started, whose scope stays what that launch approved. A remote
// added while the jail runs is out of scope until the next fresh launch shows it, and the
// launch's scope file goes with the launch, not with the attach.
func TestGitHubBrokerAnAttachKeepsTheRunningScope(t *testing.T) {
	fx := newGitHubBrokerFixture(t)
	fx.recordScope(t, "yolo-it/app")

	const release = "release-github-attach"
	first := startYoloBackground(t, "first", fx.dir,
		`echo FIRST-UP-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`,
		fx.env...)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(fx.dir, release), []byte("go\n"), 0o644) })
	// The ANSWER, not the mention: the boot echoes the command before bash runs it, so the
	// sync point is the arithmetic only bash can do.
	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "FIRST-UP-42") {
		select {
		case err := <-first.done:
			t.Fatalf("the first launch exited (%v) before its session ran:\n%s%s", err, first.combined(),
				brokerDaemonLog(t))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the first session never ran within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	awaitLaunchLockReleased(t, fx.dir, first)
	if n := scopeFiles(t); n != 1 {
		t.Fatalf("the running jail's launch left %d scope files, want 1", n)
	}

	fx.addRemote(t, "upstream", "git@github.com:yolo-up/lib.git")
	// No --accept-config-changes: an attach is never asked about the scope.
	r := runCommand(t, fx.dir, []string{"run", "--", "bash", "-lc",
		ghScript("gh pr view 1 -R yolo-up/lib", "gh pr view 2")}, fx.opts...)
	if r.rc != 0 {
		t.Fatalf("the attach failed: rc %d\n%s%s", r.rc, r.combined(), brokerDaemonLog(t))
	}
	if !strings.Contains(r.combined(), "Attaching to existing jail") {
		t.Fatalf("this was not an attach:\n%s", r.combined())
	}
	if strings.Contains(r.combined(), "yolo-up/lib  remote") || strings.Contains(r.combined(), "repository scope changed") {
		t.Errorf("the attach read the remotes or asked about the scope:\n%s", r.combined())
	}
	if got := kvLine(r.stdout, "RCA"); got != "64" {
		t.Errorf("a remote added mid-session answered %q, want 64 until a fresh launch approves it:\n%s",
			got, r.combined())
	}
	if got := kvLine(r.stdout, "RCB"); got != "0" || !strings.Contains(r.stdout, "fake gh ran: pr view 2 --repo=yolo-it/app") {
		t.Errorf("the approved origin answered %q:\n%s", got, r.combined())
	}
	if n := scopeFiles(t); n != 1 {
		t.Errorf("the attach changed the scope files: %d, want 1", n)
	}

	if err := os.WriteFile(filepath.Join(fx.dir, release), []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Fatalf("the first launch exited %d:\n%s", rc, first.combined())
	}
	if n := scopeFiles(t); n != 0 {
		t.Errorf("a scope file outlived its launch: %d left", n)
	}
}
