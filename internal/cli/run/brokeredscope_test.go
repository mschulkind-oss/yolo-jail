package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// brokeredFixture records one brokered pack loophole whose "daemon" copies the scope file
// it is handed to marker and exits, so the test can read back exactly what the launch
// handed it; and a workspace whose .git/config names a GitHub remote.
func brokeredFixture(t *testing.T) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	return brokeredFixtureWith(t, true)
}

// brokeredFixtureWith is brokeredFixture with the pack module's host-exec approval chosen:
// false is a fetched pack whose claim to run host code was never approved, so the origin
// gate keeps its daemon from starting.
func brokeredFixtureWith(t *testing.T, hostExecApproved bool) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	return brokeredFixtureOn(t, hostExecApproved, "github.com")
}

// brokeredFixtureOn is brokeredFixtureWith with the manifest's `brokered.remote_host` chosen:
// "*" is a source on any host, whose scope is the workspace's remote URLs.
func brokeredFixtureOn(t *testing.T, hostExecApproved bool, remoteHost string) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	return brokeredFixtureBlock(t, hostExecApproved, `{"source": "gbsrc", "remote_host": "`+remoteHost+`"}`)
}

// brokeredFixtureGrants is a source on any host that defines the `create-branch` grant kind, as
// the git pack's manifest does (docs/design/git-broker-plan.md ID-10).
func brokeredFixtureGrants(t *testing.T) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	return brokeredFixtureBlock(t, true, `{"source": "gbsrc", "remote_host": "*", "grants": ["create-branch"]}`)
}

// grantKindsConfig is a user config allowing source's grant kinds (`brokered.<source>.grant_kinds`).
func grantKindsConfig(source string, kinds ...string) *jsonx.OrderedMap {
	list := make([]any, len(kinds))
	for i, k := range kinds {
		list[i] = k
	}
	src := jsonx.NewOrderedMap()
	src.Set("grant_kinds", list)
	sources := jsonx.NewOrderedMap()
	sources.Set(source, src)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("brokered", sources)
	return cfg
}

// brokeredFixtureBlock is brokeredFixtureOn with the manifest's whole `brokered` block chosen.
func brokeredFixtureBlock(t *testing.T, hostExecApproved bool, block string) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	isolatePackModules(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	marker = filepath.Join(t.TempDir(), "handed")
	mod := filepath.Join(t.TempDir(), "gb")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "gb", "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
	  "brokered": ` + block + `}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: hostExecApproved}})

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The user's read grant for every workspace: without it the broker grants nothing and
	// stays out of the gate (BB-D110), which no fixture here is about.
	writeUserConfigOf(t, jsonx.NewOrderedMap())
	buf = &strings.Builder{}
	o = &Options{}
	fillDefaults(o)
	o.Workspace, o.Stdout, o.Stderr = ws, buf, buf
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStderr = func() bool { return o.IsTTYStdin() }
	o.IsMacOS = false
	// The fixture's daemon (/bin/cp) exits 0 without serving, so readiness polls out its whole
	// deadline; without this each test waits out the 5 s production default (loopholesruntime.go).
	o.ServiceReadyTimeout = 300 * time.Millisecond
	return o, buf, marker
}

// gbOn is the merged config a launch composes for a workspace whose per-workspace file turns the
// fixture's brokered loophole on: the only way a brokered loophole is on
// (docs/design/boundary-broker.md OQ-BB13), since its manifest may not default it on.
func gbOn() *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	block := jsonx.NewOrderedMap()
	block.Set("gb", entry)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", block)
	return cfg
}

// TestADeclineNamesTheNextStep is the happy-path rule at the prompt's `N`
// (docs/reference/happy-path-principle.md rule 1): it used to print "Config changes rejected.
// Exiting." and nothing else. A declined repository scope names the command that launches the
// project without the broker (deselecting its pack: a broker has no switch, BB-D106); a declined
// config change names the file it is in and that the next
// launch asks again; both, when both changed. Driven through the gate's real prompter, as a
// terminal answers it.
func TestADeclineNamesTheNextStep(t *testing.T) {
	for _, c := range []struct {
		name                        string
		configChanged, scopeChanged bool
		merged                      *jsonx.OrderedMap
		want, not                   []string
	}{
		{"scope only", false, true, gbOn(),
			[]string{"Repository scope changes rejected; nothing was recorded. Exiting.",
				"To launch without gb instead, take the pack that ships it out of `packs` in "},
			[]string{"The workspace config change is in", " here"}},
		{"config and scope", true, true, gbOn(),
			[]string{"Workspace config and repository scope changes rejected; nothing was recorded. Exiting.",
				"The workspace config change is in ", "yolo-jail.jsonc: undo it there, or answer y at the next launch, which asks again.",
				"take the pack that ships it out of `packs`"},
			[]string{" here", "yolo loopholes disable"}},
		{"config only", true, false, jsonx.NewOrderedMap(),
			[]string{"Config changes rejected; nothing was recorded. Exiting.",
				"yolo-jail.jsonc: undo it there, or answer y at the next launch, which asks again."},
			[]string{"take the pack that ships it", "repository scope changes"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, buf, _ := brokeredFixture(t)
			// The disable step names the workspace, so it works from wherever it is pasted: a
			// next step saying "here" sent a reader who had moved to another folder after it.
			for i := range c.want {
				c.want[i] = strings.ReplaceAll(c.want[i], "<ws>", shquote.QuoteDisplay(o.Workspace))
			}
			o.IsTTYStdin = func() bool { return true }
			o.Stdin = strings.NewReader("n\n")
			if !c.scopeChanged {
				// The broker is in play wherever its pack is selected (BB-D106), so a config-only
				// change is one whose broker holds no read grant: out of the gate (BB-D110).
				writeUserConfigOf(t, mapOf(t, `{"brokered": {"gbsrc": {"grants": []}}}`))
			}
			if c.configChanged {
				if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), nil); err != nil {
					t.Fatal(err)
				}
				writeWorkspaceConfig(t, o.Workspace, `{"packages": ["jq"]}`)
			}
			if o.checkConfigChanges(c.merged, "podman") {
				t.Fatalf("an `n` was accepted:\n%s", buf.String())
			}
			out := buf.String()
			for _, want := range c.want {
				if !strings.Contains(out, want) {
					t.Errorf("the decline does not say %q:\n%s", want, out)
				}
			}
			for _, not := range c.not {
				if strings.Contains(out, not) {
					t.Errorf("the decline says %q, which it must not:\n%s", not, out)
				}
			}
			if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
				t.Error("a declined scope was recorded")
			}
		})
	}
}

