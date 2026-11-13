package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokered_test.go covers the `brokered` key (docs/design/workspace-widening.md §3.1, §3.6): a
// workspace's own `brokered.<source>.repos`, validated file by file through ValidateConfig, the
// call site a launch and `yolo check` reach; the user-scope refusals; the retired form's message;
// and the gate's one strict read (gateread.go). The gate's comparison is scopeapproval_test.go's,
// and the launch's half is internal/cli/run's.

// brokeredHost is a host whose user config and one workspace a test writes.
type brokeredHost struct{ userCfg, ws string }

func newBrokeredHost(t *testing.T) brokeredHost {
	t.Helper()
	userCfg := hostFloorHome(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(root, "app")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return brokeredHost{userCfg: userCfg, ws: ws}
}

// validate loads the config as the launch does, strictly, and validates it.
func (h brokeredHost) validate(t *testing.T) (errs, warns string) {
	t.Helper()
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	e, w := ValidateConfig(cfg, h.ws, nil)
	return strings.Join(e, "\n"), strings.Join(w, "\n")
}

func brokeredLines(all string) string {
	var out []string
	for _, l := range strings.Split(all, "\n") {
		if strings.Contains(l, "brokered") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// A workspace's own entry is accepted, from either file; each malformed shape is refused at the
// file and line it was written at; a workspace `sets` is refused as user scope only (BB-P9).
func TestAWorkspaceEntryIsAcceptedAndEachBadShapeIsRefusedWhereItWasWritten(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"repos": ["org/lib", "a.b/c_d-e", "*/*", "org/*", "*/lib"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceLocalConfigName), `{"brokered": {"github": {"repos": ["private/one"]}}}`)
	if errs, _ := h.validate(t); brokeredLines(errs) != "" {
		t.Fatalf("a workspace entry was refused:\n%s", errs)
	}

	for body, want := range map[string]string{
		`{"brokered": []}`:                                                "config.brokered: expected an object of source name",
		`{"brokered": {"github": "x"}}`:                                   `config.brokered.github: expected an object holding "repos"`,
		`{"brokered": {"github": {"sets": []}}}`:                          "config.brokered.github.sets: user scope only",
		`{"brokered": {"github": {"repos": "org/lib"}}}`:                  "config.brokered.github.repos: expected a list of OWNER/REPO strings or wildcard entries",
		`{"brokered": {"github": {"repos": ["github.com/o/r"]}}}`:         "config.brokered.github.repos[0]: 'github.com/o/r' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["ok/one", 3]}}}`:              "config.brokered.github.repos[1]: 3 is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["one"]}}}`:                    "'one' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["a/b/c"]}}}`:                  "'a/b/c' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["https://github.com/o/r"]}}}`: "is not a repository scope entry",
		// A wildcard is the whole segment alone: a partial glob is not one.
		`{"brokered": {"github": {"repos": ["org*/*"]}}}`: "'org*/*' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["org/l*"]}}}`: "'org/l*' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["*"]}}}`:      "'*' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["*/*/*"]}}}`:  "'*/*/*' is not a repository scope entry",
		`{"brokered": {"github": {"repos": ["*/* "]}}}`:   "is not a repository scope entry",
	} {
		write(t, filepath.Join(h.ws, WorkspaceLocalConfigName), "{}")
		write(t, filepath.Join(h.ws, WorkspaceConfigName), body)
		errs, _ := h.validate(t)
		if !strings.Contains(errs, want) {
			t.Errorf("%s: errors lack %q:\n%s", body, want, errs)
		}
		// Located: the message leads with the file it was written in.
		if !strings.Contains(errs, filepath.Join(h.ws, WorkspaceConfigName)+":1:") {
			t.Errorf("%s: the refusal is not located at its file and line:\n%s", body, errs)
		}
	}
	for _, ok := range []string{`{"brokered": null}`, `{"brokered": {"github": null}}`,
		`{"brokered": {"github": {"repos": null}}}`, `{"brokered": {"github": {"repos": []}}}`} {
		write(t, filepath.Join(h.ws, WorkspaceConfigName), ok)
		if errs, _ := h.validate(t); brokeredLines(errs) != "" {
			t.Errorf("%s refused:\n%s", ok, errs)
		}
	}
}

