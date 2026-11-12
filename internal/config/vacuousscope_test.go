package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// vacuousScopeBrokers are the git and github brokers as their shipped packs declare them: git on
// any host (its scope is remote URLs), github on github.com (its scope is OWNER/REPO).
func vacuousScopeBrokers() map[string]ScopeBroker {
	return map[string]ScopeBroker{
		"git": {Source: "git", Label: "git-broker", RemoteHost: brokerscope.AnyRemoteHost, Pack: "git",
			GrantKinds: []string{"create-branch", "branch", "tag"},
			Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: brokerscope.AnyRemoteHost,
				Grants: []string{"create-branch", "branch", "tag"}, ReadCapability: "fetch",
				KindCapabilities: map[string]string{"create-branch": "new_branches", "branch": "push", "tag": "push_tags"}}},
		"github": {Source: "github", Label: "github-broker", RemoteHost: "github.com", Pack: "github",
			Brokered: loopholedecl.Brokered{Source: "github", RemoteHost: "github.com", ReadCapability: "read",
				KindCapabilities:    map[string]string{"create-pr": "create_pr", "create-issue": "create_issue"},
				PendingCapabilities: []string{"create_pr", "create_issue"}}},
	}
}

// vacuousScopeCheckFor reads the gate's scope for both brokers, as a launch selecting both packs
// does, through either producer: the launch's own (NewSelectedScopeCheck, with the selected-pack
// provenance a launch hands it) or the compatibility entry point with no pack metadata
// (NewScopeCheck).
func vacuousScopeCheckFor(t *testing.T, workspace string, selected bool) *ScopeCheck {
	t.Helper()
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	all := vacuousScopeBrokers()
	brokers := []ScopeBroker{all["git"], all["github"]}
	if !selected {
		for i := range brokers {
			brokers[i].Pack, brokers[i].Brokered = "", loopholedecl.Brokered{}
		}
		return NewScopeCheck(workspace, brokers, read)
	}
	var packs []ReadablePackDeclaration
	var declared []ReadableSelectedBroker
	for _, b := range brokers {
		packs = append(packs, ReadablePackDeclaration{Pack: b.Pack})
		declared = append(declared, ReadableSelectedBroker{Pack: b.Pack, Label: b.Label, Brokered: b.Brokered})
	}
	return NewSelectedScopeCheck(workspace, brokers, read, packs, declared)
}