// The gate reads the workspace's remotes when a brokered loophole will start, puts the
// labeled scope block in front of the reader, and a launch with no terminal refuses.
func TestTheGateReadsTheRemotesOfABrokerThatWillStart(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatal("a first launch with a GitHub remote and no terminal must refuse")
	}
	out := buf.String()
	for _, want := range []string{"repository scope", "gb repository scope, read from",
		`+ o/r  remote "origin"  added`, "--accept-config-changes", filepath.Join(o.Workspace, ".git", "config")} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not show %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("a refused launch recorded the scope part")
	}
}

// The interactive y/N (BB-D31): at a terminal, a change to the scope alone gets the
// scope-only header and question, the labeled block comes first, and one `y` records the
// scope part. Without this pin, a prompt that dropped the block or kept the config-only
// wording asked "Workspace config changed" over an empty diff, and one `y` approved an
// owner/repo nobody was shown.
func TestTheInteractivePromptShowsTheScopeFirstAndRecordsIt(t *testing.T) {
	for _, c := range []struct {
		name             string
		wsCfg            string
		header, question string
	}{
		{"scope only", `{}`, "Repository scope changed since last run:",
			"Accept these repository scope changes? [y/N]"},
		{"config and scope", `{"packages": ["jq"]}`, "Workspace config and repository scope changed since last run:",
			"Accept these workspace config and repository scope changes? [y/N]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, buf, _ := brokeredFixture(t)
			o.IsTTYStdin = func() bool { return true }
			o.Stdin = strings.NewReader("y\n")
			if c.name == "config and scope" {
				// A previous approval of an empty config, so the packages line is a change.
				if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), nil); err != nil {
					t.Fatal(err)
				}
			}
			writeWorkspaceConfig(t, o.Workspace, c.wsCfg)
			if !o.checkConfigChanges(gbOn(), "podman") {
				t.Fatalf("a `y` was refused:\n%s", buf.String())
			}
			out := buf.String()
			header := strings.Index(out, c.header)
			block := strings.Index(out, `+ o/r  remote "origin"  added`)
			question := strings.Index(out, c.question)
			if header < 0 || block < 0 || question < 0 {
				t.Fatalf("want the header %q, the block row and the question %q:\n%s", c.header, c.question, out)
			}
			if !(header < block && block < question) {
				t.Fatalf("the header, the scope block and the question are out of order:\n%s", out)
			}
			if c.name == "config and scope" {
				if diff := strings.Index(out, `"jq"`); diff < 0 || diff < block {
					t.Fatalf("the config diff must follow the scope block:\n%s", out)
				}
			} else if strings.Contains(out, "Workspace config changed since last run:") {
				t.Fatalf("a scope-only change used the config-only header:\n%s", out)
			}
			if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 || got[0] != "o/r" {
				t.Fatalf("the `y` did not record the scope part: %v", got)
			}
		})
	}
}

// commonDirWorktree turns the fixture workspace's .git into a worktree pointer whose common
// directory, inside the workspace, has the name the agent chose. config is that common
// directory's git config, "" for none.
func commonDirWorktree(t *testing.T, ws, name, config string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(ws, ".git")); err != nil {
		t.Fatal(err)
	}
	g := filepath.Join(ws, name, "worktrees", "w")
	if err := os.MkdirAll(g, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, body := range map[string]string{
		filepath.Join(g, "gitdir"):    "../../../.git\n",
		filepath.Join(g, "commondir"): "../..\n",
		filepath.Join(ws, ".git"):     "gitdir: " + name + "/worktrees/w\n",
	} {
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(ws, name, "config"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Nothing the agent names reaches the host's terminal as a sequence or as markup: not in the
// refusal's scope block, not in its advice, and not in the launch's warning line.
func TestTheGatePrintsTheAgentsPathsAsText(t *testing.T) {
	// Control characters in the common directory's name: the pointer reads as empty and the
	// output holds no ESC at all (color is off here, so the launcher writes none itself).
	o, buf, _ := brokeredFixture(t)
	commonDirWorktree(t, o.Workspace, "x\x1b[2K\x1b[1A\x1b]0;owned\a\x1b[8m",
		"[remote \"origin\"]\n\turl = git@github.com:victim/secret.git\n")
	o.checkConfigChanges(gbOn(), "podman")
	if out := buf.String(); strings.ContainsAny(out, "\x1b\a") {
		t.Fatalf("a terminal sequence from the workspace reached the output:\n%q", out)
	}

	// Markup in the name: the advice names the git config it was read from as text.
	o, buf, _ = brokeredFixture(t)
	commonDirWorktree(t, o.Workspace, "[bold green]c", "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatal("a first launch with a GitHub remote and no terminal must refuse")
	}
	var advice string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "git config:") {
			advice = line
		}
	}
	if !strings.Contains(advice, "bold green]c") {
		t.Fatalf("the advice's git config line lost the name's markup to the renderer: %q\n%s", advice, buf.String())
	}

	// And the warning line the launch prints for a problem.
	o, buf, _ = brokeredFixture(t)
	commonDirWorktree(t, o.Workspace, "[bold green]d", "")
	o.checkConfigChanges(gbOn(), "podman")
	var warning string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "gb: cannot read") {
			warning = line
		}
	}
	if !strings.Contains(warning, "bold green]d") {
		t.Fatalf("the warning line lost the name's markup to the renderer: %q\n%s", warning, buf.String())
	}
}