// WW-D17: a `brokered` key is honored only from files whose bytes came from inside the
// workspace. An include out of it and a link out of it, absolute or relative, are config errors
// naming the file and what reached it; a link that stays inside is accepted, as is any other key
// the outside file sets.
func TestABrokeredKeyFromAFileOutsideTheWorkspaceIsRefused(t *testing.T) {
	h := newBrokeredHost(t)
	other := filepath.Join(filepath.Dir(h.ws), "other")
	write(t, filepath.Join(other, "yolo-jail.local.jsonc"), `{"brokered": {"github": {"repos": ["acme/private"]}}, "packages": ["jq"]}`)

	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"include_if_found": ["../other/yolo-jail.local.jsonc"]}`)
	errs, _ := h.validate(t)
	for _, want := range []string{"config.brokered: written in a file outside this workspace",
		"include_if_found[0] in yolo-jail.jsonc", filepath.Join(other, "yolo-jail.local.jsonc")} {
		if !strings.Contains(errs, want) {
			t.Errorf("an include out of the workspace: errors lack %q:\n%s", want, errs)
		}
	}
	if _, err := ReadWorkspaceForGate(h.ws); err == nil ||
		!strings.Contains(err.Error(), "outside this workspace") {
		t.Errorf("the gate's read took an entry from outside: %v", err)
	}

	// The local file a link out, relative and absolute.
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{}`)
	for _, target := range []string{"../other/yolo-jail.local.jsonc", filepath.Join(other, "yolo-jail.local.jsonc")} {
		link := filepath.Join(h.ws, WorkspaceLocalConfigName)
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		errs, _ := h.validate(t)
		if !strings.Contains(errs, "a link that leads out of this workspace") {
			t.Errorf("a local file linked to %s: errors lack the refusal:\n%s", target, errs)
		}
	}

	// A link that stays inside is the workspace's own file.
	write(t, filepath.Join(h.ws, "conf", "local.jsonc"), `{"brokered": {"github": {"repos": ["org/lib"]}}}`)
	link := filepath.Join(h.ws, WorkspaceLocalConfigName)
	_ = os.Remove(link)
	if err := os.Symlink("conf/local.jsonc", link); err != nil {
		t.Fatal(err)
	}
	if errs, _ := h.validate(t); brokeredLines(errs) != "" {
		t.Errorf("a link inside the workspace was refused:\n%s", errs)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Entry("github"); len(got) != 1 || got[0].Repo != "org/lib" ||
		strings.Join(got[0].Files, ",") != WorkspaceLocalConfigName {
		t.Errorf("entry %+v, want org/lib from %s", got, WorkspaceLocalConfigName)
	}
}

// The open decides, never a path checked before it: a file named inside the workspace whose
// open leaves it reads as outside, and one whose link stays inside reads as contained, with the
// name it was read under.
func TestContainmentIsDecidedOnTheOpenThatReadsTheBytes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws, outside := filepath.Join(root, "ws"), filepath.Join(root, "outside.jsonc")
	write(t, filepath.Join(ws, "real.jsonc"), `{"in": true}`)
	write(t, outside, `{"out": true}`)
	for name, target := range map[string]string{"up.jsonc": "../outside.jsonc", "abs.jsonc": outside,
		"in.jsonc": "real.jsonc"} {
		if err := os.Symlink(target, filepath.Join(ws, name)); err != nil {
			t.Fatal(err)
		}
	}
	w := openWorkspaceRoot(ws)
	defer w.close()
	for name, wantRel := range map[string]string{"up.jsonc": "", "abs.jsonc": "", "in.jsonc": "in.jsonc",
		"real.jsonc": "real.jsonc"} {
		data, rel, err := w.read(filepath.Join(ws, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rel != wantRel {
			t.Errorf("%s read as %q, want %q", name, rel, wantRel)
		}
		if len(data) == 0 {
			t.Errorf("%s: no bytes: an outside file still reads as it always has", name)
		}
	}
	if _, rel, _ := w.read(outside); rel != "" {
		t.Errorf("a file outside read as contained: %q", rel)
	}
}

// WW-D24: a `brokered`, source or `repos` key written twice in one file is refused, naming the
// file and the key, since the decoder keeps the last one silently.
func TestABrokeredKeyWrittenTwiceInOneFileIsRefused(t *testing.T) {
	h := newBrokeredHost(t)
	for body, want := range map[string]string{
		`{"brokered": {"github": {"repos": ["org/a"]}}, "brokered": {"github": {"repos": ["org/b"]}}}`: "config.brokered is written 2 times",
		`{"brokered": {"github": {"repos": ["org/a"]}, "github": {"repos": ["org/b"]}}}`:               "config.brokered.github is written 2 times",
		`{"brokered": {"github": {"repos": ["org/a"], "repos": ["org/b"]}}}`:                           "config.brokered.github.repos is written 2 times",
	} {
		write(t, filepath.Join(h.ws, WorkspaceConfigName), body)
		errs, _ := h.validate(t)
		if !strings.Contains(errs, want) || !strings.Contains(errs, filepath.Join(h.ws, WorkspaceConfigName)) ||
			!strings.Contains(errs, "Merge them into one") {
			t.Errorf("%s: errors lack %q naming the file:\n%s", body, want, errs)
		}
	}
}

// WW-P3: both `brokered` refusals name the files the agent chose as text, a newline in a name
// included, since a printer keeps the newlines yolo writes: the file outside and the include that
// reached it, and the file a key is written twice in.
func TestABrokeredRefusalNamesAnAgentChosenFileAsText(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"include_if_found": ["a\nb.jsonc", "t\nw.jsonc"]}`)
	write(t, filepath.Join(h.ws, "a\nb.jsonc"), `{"include_if_found": ["../o\nut.jsonc"]}`)
	write(t, filepath.Join(filepath.Dir(h.ws), "o\nut.jsonc"), `{"brokered": {"github": {"repos": ["acme/private"]}}}`)
	write(t, filepath.Join(h.ws, "t\nw.jsonc"),
		`{"brokered": {"github": {"repos": ["org/a"]}}, "brokered": {"github": {"repos": ["org/b"]}}}`)
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, h.ws, nil)
	found := map[string]bool{}
	for _, e := range errs {
		for _, want := range []string{"written in a file outside this workspace", "is written 2 times"} {
			if strings.Contains(e, want) {
				found[want] = true
				if strings.ContainsAny(e, "\n\x1b") {
					t.Errorf("a file name reached the refusal unescaped: %q", e)
				}
			}
		}
	}
	all := strings.Join(errs, "\n")
	for _, want := range []string{`o\nut.jsonc:1:`, `a\nb.jsonc, which leads out`,
		`t\nw.jsonc: config.brokered is written 2 times`} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusals do not name %q as text:\n%s", want, all)
		}
	}
	if len(found) != 2 {
		t.Errorf("both refusals did not fire: %v\n%s", found, all)
	}
}

// WW-P3: the loader's own refusals of an include, that it cannot be parsed and that it holds no
// object, name the file the agent chose as text, a newline in its name included.
func TestALoadRefusalNamesAnAgentChosenFileAsText(t *testing.T) {
	h := newBrokeredHost(t)
	for body, want := range map[string]string{`{"packages": `: "Failed to parse ", `[]`: "must contain a top-level JSON object"} {
		write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"include_if_found": ["p\nq.jsonc"]}`)
		write(t, filepath.Join(h.ws, "p\nq.jsonc"), body)
		_, err := LoadConfig(h.ws, true, func(string) {})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), `p\nq.jsonc`) ||
			strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: the refusal does not name the file as text: %q", body, err)
		}
	}
}

