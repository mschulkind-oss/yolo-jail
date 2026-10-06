package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`
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
	buf = &strings.Builder{}
	o = &Options{}
	fillDefaults(o)
	o.Workspace, o.Stdout, o.Stderr = ws, buf, buf
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsMacOS = false
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
// project without the broker; a declined config change names the file it is in and that the next
// launch asks again; both, when both changed. Driven through the gate's real prompter, as a
// terminal answers it.
func TestADeclineNamesTheNextStep(t *testing.T) {
	for _, c := range []struct {
		name          string
		configChanged bool
		merged        *jsonx.OrderedMap
		want, not     []string
	}{
		{"scope only", false, gbOn(),
			[]string{"Repository scope changes rejected; nothing was recorded. Exiting.",
				"To launch this project without gb, run `yolo loopholes disable gb --workspace <ws>`"},
			[]string{"The workspace config change is in", " here"}},
		{"config and scope", true, gbOn(),
			[]string{"Workspace config and repository scope changes rejected; nothing was recorded. Exiting.",
				"The workspace config change is in ", "yolo-jail.jsonc: undo it there, or answer y at the next launch, which asks again.",
				"run `yolo loopholes disable gb --workspace <ws>`"},
			[]string{" here"}},
		{"config only", true, jsonx.NewOrderedMap(),
			[]string{"Config changes rejected; nothing was recorded. Exiting.",
				"yolo-jail.jsonc: undo it there, or answer y at the next launch, which asks again."},
			[]string{"yolo loopholes disable", "repository scope changes"}},
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
		"run `yolo loopholes disable gb --workspace"} {
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