// Apple Container starts no broker, so it reads nothing and asks nothing (§5.6).
func TestTheGateAsksNothingWhereNoBrokerStarts(t *testing.T) {
	o, _, _ := brokeredFixture(t)
	if !o.checkConfigChanges(gbOn(), "container") {
		t.Fatal("an Apple Container launch must not be asked about a broker it does not start")
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("a launch that starts no broker gained a scope part")
	}
}

// The whole launch-side chain: the gate reads the remotes and records the approval, the
// spawn writes this launch's scope file and hands its path to the daemon through
// {repository_scope}, and the file goes with the daemon.
func TestAFreshLaunchHandsTheApprovedScopeToTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("approved scope %v", got)
	}

	handles := o.startLoopholes("yolo-brokered-test", "podman", gbOn())
	socketsDir := hostServiceSocketsDir("yolo-brokered-test", false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the daemon was never handed a scope file (%v); output:\n%s", err, buf.String())
	}
	var f brokerscope.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Repos) != 1 || f.Repos[0] != "o/r" || f.Workspace != o.Workspace || f.PID != os.Getpid() ||
		f.Container != "yolo-brokered-test" || f.Source != "gbsrc" {
		t.Fatalf("scope file %+v", f)
	}
	if !strings.Contains(buf.String(), "gb: scope for this workspace: o/r") {
		t.Errorf("the launch did not disclose the scope:\n%s", buf.String())
	}
	// The fixture daemon exits instead of serving, so its start fails — and the scope file
	// written for it goes too, rather than outliving a daemon that never ran.
	entries, _ := os.ReadDir(filepath.Dir(paths.BrokerScopeFile("gbsrc", "x")))
	if len(entries) != 0 {
		t.Fatalf("a scope file outlived its daemon: %v", entries)
	}
}

// readHandedScope reads back the scope file the fixture daemon was handed.
func readHandedScope(t *testing.T, marker string, out *strings.Builder) brokerscope.File {
	t.Helper()
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the daemon was never handed a scope file (%v); output:\n%s", err, out.String())
	}
	var f brokerscope.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// The workspace entry, the launch's half (docs/design/workspace-widening.md §3.2, §3.3): a
// `brokered.<source>.repos` entry in the workspace config is a row of the scope block, approved
// by the gate exactly as a remote is, never a JSON line; the broker's scope file holds the
// approved union in `repos`, with no `widened` list and the names of the two config files; and
// the launch line names each repository's sources.
func TestAWorkspaceEntryIsApprovedAtTheGateAndReachesTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib", "O/R"]}}}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	if got := config.ApprovedScope(o.Workspace, "gbsrc"); strings.Join(got, ",") != "o/r,org/lib" {
		t.Fatalf("the approval record holds %v, want the remote and the entry", got)
	}
	gate := buf.String()
	// The flag path shows the block before it records (WW-D20), and the entry is never JSON.
	for _, want := range []string{`+ o/r      remote "origin", yolo-jail.jsonc  added`,
		"+ org/lib  yolo-jail.jsonc                   added", "gb: 2 added, 0 removed, 0 source changed"} {
		if !strings.Contains(gate, want) {
			t.Errorf("the flag path did not show %q:\n%s", want, gate)
		}
	}
	if strings.Contains(gate, `"brokered"`) || strings.Contains(gate, `"repos"`) {
		t.Errorf("the entry reached the gate as JSON lines:\n%s", gate)
	}

	handles := o.startLoopholes("yolo-brokered-entry", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-entry", false), "", "") })
	f := readHandedScope(t, marker, buf)
	if strings.Join(f.Repos, ",") != "o/r,org/lib" || len(f.Widened) != 0 ||
		f.ConfigFile != config.WorkspaceConfigName || f.LocalFile != config.WorkspaceLocalConfigName {
		t.Fatalf("scope file %+v, want repos [o/r org/lib], no widened, and the two config files", f)
	}
	if want := `gb: scope for this workspace: o/r (remote "origin", yolo-jail.jsonc), org/lib (yolo-jail.jsonc)`; !strings.Contains(buf.String(), want) {
		t.Errorf("the launch line is not %q:\n%s", want, buf.String())
	}
}