// WW-D9: a user-scope `repos` list is refused, naming the workspace files instead, at the user
// file's line; a jail, whose user scope is the host's, warns with the snapshot suffix.
func TestAUserScopeReposListIsRefusedNamingTheWorkspaceFiles(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"repos": ["org/lib"]}}}`)
	errs, _ := h.validate(t)
	for _, want := range []string{"config.brokered.github.repos: not in the user config",
		"would widen every workspace's repository scope", WorkspaceConfigName, WorkspaceLocalConfigName,
		paths.UserConfigPath()} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors lack %q:\n%s", want, errs)
		}
	}
	if !strings.Contains(errs, "config.jsonc:1:") {
		t.Errorf("the refusal is not located at the user file's line:\n%s", errs)
	}
	write(t, h.userCfg, `{"brokered": {"github": {"sets": {}}}}`)
	if errs, _ := h.validate(t); !strings.Contains(errs, "config.brokered.github.sets: expected a list of sets") {
		t.Errorf("a user-scope sets of the wrong shape is not refused as a list:\n%s", errs)
	}
	write(t, h.userCfg, `{"brokered": {"tasks": {"roots": ["team:alpha"]}}}`)
	if errs, _ := h.validate(t); !strings.Contains(errs, "config.brokered.tasks.roots: workspace scope only") ||
		!strings.Contains(errs, WorkspaceConfigName) {
		t.Errorf("declared roots at user scope were not refused with workspace-only guidance:\n%s", errs)
	}
}

// WW-D21: the old form is refused at every scope. The message gives the exact edit, into the
// local file, for the project being validated alone, and counts the others: it names no other
// project's path or repository, since a launch tees it into a log its jail reads.
// RetiredBrokeredMoves, host `yolo check`'s own section, lists every project.
func TestTheRetiredFormNamesThisProjectsEditAndOnlyCountsTheRest(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"workspaces": {
	  "`+h.ws+`": {"repos": ["org/lib", "Org/Lib", "x/y/z", "org/docs"]},
	  "~/code/secret-client": {"repos": ["acme/private-roadmap"]},
	  "relative/elsewhere": {"repos": ["rel/repo"]}
	}}}}`)
	errs, _ := h.validate(t)
	for _, want := range []string{"config.brokered.github.workspaces: RETIRED",
		`"brokered": {"github": {"repos": ["org/docs", "org/lib"]}}`,
		"in " + WorkspaceLocalConfigName + ", which yolo does not git-ignore",
		"2 other projects have entries under this key; host `yolo check` prints the edit for each",
		"remove the `workspaces` key"} {
		if !strings.Contains(errs, want) {
			t.Errorf("errors lack %q:\n%s", want, errs)
		}
	}
	for _, leak := range []string{"secret-client", "acme/private-roadmap", "relative/elsewhere", "rel/repo"} {
		if strings.Contains(errs, leak) {
			t.Errorf("the validator named another project's %q:\n%s", leak, errs)
		}
	}
	if strings.Count(errs, "RETIRED") != 1 || strings.Contains(errs, "expected an object") {
		t.Errorf("the retired key has more than its own message:\n%s", errs)
	}

	moves := RetiredBrokeredMoves()
	var edits []string
	for _, m := range moves {
		edits = append(edits, m.Edit())
	}
	all := strings.Join(edits, "\n")
	for _, want := range []string{h.ws + `: put "brokered": {"github": {"repos": ["org/docs", "org/lib"]}} in ` +
		filepath.Join(h.ws, WorkspaceLocalConfigName), "~/code/secret-client: put", "acme/private-roadmap",
		"relative/elsewhere: it is relative"} {
		if !strings.Contains(all, want) {
			t.Errorf("the check's list lacks %q:\n%s", want, all)
		}
	}

	// In a jail the config is the host-written snapshot: a warning, naming no edit.
	t.Setenv("YOLO_VERSION", "test")
	cfg := decode(t, `{"brokered": {"github": {"workspaces": {"/host/ws": {"repos": ["org/lib"]}}}}}`)
	e, w := ValidateConfig(cfg, h.ws, nil)
	if brokeredLines(strings.Join(e, "\n")) != "" || !strings.Contains(strings.Join(w, "\n"), "RETIRED") ||
		!strings.Contains(strings.Join(w, "\n"), "remove the key from the HOST config") ||
		strings.Contains(strings.Join(w, "\n"), "/host/ws") {
		t.Errorf("in a jail: errors %q warnings %q", e, w)
	}
}

