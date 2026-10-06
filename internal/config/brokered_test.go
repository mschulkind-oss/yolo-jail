package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// file and line it was written at; `sets` is an unknown key, never a message of its own.
func TestAWorkspaceEntryIsAcceptedAndEachBadShapeIsRefusedWhereItWasWritten(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"github": {"repos": ["org/lib", "a.b/c_d-e"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceLocalConfigName), `{"brokered": {"github": {"repos": ["private/one"]}}}`)
	if errs, _ := h.validate(t); brokeredLines(errs) != "" {
		t.Fatalf("a workspace entry was refused:\n%s", errs)
	}

	for body, want := range map[string]string{
		`{"brokered": []}`:                                                "config.brokered: expected an object of source name",
		`{"brokered": {"github": "x"}}`:                                   `config.brokered.github: expected an object holding "repos"`,
		`{"brokered": {"github": {"sets": []}}}`:                          "config.brokered.github.sets: unknown key",
		`{"brokered": {"github": {"repos": "org/lib"}}}`:                  "config.brokered.github.repos: expected a list of OWNER/REPO strings",
		`{"brokered": {"github": {"repos": ["github.com/o/r"]}}}`:         "config.brokered.github.repos[0]: 'github.com/o/r' is not OWNER/REPO",
		`{"brokered": {"github": {"repos": ["ok/one", 3]}}}`:              "config.brokered.github.repos[1]: 3 is not OWNER/REPO",
		`{"brokered": {"github": {"repos": ["one"]}}}`:                    "'one' is not OWNER/REPO",
		`{"brokered": {"github": {"repos": ["a/b/c"]}}}`:                  "'a/b/c' is not OWNER/REPO",
		`{"brokered": {"github": {"repos": ["https://github.com/o/r"]}}}`: "is not OWNER/REPO",
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
	if errs, _ := h.validate(t); !strings.Contains(errs, "config.brokered.github.sets: unknown key") {
		t.Errorf("a user-scope sets is not an unknown key:\n%s", errs)
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
		"'githb', so this entry does nothing here (brokered sources here: github)") ||
		!strings.Contains(joined, "Check the spelling") {
		t.Errorf("no warning, with its next step, for the unbrokered source:\n%s", joined)
	}
	if strings.Contains(joined, "config.brokered.github:") {
		t.Errorf("a warning for a brokered source:\n%s", joined)
	}
	if joined := strings.Join(UnbrokeredSourceWarnings(cfg, fenceResolver{}), "\n"); !strings.Contains(joined,
		"config.brokered.github: no loophole") || !strings.Contains(joined, "(brokered sources here: none)") {
		t.Errorf("with no brokered loophole selected, every source is inert:\n%s", joined)
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