// A TOML workspace's scope file names its TOML files: the broker's advice points at the file the
// workspace actually has, never the legacy name it does not.
func TestATOMLWorkspaceScopeFileNamesTheTOMLFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	if err := os.WriteFile(filepath.Join(o.Workspace, paths.WorkspaceConfigTOMLName),
		[]byte("[install]\nnix = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	handles := o.startLoopholes("yolo-brokered-toml", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-toml", false), "", "") })
	f := readHandedScope(t, marker, buf)
	if f.ConfigFile != paths.WorkspaceConfigTOMLName || f.LocalFile != paths.WorkspaceLocalConfigTOMLName {
		t.Fatalf("scope file names %q and %q, want the TOML files", f.ConfigFile, f.LocalFile)
	}
}

// A workspace lists every repository it can reach with a whole-segment wildcard (WW-D31): the
// gate shows what the wildcard admits rather than leaving the human to read the token as one
// repository's name, the scope file carries the token, and the launch line says what it admits.
func TestAWildcardEntryIsApprovedAtTheGateAndReachesTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["*/*"]}}}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	gate := buf.String()
	for _, want := range []string{"*/*", "every repository on github.com"} {
		if !strings.Contains(gate, want) {
			t.Errorf("the gate did not show %q:\n%s", want, gate)
		}
	}

	handles := o.startLoopholes("yolo-brokered-wildcard", "podman", gbOn())
	t.Cleanup(func() {
		o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-wildcard", false), "", "")
	})
	f := readHandedScope(t, marker, buf)
	if strings.Join(f.Repos, ",") != "*/*,o/r" {
		t.Fatalf("scope file repos %v, want the wildcard and the remote", f.Repos)
	}
	if want := `gb: scope for this workspace: */* (every repository on github.com; yolo-jail.jsonc), o/r (remote "origin")`; !strings.Contains(buf.String(), want) {
		t.Errorf("the launch line is not %q:\n%s", want, buf.String())
	}
}

// A source on any host (docs/design/git-broker-plan.md ID-2), through writeScopeFiles' own call
// site: the gate approves the workspace's remote URLs verbatim, and the scope file the daemon is
// handed records them with the remote-url form, which is what makes its broker match a URL
// exactly rather than read it as an OWNER/REPO.
func TestWriteScopeFilesRemoteURLForm(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixtureOn(t, true, "*")
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	handles := o.startLoopholes("yolo-brokered-url", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-url", false), "", "") })
	f := readHandedScope(t, marker, buf)
	if f.Form != brokerscope.FormRemoteURL || strings.Join(f.Repos, ",") != "git@github.com:o/r.git" {
		t.Fatalf("scope file form %q repos %v, want the remote-url form and the remote's URL", f.Form, f.Repos)
	}
	if want := `gb: scope for this workspace: git@github.com:o/r.git (remote "origin")`; !strings.Contains(buf.String(), want) {
		t.Errorf("the launch line is not %q:\n%s", want, buf.String())
	}
}

// The scope file's `grants` hold what this launch's gate approved (docs/design/git-broker-plan.md
// ID-10), through writeScopeFiles' own call site: a grant written into the workspace config after
// the y waits for the next fresh launch, as a repository entry does.
func TestScopeFileCarriesApprovedGrants(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixtureGrants(t)
	writeUserConfigOf(t, grantKindsConfig("gbsrc", "create-branch"))
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "+ grant create-branch github.com/o/r  added") {
		t.Errorf("the gate showed no line for the grant:\n%s", buf.String())
	}
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}, {"kind": "create-branch", "repo": "*"}]}}}`)
	handles := o.startLoopholes("yolo-brokered-grants", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-grants", false), "", "") })
	f := readHandedScope(t, marker, buf)
	want := []brokerscope.Grant{{Kind: "read", Repo: "*"}, {Kind: "create-branch", Repo: "github.com/o/r"}}
	if !reflect.DeepEqual(f.Grants, want) {
		t.Fatalf("scope file grants %v, want the gate's %v alone", f.Grants, want)
	}
	if line := "gb: grants for this workspace: read *, create-branch github.com/o/r"; !strings.Contains(buf.String(), line) {
		t.Errorf("the launch did not disclose the grant as %q:\n%s", line, buf.String())
	}
}

// The gate's own filter (docs/design/git-broker-plan.md ID-10), through the real path: a grant the
// workspace lists at the gate's read joins no scope when the user's config does not allow its
// kind, when the source does not define it, or when it is `read`, which every source defines
// (BB-D107) but the user config neither lists nor holds. The gate shows no line for it and the
// scope file holds no grants.
func TestAGrantTheGateCannotJoinReachesNoScope(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	for _, c := range []struct {
		name, block string
		allowed     []string
		kind        string
	}{
		{"the user config lacks the kind", `{"source": "gbsrc", "remote_host": "*", "grants": ["create-branch"]}`,
			nil, "create-branch"},
		{"the source does not define the kind", `{"source": "gbsrc", "remote_host": "*", "grants": ["create-branch"]}`,
			[]string{"create-branch", "delete-branch"}, "delete-branch"},
		{"the kind is read, not allowed", `{"source": "gbsrc", "remote_host": "*", "grants": ["create-branch"]}`,
			[]string{"create-branch"}, "read"},
	} {
		t.Run(c.name, func(t *testing.T) {
			o, buf, marker := brokeredFixtureBlock(t, true, c.block)
			user := grantKindsConfig("gbsrc", c.allowed...)
			// The user's read grant joins every scope; a workspace `read` grant joins only beside
			// it, so its case is one whose user config holds none: then nothing joins (BB-D110).
			want := []brokerscope.Grant{{Kind: "read", Repo: "*"}}
			if c.kind == "read" {
				user = mapOf(t, `{"brokered": {"gbsrc": {"grant_kinds": ["create-branch"], "grants": []}}}`)
				want = nil
			}
			writeUserConfigOf(t, user)
			writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"grants": [{"kind": "`+c.kind+`", "repo": "github.com/o/r"}]}}}`)
			o.AcceptConfigChanges = true
			if !o.checkConfigChanges(gbOn(), "podman") {
				t.Fatalf("an accepted gate refused:\n%s", buf.String())
			}
			// The one grant line the gate may show is the user's read grant's.
			wantGrantLines := []string{"+ grant read *  added (from your user config, for every workspace)"}
			if want == nil {
				wantGrantLines = nil
			}
			if got := linesContaining(buf.String(), "+ grant"); !reflect.DeepEqual(got, wantGrantLines) {
				t.Errorf("the gate's grant lines are %q, want %q:\n%s", got, wantGrantLines, buf.String())
			}
			cname := "yolo-brokered-nogrant"
			handles := o.startLoopholes(cname, "podman", gbOn())
			t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), "", "") })
			if f := readHandedScope(t, marker, buf); !reflect.DeepEqual(f.Grants, want) {
				t.Fatalf("scope file grants %v, want %v", f.Grants, want)
			}
			wantDisclosed := []string{"gb: grants for this workspace: read *"}
			if want == nil {
				wantDisclosed = nil
			}
			if got := linesContaining(buf.String(), "grants for this workspace:"); !reflect.DeepEqual(got, wantDisclosed) {
				t.Errorf("the launch disclosed %q, want %q:\n%s", got, wantDisclosed, buf.String())
			}
		})
	}
}