// WW-D22: a source no selected pack brokers is host `yolo check`'s warning, naming the next
// step, and the shared validator a launch runs says nothing about it.
func TestAnUnbrokeredSourceIsTheChecksWarningAndNeverTheLaunchs(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"repos": ["org/lib"]}, "githb": {"repos": []}}}`)
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	_, warns := ValidateConfig(cfg, h.ws, brokeredResolver)
	if got := brokeredLines(strings.Join(warns, "\n")); got != "" {
		t.Fatalf("the shared validator warned about a source, which a launch would print at every launch:\n%s", got)
	}
	joined := strings.Join(UnbrokeredSourceWarnings(cfg, brokeredResolver), "\n")
	if !strings.Contains(joined, "config.brokered.githb: no loophole a selected pack ships brokers the source "+
		"'githb', so this entry does nothing here (selected sources here: github)") ||
		!strings.Contains(joined, "Check the spelling") {
		t.Errorf("no warning, with its next step, for the unbrokered source:\n%s", joined)
	}
	if strings.Contains(joined, "config.brokered.github:") {
		t.Errorf("a warning for a brokered source:\n%s", joined)
	}
	if joined := strings.Join(UnbrokeredSourceWarnings(cfg, fenceResolver{}), "\n"); !strings.Contains(joined,
		"config.brokered.github: no loophole") || !strings.Contains(joined, "(selected sources here: none)") {
		t.Errorf("with no brokered loophole selected, every source is inert:\n%s", joined)
	}
}

// WW-D22 recognizes both declarations that own a configured source while retaining warnings
// for names neither selected declaration owns.
func TestUnbrokeredSourceWarningsRecognizesSelectedCallBrokerSources(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	merged := decode(t, `{"brokered":{"legacy":{},"local-tasks":{},"unknown":{}}}`)
	resolver := fenceResolver{known: map[string]LoopholeInfo{
		"legacy":      {Name: "legacy", Brokered: &loopholedecl.Brokered{Source: "legacy"}},
		"call-broker": {Name: "call-broker", CallBroker: &loopholedecl.CallBroker{Source: "local-tasks"}},
	}}
	joined := strings.Join(UnbrokeredSourceWarnings(merged, resolver), "\n")
	for _, expected := range []string{
		"config.brokered.unknown: no loophole a selected pack ships brokers the source 'unknown'",
		"(selected sources here: legacy, local-tasks)",
		"a selected loophole manifest's `brokered.source` or `call_broker.source`",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("warning %q missing from %q", expected, joined)
		}
	}
	for _, source := range []string{"legacy", "local-tasks"} {
		if strings.Contains(joined, "config.brokered."+source+":") {
			t.Errorf("selected source %q was reported inert: %s", source, joined)
		}
	}
}

// gitResolver declares a source on any host, as the git pack's manifest does.
var gitResolver = fenceResolver{known: map[string]LoopholeInfo{
	"git-broker": {Name: "git-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "git", RemoteHost: "*"}},
}}

// A source whose scope is every remote URL takes no `repos` entry (docs/reference/git-broker.md
// ID-2): a git remote joins with `git remote add`, which reaches the gate as a new remote. The
// refusal comes through the resolver, never the source's name, so an owner-repo source keeps its
// entry.
func TestBrokeredReposRefusedForWildcardSource(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"repos": ["o/r"]}, "github": {"repos": ["org/lib"]}}}`)
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, h.ws, gitResolver)
	got := brokeredLines(strings.Join(errs, "\n"))
	if !strings.Contains(got, "config.brokered.git.repos") || !strings.Contains(got, "git remote add") {
		t.Fatalf("no refusal naming `git remote add` for brokered.git.repos:\n%s", got)
	}
	if strings.Contains(got, "config.brokered.github") {
		t.Errorf("an owner-repo source's entry was refused:\n%s", got)
	}
	// With no resolver declaring git on any host, the entry is the ordinary shape it always was.
	errs, _ = ValidateConfig(cfg, h.ws, nil)
	if got := brokeredLines(strings.Join(errs, "\n")); got != "" {
		t.Errorf("refused with no wildcard source known:\n%s", got)
	}
}