// vacuousScopeBlock is the scope block's lines for one broker label: from its heading up to the
// next broker's heading.
func vacuousScopeBlock(lines []string, label string) string {
	var out []string
	in := false
	for _, line := range lines {
		if strings.HasPrefix(line, label+" repository scope") {
			in = true
		} else if !strings.HasPrefix(line, " ") && strings.Contains(line, " repository scope") {
			in = false
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// vacuousScopeUser writes the user config: the legacy JSONC read grant for both brokers, or the
// readable TOML allowance for both.
func vacuousScopeUser(t *testing.T, form string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, body := "config.jsonc", `{"brokered":{"git":{"grants":[{"kind":"read","repo":"*"}]},"github":{"grants":[{"kind":"read","repo":"*"}]}}}`
	if form == "toml allow" {
		name, body = "config.toml", "[allow.git]\nalways = [\"fetch\"]\n[allow.github]\nalways = [\"read\"]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A broker with no concrete scope — no remote it can serve and no requested repository — asks
// nothing and records nothing, whichever config form grants its read and whichever producer
// reads it; a real remote or a requested repository still asks, and records the grant beside it.
// The owner's principle: no vacuous git/github scope approvals.
func TestAVacuousBrokerScopeAsksNothingAndRecordsNothing(t *testing.T) {
	type wsCase struct {
		name string
		// setup prepares the workspace for the source under test and the config form.
		setup func(t *testing.T, ws, source, form string)
		// concrete is whether a source has concrete scope in this workspace, so asks and records.
		concrete func(source string) bool
		// added is a line the source's scope block must show when it asks.
		added func(source string) string
	}
	workspaces := []wsCase{
		{"non-Git", func(*testing.T, string, string, string) {}, func(string) bool { return false }, nil},
		{"no remotes", func(t *testing.T, ws, _, _ string) { writeReadableScopeRemote(t, ws, "[core]\n\tbare = false\n") },
			func(string) bool { return false }, nil},
		{"one remote", func(t *testing.T, ws, _, _ string) {
			writeReadableScopeRemote(t, ws, "[remote \"origin\"]\n\turl = git@github.com:acme/repo.git\n")
		}, func(string) bool { return true }, func(source string) string {
			if source == "git" {
				return "git@github.com:acme/repo.git"
			}
			return "acme/repo"
		}},
		{"one requested repo", func(t *testing.T, ws, source, form string) {
			if form == "legacy grant" {
				if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName),
					[]byte(`{"brokered":{"`+source+`":{"repos":["acme/requested"]}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err := os.WriteFile(filepath.Join(ws, WorkspaceTOMLConfigName),
				[]byte("[repos.requested]\nrepo = \"github.com/acme/requested\"\n[requests."+source+"]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, func(source string) bool { return source == "github" }, func(string) string { return "acme/requested" }},
	}
	for _, form := range []string{"legacy grant", "toml allow"} {
		for _, wc := range workspaces {
			for _, source := range []string{"git", "github"} {
				for _, selected := range []bool{true, false} {
					producer := map[bool]string{true: "selected", false: "legacy producer"}[selected]
					t.Run(form+"/"+wc.name+"/"+source+"/"+producer, func(t *testing.T) {
						if form == "toml allow" && !selected {
							// The readable allowance is read only with selected-pack provenance; the
							// metadata-free producer is the legacy JSONC path alone.
							t.Skip("the TOML allow form is read only with selected-pack provenance")
						}
						if wc.name == "one requested repo" && source == "git" {
							// A source on any host holds exact remote URLs, never OWNER/REPO:
							// validation refuses `brokered.git.repos` (validateBrokeredWorkspace)
							// and the readable resolver refuses `requests.git.repos` outright.
							t.Skip("a source on any host takes no requested repository in either form")
						}
						vacuousScopeUser(t, form)
						ws := t.TempDir()
						wc.setup(t, ws, source, form)
						label := vacuousScopeBrokers()[source].Label
						check := vacuousScopeCheckFor(t, ws, selected)
						p := &reportPrompter{accept: true}
						ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), check, true, false, p)
						if err != nil || !ok {
							t.Fatalf("gate failed: ok=%v err=%v", ok, err)
						}
						rec, _, err := readScopeRecord(ApprovalScopePath(ws))
						if err != nil {
							t.Fatal(err)
						}
						concrete := wc.concrete(source)
						// A requested repository names one source; the other has none, and a
						// remote serves both.
						otherConcrete := wc.name == "one remote"
						if !concrete {
							if p.got != nil && vacuousScopeBlock(p.got.ScopeBlock, label) != "" {
								t.Fatalf("a vacuous %s scope asked:\n%s", source, strings.Join(p.got.ScopeBlock, "\n"))
							}
							if p.got != nil && !otherConcrete {
								t.Fatalf("no broker has concrete scope, yet the gate asked:\n%s", strings.Join(p.got.ScopeBlock, "\n"))
							}
							if v, has := rec[source]; has {
								t.Fatalf("a vacuous %s scope recorded its scope: %v", source, v)
							}
							if v, has := rec[grantsRecordKey(source)]; has {
								t.Fatalf("a vacuous %s scope recorded grants: %v", source, v)
							}
							if g := check.ApprovedGrants()[label]; len(g) != 0 {
								t.Fatalf("a vacuous %s scope handed its broker grants: %v", source, g)
							}
							return
						}
						if p.got == nil || !p.got.ScopeChanged {
							t.Fatalf("a concrete %s scope did not ask: %#v", source, p.got)
						}
						block := vacuousScopeBlock(p.got.ScopeBlock, label)
						if !strings.Contains(block, wc.added(source)) || !strings.Contains(block, "added") {
							t.Fatalf("the %s scope block does not show %q added:\n%s", source, wc.added(source), block)
						}
						if len(rec[source]) == 0 || !recordHeldRead(rec, source) {
							t.Fatalf("a concrete %s scope was not recorded with its read grant: %v", source, rec)
						}
					})
				}
			}
		}
	}
}

// Removing the last remote of a scope approved under the legacy user grant shows the removal
// once, records nothing for the source afterwards, and asks nothing again.
func TestRemovingTheLastRemoteOfALegacyGrantScopePromptsOnce(t *testing.T) {
	for _, selected := range []bool{true, false} {
		t.Run(map[bool]string{true: "selected", false: "legacy producer"}[selected], func(t *testing.T) {
			vacuousScopeUser(t, "legacy grant")
			ws := t.TempDir()
			gate := func() *reportPrompter {
				t.Helper()
				p := &reportPrompter{accept: true}
				ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), vacuousScopeCheckFor(t, ws, selected), true, false, p)
				if err != nil || !ok {
					t.Fatalf("gate failed: ok=%v err=%v", ok, err)
				}
				return p
			}
			writeReadableScopeRemote(t, ws, "[remote \"origin\"]\n\turl = git@github.com:acme/repo.git\n")
			if p := gate(); p.got == nil {
				t.Fatal("the first remote did not ask")
			}
			writeReadableScopeRemote(t, ws, "")
			p := gate()
			if p.got == nil || !p.got.ScopeChanged {
				t.Fatalf("removing the last remote did not ask: %#v", p.got)
			}
			for _, label := range []string{"git-broker", "github-broker"} {
				if block := vacuousScopeBlock(p.got.ScopeBlock, label); !strings.Contains(block, "removed") {
					t.Fatalf("the %s removal is not shown:\n%s", label, strings.Join(p.got.ScopeBlock, "\n"))
				}
			}
			if p := gate(); p.got != nil {
				t.Fatalf("an accepted removal asked again:\n%s", strings.Join(p.got.ScopeBlock, "\n"))
			}
			rec, _, err := readScopeRecord(ApprovalScopePath(ws))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{"git", "github"} {
				if _, has := rec[source]; has {
					t.Fatalf("an emptied scope kept its source key: %v", rec)
				}
				if _, has := rec[grantsRecordKey(source)]; has {
					t.Fatalf("an emptied scope kept its grants: %v", rec)
				}
			}
		})
	}
}

// A record the gate wrote before it refused vacuous approvals — a read grant over no repository —
// approved nothing: under either config form, a workspace still without concrete scope asks
// nothing about it, and the first real remote still asks and records a live scope.
func TestAPhantomReadRecordAsksNothingAndAFirstRemoteShowsTheGrant(t *testing.T) {
	for _, form := range []string{"legacy grant", "toml allow"} {
		t.Run(form, func(t *testing.T) {
			vacuousScopeUser(t, form)
			ws := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(ApprovalScopePath(ws)), 0o700); err != nil {
				t.Fatal(err)
			}
			phantom := `{"git":[],"git.grants":["read *"],"github":[],"github.grants":["read *"]}`
			if err := os.WriteFile(ApprovalScopePath(ws), []byte(phantom), 0o600); err != nil {
				t.Fatal(err)
			}
			gate := func() *reportPrompter {
				t.Helper()
				p := &reportPrompter{accept: true}
				ok, err := CheckConfigAndScopeChanges(ws, decode(t, `{}`), vacuousScopeCheckFor(t, ws, true), true, false, p)
				if err != nil || !ok {
					t.Fatalf("gate failed: ok=%v err=%v", ok, err)
				}
				return p
			}
			if p := gate(); p.got != nil {
				t.Fatalf("a phantom record asked:\n%s", strings.Join(p.got.ScopeBlock, "\n"))
			}
			writeReadableScopeRemote(t, ws, "[remote \"origin\"]\n\turl = git@github.com:acme/repo.git\n")
			p := gate()
			if p.got == nil {
				t.Fatal("the first remote did not ask")
			}
			for _, label := range []string{"git-broker", "github-broker"} {
				block := vacuousScopeBlock(p.got.ScopeBlock, label)
				if !strings.Contains(block, "acme/repo") || !strings.Contains(block, "added") {
					t.Fatalf("the %s first remote is not shown added:\n%s", label, strings.Join(p.got.ScopeBlock, "\n"))
				}
			}
			rec, _, err := readScopeRecord(ApprovalScopePath(ws))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{"git", "github"} {
				if !recordHeldLiveScope(rec, source) {
					t.Fatalf("the approved first remote was not recorded with its read grant: %v", rec)
				}
			}
		})
	}
}