// FAIL CLOSED for grants as for repositories: a spawn whose gate recorded no approved scope for a
// loophole hands its broker no grant, even where grants are held for it, so a path that skipped
// the gate cannot start a broker with standing access.
func TestNoApprovedScopeHandsNoGrant(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixtureGrants(t)
	o.approvedGrants = map[string][]brokerscope.Grant{"gb": {{Kind: "create-branch", Repo: "github.com/o/r"}}}
	cname := "yolo-brokered-ungated-grant"
	handles := o.startLoopholes(cname, "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), "", "") })
	if f := readHandedScope(t, marker, buf); len(f.Grants) != 0 {
		t.Fatalf("an ungated spawn handed its broker grants %v", f.Grants)
	}
	if strings.Contains(buf.String(), "grants for this workspace") {
		t.Errorf("an ungated spawn disclosed a grant:\n%s", buf.String())
	}
}

// A non-interactive attach's pending check reads grants as the gate does (LA-D9;
// docs/design/git-broker-plan.md ID-10): a grant the user allows and the workspace names after the
// last approval is a pending change, so the attach prints its notice rather than nothing.
func TestAnUnapprovedGrantIsPendingAtAnAttach(t *testing.T) {
	o, buf, _ := brokeredFixtureGrants(t)
	writeUserConfigOf(t, grantKindsConfig("gbsrc", "create-branch"))
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	if o.configChangesPending("podman", "yolo-pending-grant", gbOn(), nil) {
		t.Fatal("a just-approved workspace reads as pending")
	}
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`)
	if !o.configChangesPending("podman", "yolo-pending-grant", gbOn(), nil) {
		t.Fatal("a grant named after the last approval is not pending at an attach")
	}
}

// WW-P2: the scope file holds what THIS gate approved, held in memory. An edit to the entry
// after the y, and an approval another session records between this gate and the spawn, both
// wait for the next fresh launch.
func TestNothingAfterTheGateReachesTheScopeFile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib", "org/late"]}}}`)
	other := &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "gbsrc", Label: "gb",
		Entry: []config.EntryRepo{{Repo: "org/concurrent", Files: []string{"yolo-jail.jsonc"}}}}}}
	if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), other); err != nil {
		t.Fatal(err)
	}
	handles := o.startLoopholes("yolo-brokered-late", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-late", false), "", "") })
	if f := readHandedScope(t, marker, buf); strings.Join(f.Repos, ",") != "o/r,org/lib" {
		t.Fatalf("scope file repos %v, want the gate's o/r and org/lib alone", f.Repos)
	}
}

// WW-D18: the gate's read is strict and its own. A workspace config made unparseable after the
// launch's first read refuses at the gate, naming the file, at a terminal too, rather than
// prompting over an empty config whose entry reads as empty; nothing is recorded.
func TestAConfigBrokenAfterTheLaunchsReadRefusesAtTheGate(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	if _, ok := o.loadAndValidateConfig(); !ok {
		t.Fatalf("the launch's own read refused a valid config:\n%s", buf.String())
	}
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]`)
	o.IsTTYStdin = func() bool { return true }
	o.Stdin = strings.NewReader("y\n")
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("a broken workspace config passed the gate:\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "Cannot read this workspace's config for the config-change gate") ||
		!strings.Contains(out, "Failed to parse yolo-jail.jsonc") {
		t.Errorf("the refusal does not name the file and the error:\n%s", out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("the gate prompted over a config it could not read:\n%s", out)
	}
	for _, p := range []string{config.ApprovalSnapshotPath(o.Workspace), config.ApprovalScopePath(o.Workspace)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("a refused gate recorded %s", p)
		}
	}
}

// WW-D22: a committed entry for a source no selected pack brokers says nothing at a launch,
// which would print it at every launch of every contributor without the pack.
func TestALaunchSaysNothingOfAnEntryNoSelectedPackBrokers(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"github": {"repos": ["org/lib"]}}}`)
	if _, ok := o.loadAndValidateConfig(); !ok {
		t.Fatalf("the launch refused:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "brokered") {
		t.Errorf("the launch printed a brokered line:\n%s", buf.String())
	}
}