// The gate's scope for a source on any host is the workspace's remote URLs, verbatim, and no
// entry: a `brokered.git.repos` value reaches no scope even where nothing validated it, and a URL
// carrying a password is left out with a line that says why.
func TestTheRemoteURLScopeAtTheGate(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), `[remote "origin"]
	url = git@github.com:o/r.git
	pushurl = ssh://git@github.com/o/r
[remote "lab"]
	url = https://gitlab.example.com/team/app.git
[remote "leak"]
	url = https://me:hunter2@git.example.com/x.git
`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"repos": ["o/r"]}}}`)
	// The user's read grant: a source with none holds nothing at the gate (BB-D110).
	write(t, h.userCfg, `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(h.ws, []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*"}}, read)
	s := check.Sources[0]
	if s.Form != brokerscope.FormRemoteURL || len(s.Entry) != 0 {
		t.Fatalf("source %+v, want the remote-url form and no entry", s)
	}
	want := []string{"git@github.com:o/r.git", "https://gitlab.example.com/team/app.git", "ssh://git@github.com/o/r"}
	if got := s.Repos(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("scope %q, want %q", got, want)
	}
	r, err := ScopeChanges(h.ws, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(r.ScopeBlock, "\n")
	for _, w := range append(want, `remote "origin"`, `remote "lab"`, `"leak"`, "git-broker: 3 added") {
		if !strings.Contains(block+"\n"+strings.Join(r.ScopeCounts, "\n"), w) {
			t.Errorf("the gate does not show %q:\n%s", w, block)
		}
	}
	if strings.Contains(block, "hunter2") || strings.Contains(block, "me:") || strings.Contains(block, " o/r ") {
		t.Errorf("the gate shows the password, the user, or the entry:\n%s", block)
	}
	t.Logf("gate block:\n%s\n%s", block, strings.Join(r.ScopeCounts, "\n"))
	if err := RecordApproval(h.ws, read.Config, check); err != nil {
		t.Fatal(err)
	}
	if got := ApprovedScope(h.ws, "git"); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("approval record %q", got)
	}
	// A URL differing only in case is a different URL: it is a change the gate asks about.
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n\turl = git@GitHub.com:o/r.git\n")
	read, _ = ReadWorkspaceForGate(h.ws)
	r, err = ScopeChanges(h.ws, NewScopeCheck(h.ws, []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*"}}, read))
	if err != nil || !r.ScopeChanged || !strings.Contains(strings.Join(r.ScopeCounts, ""), "1 added, 3 removed") {
		t.Fatalf("a case-only URL change: %+v, %v", r, err)
	}
}

// WW-D18: the gate's read is strict, so a file it cannot parse is an error naming it, never an
// empty config whose entry reads as empty; and its entry unions the workspace's files without
// case, naming every file that lists each repository, with `null` clearing the list.
func TestTheGatesReadIsStrictAndItsEntryNamesEachFile(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"repos": ["org/lib"]`)
	if _, err := ReadWorkspaceForGate(h.ws); err == nil || !strings.Contains(err.Error(), "Failed to parse "+WorkspaceConfigName) {
		t.Fatalf("an unparseable config read as %v, want the parse error naming the file", err)
	}

	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"repos": ["org/lib", "Org/Docs"]}},
	  "include_if_found": ["conf/extra.jsonc"]}`)
	write(t, filepath.Join(h.ws, "conf", "extra.jsonc"), `{"brokered": {"github": {"repos": ["ORG/LIB", "x/y"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceLocalConfigName), `{"brokered": {"github": {"repos": ["org/lib", "bad"]}}}`)
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	got := read.Entry("github")
	var rows []string
	for _, e := range got {
		rows = append(rows, e.Repo+"="+strings.Join(e.Files, ","))
	}
	want := "org/lib=yolo-jail.jsonc," + filepath.Join("conf", "extra.jsonc") + ",yolo-jail.local.jsonc|" +
		"Org/Docs=yolo-jail.jsonc|x/y=" + filepath.Join("conf", "extra.jsonc")
	if strings.Join(rows, "|") != want {
		t.Errorf("entry %s, want %s", strings.Join(rows, "|"), want)
	}
	if read.Entry("gitlab") != nil {
		t.Error("another source's entry is not this one's")
	}

	write(t, filepath.Join(h.ws, WorkspaceLocalConfigName), `{"brokered": {"github": {"repos": null}}}`)
	read, err = ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if got := read.Entry("github"); len(got) != 0 {
		t.Errorf("a local `repos: null` left %+v, want the list cleared", got)
	}
}

// BB-D23/§5.7: a source's permission sets are declared at user scope under `sets`, each validated
// in shape and each admitted rule kept; the reader the launch's disclosure uses (BrokeredSets)
// returns what the validator accepted, in the order written.
func TestUserScopeBrokeredSetsAreValidatedAndReadable(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"github": {"sets": [
	  {"name": "read-only", "mode": "standing", "admit": ["pr comment"]},
	  {"name": "review", "mode": "windowed", "window": "1h", "max_window": "session", "uses": 50,
	   "admit": ["pr comment", "pr review"]},
	  {"name": "read-write", "mode": "windowed", "window": "15m"}
	]}}}`)
	if errs, _ := h.validate(t); brokeredLines(errs) != "" {
		t.Fatalf("a user-scope sets list was refused:\n%s", errs)
	}
	want := "read-only (+ pr comment), review 1h max session 50 uses (+ pr comment, + pr review), read-write 15m"
	if got := BrokeredSetsDisclosure(BrokeredSets("github")); got != want {
		t.Fatalf("disclosure %q, want %q", got, want)
	}
	if got := BrokeredSets("gitlab"); got != nil {
		t.Errorf("another source's sets are this source's: %v", got)
	}
}

// BB-D23/§5.7: every malformed set is refused at the user file it was written at, and a bad entry
// is skipped rather than repaired. A set with no `admit` keeps the source's default rules, which
// is why it is accepted.
func TestEachMalformedBrokeredSetIsRefused(t *testing.T) {
	h := newBrokeredHost(t)
	for body, want := range map[string]string{
		`{"brokered": {"github": {"sets": {}}}}`:                                                                     "config.brokered.github.sets: expected a list of sets",
		`{"brokered": {"github": {"sets": []}}}`:                                                                     "an empty list defines no set",
		`{"brokered": {"github": {"sets": [3]}}}`:                                                                    "config.brokered.github.sets[0]: expected an object",
		`{"brokered": {"github": {"sets": [{"mode": "standing"}]}}}`:                                                 ".name: expected a set name",
		`{"brokered": {"github": {"sets": [{"name": "Read", "mode": "standing"}]}}}`:                                 "is not a set name",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "always"}]}}}`:                                      ".mode: expected",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "standing", "window": "1h"}]}}}`:                    ".window: only a windowed set",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "window": "soon"}]}}}`:                  ".window: expected a duration",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "window": "session"}]}}}`:               ".window: expected a duration",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "uses": 0}]}}}`:                         ".uses: expected a whole number",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "uses": 1.5}]}}}`:                       ".uses: expected a whole number",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "admit": "pr"}]}}}`:                     ".admit: expected a list",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "admit": [""]}]}}}`:                     ".admit[0]: expected a non-empty",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "standing", "nope": 1}]}}}`:                         ".nope: unknown key",
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "standing"}, {"name": "a", "mode": "windowed"}]}}}`: "is named twice",
	} {
		write(t, h.userCfg, body)
		errs, _ := h.validate(t)
		if !strings.Contains(errs, want) || !strings.Contains(errs, "config.jsonc:1:") {
			t.Errorf("%s: errors lack %q (or the location):\n%s", body, want, errs)
		}
	}
	for _, ok := range []string{
		`{"brokered": {"github": {"sets": [{"name": "read-only", "mode": "standing"}]}}}`,
		`{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "max_window": "session"}]}}}`,
	} {
		write(t, h.userCfg, ok)
		if errs, _ := h.validate(t); brokeredLines(errs) != "" {
			t.Errorf("%s refused though it is valid:\n%s", ok, errs)
		}
	}
	// A rule a launch prints cannot carry a control character.
	write(t, h.userCfg, `{"brokered": {"github": {"sets": [{"name": "a", "mode": "windowed", "admit": ["pr\ncomment"]}]}}}`)
	if errs, _ := h.validate(t); !strings.Contains(errs, "carries a control character") {
		t.Errorf("a rule with a newline was not refused:\n%s", errs)
	}
}