// WW-D21: the old form's refusal at a launch names this project's edit and counts the others,
// so the launch log its jail reads names no other workspace.
func TestTheOldFormsLaunchRefusalNamesNoOtherWorkspace(t *testing.T) {
	// A HOST LAUNCH: in a jail the user config is the host-generated snapshot, and a retired key
	// there only warns (remove it from the host config), so a run inside the dev jail passed the
	// launch and failed this test while CI, which runs outside a jail, stayed green.
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	o, buf, _ := brokeredFixture(t)
	cfg := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	ws, _ := json.Marshal(o.Workspace)
	body := `{"brokered": {"gbsrc": {"workspaces": {` + string(ws) + `: {"repos": ["org/lib"]},
	  "/home/someone/secret-client": {"repos": ["acme/private-roadmap"]}}}}}`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := o.loadAndValidateConfig(); ok {
		t.Fatalf("a launch passed with the retired form:\n%s", buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, `"brokered": {"gbsrc": {"repos": ["org/lib"]}}`) ||
		!strings.Contains(out, "1 other project has an entry under this key") {
		t.Errorf("the refusal lacks this project's edit or the count:\n%s", out)
	}
	for _, leak := range []string{"secret-client", "acme/private-roadmap"} {
		if strings.Contains(out, leak) {
			t.Errorf("the launch output names another workspace's %q:\n%s", leak, out)
		}
	}
}

// A launch whose gate recorded no scope hands the daemon an EMPTY one and says so: neither the
// remotes nor the workspace's entry, which reaches a broker only through the gate (WW-D12).
func TestASpawnWithNoApprovedScopeFailsClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	handles := o.startLoopholes("yolo-brokered-test2", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-test2", false), "", "") })
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("no scope file handed: %v\n%s", err, buf.String())
	}
	var f brokerscope.File
	_ = json.Unmarshal(data, &f)
	if len(f.Repos) != 0 || !strings.Contains(buf.String(), "no repository scope was approved") {
		t.Fatalf("scope %+v, output:\n%s", f, buf.String())
	}
}

// The spawn writes a scope file for exactly the brokers the gate asked about. A pack
// loophole whose origin gate is closed starts no daemon, so the gate asks nothing and the
// spawn writes no file and prints no scope line: a file written for a daemon that never
// runs has no handle to remove it, and a line about a scope nobody approved is noise.
func TestTheSpawnWritesNoScopeFileForABrokerTheOriginGateStops(t *testing.T) {
	o, buf, marker := brokeredFixtureWith(t, false)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("the gate refused:\n%s", buf.String())
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("the gate recorded a scope for a broker the origin gate stops")
	}
	handles := o.startLoopholes("yolo-brokered-test3", "podman", gbOn())
	socketsDir := hostServiceSocketsDir("yolo-brokered-test3", false)
	o.stopLoopholes(handles, socketsDir, "", "")
	_ = os.RemoveAll(socketsDir)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the daemon of an unapproved pack ran")
	}
	entries, _ := os.ReadDir(filepath.Dir(paths.BrokerScopeFile("gbsrc", "x")))
	if len(entries) != 0 {
		t.Fatalf("a scope file was written for a broker that never started: %v", entries)
	}
	for _, line := range []string{"no repository scope was approved", "repository scope is empty", "scope for this workspace"} {
		if strings.Contains(buf.String(), line) {
			t.Errorf("the launch printed %q for a broker that never started:\n%s", line, buf.String())
		}
	}
}

// {repository_scope} with no file written is a refusal to start, never a literal token.
func TestTheScopeTokenWithNoFileRefusesTheSpawn(t *testing.T) {
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"/bin/true", "--scope-file", "{repository_scope}"})
	if _, ok := o.resolveDaemonArgv("gb", spec, "/tmp/x.sock"); ok {
		t.Fatal("a daemon named its scope file and started without one")
	}
	o.scopeFiles = map[string]string{"gb": "/s/f.json"}
	argv, ok := o.resolveDaemonArgv("gb", spec, "/tmp/x.sock")
	if !ok || argv[len(argv)-1] != "/s/f.json" {
		t.Fatalf("argv %v", argv)
	}
}

// An `N` to a changed workspace entry names the file the change is in, beside the step that
// launches the project without the broker (docs/design/workspace-widening.md §3.2), and records
// nothing. Driven through the gate's real prompter.
func TestADeclinedEntryNamesTheFileItIsIn(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	writeWorkspaceConfig(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	o.IsTTYStdin = func() bool { return true }
	o.Stdin = strings.NewReader("n\n")
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an `n` was accepted:\n%s", buf.String())
	}
	out := buf.String()
	for _, want := range []string{"+ org/lib  yolo-jail.jsonc",
		"The `brokered.gbsrc.repos` change is in yolo-jail.jsonc: undo it there, or answer y at the next launch",
		"gb: 2 added, 0 removed, 0 source changed",
		"take the pack that ships it out of `packs`"} {
		if !strings.Contains(out, want) {
			t.Errorf("the decline does not say %q:\n%s", want, out)
		}
	}
	// The count line sits directly above the question.
	if i, j := strings.Index(out, "gb: 2 added"), strings.Index(out, "[y/N]"); i < 0 || j < 0 || i > j {
		t.Errorf("the count line is not above the question:\n%s", out)
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("a declined gate recorded the scope part")
	}
}