// gitGrantResolver is gitResolver with the git manifest's defined grant kinds.
var gitGrantResolver = fenceResolver{known: map[string]LoopholeInfo{
	"git-broker": {Name: "git-broker", HasHostDaemon: true, Brokered: &loopholedecl.Brokered{
		Source: "git", RemoteHost: "*", Grants: []string{"create-branch", "branch", "tag"}}},
}}

// validateWith loads the config as the launch does and validates it with resolver, keeping the
// lines about `brokered`.
func (h brokeredHost) validateWith(t *testing.T, resolver LoopholeResolver) (errs, warns string) {
	t.Helper()
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	e, w := ValidateConfig(cfg, h.ws, resolver)
	return brokeredLines(strings.Join(e, "\n")), brokeredLines(strings.Join(w, "\n"))
}

const oneGitGrant = `{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`

// A workspace grant of a kind the user's own config does not allow is refused, naming the
// user-config key that would allow it (BB-D96; docs/reference/git-broker.md ID-10).
func TestGrantKindNotAllowedNamesUserKey(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneGitGrant)
	errs, _ := h.validateWith(t, gitGrantResolver)
	if !strings.Contains(errs, "brokered.git.grant_kinds") || !strings.Contains(errs, "create-branch") {
		t.Fatalf("no refusal naming brokered.git.grant_kinds and create-branch:\n%s", errs)
	}
}