// evilInclude is an include file name the agent chose, holding terminal sequences and markup.
const evilInclude = "x\x1b[2K\x1b]0;owned\a[bold]y.jsonc"

// writeEvilInclude makes the workspace config include evilInclude, which holds body.
func writeEvilInclude(t *testing.T, ws, body string) {
	t.Helper()
	writeWorkspaceConfig(t, ws, `{"include_if_found": ["x\u001b[2K\u001b]0;owned\u0007[bold]y.jsonc"]}`)
	if err := os.WriteFile(filepath.Join(ws, evilInclude), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// asText reports whether out names evilInclude as text: no terminal sequence reached it, and the
// renderer left the name's markup in place.
func asText(out string) bool { return namesAsText(out, "bold]y.jsonc") }

// namesAsText reports whether out holds no terminal sequence and holds want, the tail of an
// agent-chosen name whose markup the renderer would have consumed.
func namesAsText(out, want string) bool {
	return !strings.ContainsAny(out, "\x1b\a") && strings.Contains(out, want)
}

// WW-P3: the launch's own config messages print what the agent chose as text, written in an
// include the agent named: the include's parse error, which names it; a warning and an error that
// echo a value and a key; and a `brokered` key written twice there (WW-D24).
func TestTheLaunchsConfigMessagesPrintTheAgentsTextAsText(t *testing.T) {
	for name, c := range map[string]struct {
		body, want string
		ok         bool
	}{
		"a parse error": {`{"packages": `, "bold]y.jsonc", false},
		"a warning":     {`{"devices": ["/dev/x\u001b]0;owned\u0007[bold]z"]}`, "bold]z", true},
		"an error":      {`{"x\u001b]0;owned\u0007[bold]k": 1}`, "bold]k: unknown key", false},
		"a key written twice": {`{"brokered": {"gbsrc": {"repos": ["a/a"]}}, "brokered": {"gbsrc": {"repos": ["b/b"]}}}`,
			"bold]y.jsonc: config.brokered is written 2 times", false},
	} {
		o, buf, _ := brokeredFixture(t)
		writeEvilInclude(t, o.Workspace, c.body)
		if _, ok := o.loadAndValidateConfig(); ok != c.ok {
			t.Errorf("%s: the launch's config gate returned ok=%v, want %v:\n%s", name, ok, c.ok, buf.String())
		}
		if !namesAsText(buf.String(), c.want) {
			t.Errorf("%s: the agent's text did not reach the output as text (want %q):\n%q", name, c.want, buf.String())
		}
	}
}

// WW-D19: the decline line names the entry's agent-chosen file as text.
func TestTheDeclineNamesAnAgentChosenFileAsText(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	writeEvilInclude(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	o.IsTTYStdin = func() bool { return true }
	o.Stdin = strings.NewReader("n\n")
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an `n` was accepted:\n%s", buf.String())
	}
	out := buf.String()
	if strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, `change is in x\x1b[2K`) {
		t.Fatalf("the decline does not name the file as text:\n%q", out)
	}
}

// WW-D19: the launch line names each repository's agent-chosen file as text, markup included.
func TestTheLaunchLineNamesAnAgentChosenFileAsText(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, _ := brokeredFixture(t)
	writeEvilInclude(t, o.Workspace, `{"brokered": {"gbsrc": {"repos": ["org/lib"]}}}`)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	handles := o.startLoopholes("yolo-escaped-line", "podman", gbOn())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-escaped-line", false), "", "") })
	var line string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "scope for this workspace") {
			line = l
		}
	}
	if !asText(line) {
		t.Fatalf("the launch line lost or rendered the file's name: %q\n%s", line, buf.String())
	}
}

// WW-D18, WW-D19: the gate's read refusal names an agent-chosen include as text.
func TestTheGateReadRefusalNamesAnAgentChosenFileAsText(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	writeEvilInclude(t, o.Workspace, `{"brokered": `)
	if o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("a broken include passed the gate:\n%s", buf.String())
	}
	if !asText(buf.String()) {
		t.Fatalf("the refusal lost or rendered the file's name:\n%q", buf.String())
	}
}