// In a jail the user config is the host-generated snapshot, so a kind it does not allow warns, with
// the in-jail ending every such user-scope finding carries, rather than refusing; the host's
// launch enforces it.
func TestGrantKindNotAllowedOnlyWarnsInAJail(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneGitGrant)
	t.Setenv("YOLO_VERSION", "test")
	errs, warns := h.validateWith(t, gitGrantResolver)
	if strings.Contains(errs, "brokered.git.grant_kinds") {
		t.Fatalf("a jail refused a kind only the host's user config can allow:\n%s", errs)
	}
	if !strings.Contains(warns, "brokered.git.grant_kinds") || !strings.Contains(warns, "the host's launch enforces it") {
		t.Fatalf("no in-jail warning naming brokered.git.grant_kinds:\n%s", warns)
	}
}

func TestGrantKindAllowedPasses(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneGitGrant)
	if errs, warns := h.validateWith(t, gitGrantResolver); errs != "" || warns != "" {
		t.Fatalf("an allowed grant was not accepted:\nerrors:\n%s\nwarnings:\n%s", errs, warns)
	}
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "*"}]}}}`)
	if errs, _ := h.validateWith(t, gitGrantResolver); errs != "" {
		t.Fatalf("the written-out wildcard was refused:\n%s", errs)
	}
}

// Each wrong shape has its own message: a kind the source does not define, a repo that is not a
// repository identity, `grant_kinds` at workspace scope, a user-scope grant other than the read
// grant for every workspace (BB-D107), and a workspace read grant the user config does not allow.
// A workspace read grant the user config allows, or holds itself, passes.
func TestGrantShapeRefusals(t *testing.T) {
	for _, c := range []struct {
		name, user, ws    string
		wantErr, wantWarn string
	}{
		{"undefined kind", `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`,
			`{"brokered": {"git": {"grants": [{"kind": "create-pr", "repo": "github.com/o/r"}]}}}`,
			`config.brokered.git.grants[0].kind: 'create-pr' is not a grant kind the git source defines (it defines: read, create-branch, branch, tag)`, ""},
		{"repo without host", `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`,
			`{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "o/r"}]}}}`,
			`config.brokered.git.grants[0].repo: 'o/r' is not a repository identity`, ""},
		{"grant_kinds at workspace scope", `{}`,
			`{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`,
			"config.brokered.git.grant_kinds: user scope only", ""},
		{"another kind at user scope", `{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "github.com/o/r"}]}}}`,
			`{}`, "config.brokered.git.grants[0].kind: 'create-branch' is not a grant the user config holds", ""},
		{"one repository at user scope", `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "github.com/o/r"}]}}}`,
			`{}`, "config.brokered.git.grants[0].repo:", ""},
		{"read at workspace scope, not allowed", `{}`,
			`{"brokered": {"git": {"grants": [{"kind": "read", "repo": "github.com/o/r"}]}}}`,
			"your user config does not let a workspace hold a 'read' grant", ""},
		{"read at workspace scope, allowed", `{"brokered": {"git": {"grant_kinds": ["read"]}}}`,
			`{"brokered": {"git": {"grants": [{"kind": "read", "repo": "github.com/o/r"}]}}}`, "", ""},
		{"read at workspace scope, user holds read", `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`,
			`{"brokered": {"git": {"grants": [{"kind": "read", "repo": "github.com/o/r"}]}}}`, "", ""},
		{"read for every workspace", `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`, `{}`, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBrokeredHost(t)
			write(t, h.userCfg, c.user)
			write(t, filepath.Join(h.ws, WorkspaceConfigName), c.ws)
			errs, warns := h.validateWith(t, gitGrantResolver)
			if c.wantErr == "" && errs != "" {
				t.Errorf("unexpected errors:\n%s", errs)
			}
			if c.wantErr != "" && !strings.Contains(errs, c.wantErr) {
				t.Errorf("errors do not contain %q:\n%s", c.wantErr, errs)
			}
			if c.wantWarn != "" && !strings.Contains(warns, c.wantWarn) {
				t.Errorf("warnings do not contain %q:\n%s", c.wantWarn, warns)
			}
		})
	}
}

// --- branch grants: a working-set branch declared in config (docs/reference/git-broker.md GB-D12, GB-D13) ---

const oneBranchGrant = `{"brokered": {"git": {"grants": [{"kind": "branch", "repo": "github.com/o/r", "branch": "main"}]}}}`

// `yolo check` refuses a branch grant the user's own config does not allow, naming the
// user-config key, exactly as it does a create-branch grant.
func TestBranchGrantKindNotAllowedNamesUserKey(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["create-branch"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneBranchGrant)
	errs, _ := h.validateWith(t, gitGrantResolver)
	if !strings.Contains(errs, "brokered.git.grant_kinds") || !strings.Contains(errs, "branch") {
		t.Fatalf("no refusal naming brokered.git.grant_kinds and branch:\n%s", errs)
	}
}

// An allowed branch grant is accepted, on one repository or on `*`, and the gate's read
// carries it with its branch, so it can join the scope.
func TestBranchGrantAllowedPasses(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["branch"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), oneBranchGrant)
	if errs, warns := h.validateWith(t, gitGrantResolver); errs != "" || warns != "" {
		t.Fatalf("%s was not accepted:\nerrors:\n%s\nwarnings:\n%s", oneBranchGrant, errs, warns)
	}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	grants, _ := read.Grants("git", []string{"create-branch", "branch"})
	want := brokerscope.Grant{Kind: "branch", Repo: "github.com/o/r", Branch: "main"}
	if len(grants) != 1 || grants[0] != want {
		t.Fatalf("the gate's read carries %+v, want [%+v]", grants, want)
	}
	// The same repository's grants on two branches are two grants, not a duplicate.
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [`+
		`{"kind": "branch", "repo": "github.com/o/r", "branch": "main"},`+
		`{"kind": "branch", "repo": "github.com/o/r", "branch": "dev"}]}}}`)
	if errs, _ := h.validateWith(t, gitGrantResolver); errs != "" {
		t.Fatalf("grants on two branches were refused:\n%s", errs)
	}
}