// BB-D23: a launch that starts a brokered loophole discloses the user-scope permission sets the
// source runs under, read from the user config alone. Deleting the disclosure call site leaves a
// widened grant invisible at the launch that starts it.
func TestTheLaunchDisclosesTheUserScopesBrokeredSets(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, _ := brokeredFixture(t)
	userCfg := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userCfg, []byte(`{"brokered": {"gbsrc": {"sets": [
	  {"name": "read-only", "mode": "standing", "admit": ["pr comment"]},
	  {"name": "review", "mode": "windowed", "window": "1h"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(gbOn(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	handles := o.startLoopholes("yolo-brokered-sets", "podman", gbOn())
	t.Cleanup(func() {
		o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-sets", false), "", "")
	})
	if want := "gb: sets from user config: read-only (+ pr comment), review 1h"; !strings.Contains(buf.String(), want) {
		t.Errorf("the launch does not disclose %q:\n%s", want, buf.String())
	}
}

// SELECTING THE PACK IS "BASICALLY A NO-OP OPTION" (docs/design/boundary-broker.md BB-D110). A
// broker source that holds no read grant now, and held none at its last approval, is out of the
// gate: no prompt at a terminal, no refusal without one, and nothing recorded. The user's read
// grant then puts it to the gate with the remotes it reaches, both on the block; withdrawing the
// grant shows the scope and the grant removed, once, and an approval clears them, after which the
// source is out of the gate again.
func TestABrokerWithNoReadGrantStaysOutOfTheGate(t *testing.T) {
	noRead := `{"brokered": {"gbsrc": {"grants": []}}}`
	t.Run("at a terminal", func(t *testing.T) {
		o, buf, _ := brokeredFixture(t)
		writeUserConfigOf(t, mapOf(t, noRead))
		o.IsTTYStdin = func() bool { return true }
		o.Stdin = strings.NewReader("n\n")
		if !o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("a broker with no read grant was put to the gate, and the `n` refused the launch:\n%s", buf.String())
		}
		if strings.Contains(buf.String(), "repository scope") || strings.Contains(buf.String(), "[y/N]") {
			t.Errorf("a broker with no read grant prompted:\n%s", buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 0 {
			t.Errorf("a broker with no read grant had its scope recorded: %v", got)
		}
	})
	t.Run("with no terminal", func(t *testing.T) {
		o, buf, _ := brokeredFixture(t)
		writeUserConfigOf(t, mapOf(t, noRead))
		if !o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("a launch with no terminal was refused over a broker with no read grant:\n%s", buf.String())
		}
		if strings.Contains(buf.String(), "--accept-config-changes") {
			t.Errorf("the launch named the refusal's flag:\n%s", buf.String())
		}
	})
	// A record from before brokers granted nothing: remotes approved under the old per-project
	// switch, and no grant. The source is still out of the gate: its record is kept as it is,
	// and nothing about it is asked.
	t.Run("an old record of remotes and no grant", func(t *testing.T) {
		o, buf, _ := brokeredFixture(t)
		writeUserConfigOf(t, mapOf(t, noRead))
		old := &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "gbsrc", Label: "gb", RemoteHost: "github.com",
			Read: brokerscope.Read{Remotes: []brokerscope.Remote{{Name: "origin", Repo: "o/r"}}}}}}
		if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), old); err != nil {
			t.Fatal(err)
		}
		if !o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("a launch with no terminal was refused over an old record and no read grant:\n%s", buf.String())
		}
		if strings.Contains(buf.String(), "repository scope") {
			t.Errorf("a broker with no read grant was put to the gate:\n%s", buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 {
			t.Errorf("the old record became %v, want it kept", got)
		}
		if repos, grants := o.approvedScopes["gb"], o.approvedGrants["gb"]; len(repos) != 0 || len(grants) != 0 {
			t.Fatalf("inert historical rows reached current broker authority: repos=%v grants=%v", repos, grants)
		}
	})
	// The same old record, then the user's read grant: the record was approved while the broker
	// could do nothing, so the gate compares against no record and the remote shows as added
	// beside the grant, not as unchanged.
	t.Run("an old record of remotes, then a first read grant", func(t *testing.T) {
		o, buf, _ := brokeredFixture(t)
		old := &config.ScopeCheck{Sources: []config.ScopeSource{{Source: "gbsrc", Label: "gb", RemoteHost: "github.com",
			Read: brokerscope.Read{Remotes: []brokerscope.Remote{{Name: "origin", Repo: "o/r"}}}}}}
		if err := config.RecordApproval(o.Workspace, jsonx.NewOrderedMap(), old); err != nil {
			t.Fatal(err)
		}
		if o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("a first read grant reached the broker unseen:\n%s", buf.String())
		}
		for _, want := range []string{`+ o/r  remote "origin"  added`,
			"+ grant read *  added (from your user config, for every workspace)"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the gate does not show %q:\n%s", want, buf.String())
			}
		}
	})
	t.Run("granted, then withdrawn", func(t *testing.T) {
		o, buf, _ := brokeredFixture(t)
		// Granted: the remote and the grant are shown together, and a launch with no terminal
		// refuses until they are approved.
		if o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("the read grant reached the broker unseen:\n%s", buf.String())
		}
		for _, want := range []string{`+ o/r  remote "origin"  added`,
			"+ grant read *  added (from your user config, for every workspace)"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the gate does not show %q:\n%s", want, buf.String())
			}
		}
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("an accepted gate refused:\n%s", buf.String())
		}
		if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 {
			t.Fatalf("the approved scope is %v, want the remote", got)
		}
		// Withdrawn: shown once, removed, and the approval clears the record.
		writeUserConfigOf(t, mapOf(t, noRead))
		buf.Reset()
		o.AcceptConfigChanges = false
		if o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("a withdrawn grant was dropped unseen:\n%s", buf.String())
		}
		for _, want := range []string{"- o/r", "- grant read *"} {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the withdrawal does not show %q:\n%s", want, buf.String())
			}
		}
		o.AcceptConfigChanges = true
		if !o.checkConfigChanges(gbOn(), "podman") {
			t.Fatalf("an accepted withdrawal refused:\n%s", buf.String())
		}
		if got, grants := config.ApprovedScope(o.Workspace, "gbsrc"), config.ApprovedGrants(o.Workspace, "gbsrc"); len(got) != 0 || len(grants) != 0 {
			t.Fatalf("after the withdrawal the record holds %v and %v, want neither", got, grants)
		}
		// Out of the gate again.
		buf.Reset()
		o.AcceptConfigChanges = false
		if !o.checkConfigChanges(gbOn(), "podman") || strings.Contains(buf.String(), "repository scope") {
			t.Fatalf("a source whose withdrawal was approved is still at the gate:\n%s", buf.String())
		}
	})
}

// linesContaining is each line of out containing sub, trimmed of surrounding space.
func linesContaining(out, sub string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			got = append(got, strings.TrimSpace(l))
		}
	}
	return got
}