// A branch grant's branch is refused at config load unless it is a short name git accepts, or a
// pattern with one `*` (GB-D25, TestRefGrantPatternRefusals); a branch grant without one, and
// any other kind with one, are refused too. Each refusal names the next step.
func TestBranchGrantBadBranchRefused(t *testing.T) {
	for _, c := range []struct{ name, grant, want string }{
		{"missing", `{"kind": "branch", "repo": "github.com/o/r"}`,
			`config.brokered.git.grants[0].branch: a branch grant names its branch`},
		{"full ref", `{"kind": "branch", "repo": "github.com/o/r", "branch": "refs/heads/main"}`,
			`config.brokered.git.grants[0].branch: 'refs/heads/main' is not a branch name`},
		{"double dot", `{"kind": "branch", "repo": "github.com/o/r", "branch": "a..b"}`,
			`config.brokered.git.grants[0].branch: 'a..b' is not a branch name`},
		{"not a string", `{"kind": "branch", "repo": "github.com/o/r", "branch": 3}`,
			`config.brokered.git.grants[0].branch: 3 is not a branch name`},
		{"create-branch with a branch", `{"kind": "create-branch", "repo": "github.com/o/r", "branch": "main"}`,
			`config.brokered.git.grants[0].branch: only a branch grant names a branch`},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newBrokeredHost(t)
			write(t, h.userCfg, `{"brokered": {"git": {"grant_kinds": ["create-branch", "branch"]}}}`)
			write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [`+c.grant+`]}}}`)
			errs, _ := h.validateWith(t, gitGrantResolver)
			if !strings.Contains(errs, c.want) {
				t.Fatalf("errors do not contain %q:\n%s", c.want, errs)
			}
			read, err := ReadWorkspaceForGate(h.ws)
			if err != nil {
				t.Fatal(err)
			}
			if grants, _ := read.Grants("git", []string{"create-branch", "branch"}); len(grants) != 0 {
				t.Fatalf("a refused grant reached the gate's read: %+v", grants)
			}
		})
	}
}
