package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func readableScopeBroker(pack, source, label, host, readCapability string, grants []string, kinds map[string]string) ScopeBroker {
	decl := &ReadableBrokerDeclaration{Pack: pack, Label: label,
		Brokered:       loopholedecl.Brokered{Source: source, RemoteHost: host, Grants: grants},
		ReadCapability: readCapability, KindCapabilities: kinds}
	return ScopeBroker{Source: source, Label: label, RemoteHost: host, GrantKinds: grants, Readable: decl}
}

func readableScopeEntry(entries []EntryRepo, repo, file string) bool {
	for _, entry := range entries {
		if entry.Repo == repo && inList(entry.Files, file) {
			return true
		}
	}
	return false
}

func writeReadableScopeRemote(t *testing.T, workspace, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "config"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeReadableScopeUser(t *testing.T, home, body string) {
	t.Helper()
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSelectedScopeFailsClosedForMissingAndConflictingPackProvenance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		broker ScopeBroker
	}{
		{name: "config-only impostor", broker: readableScopeBroker("spoofed-owner", "git", "git-broker", "github.com", "fetch", nil, nil)},
		{name: "conflicting owner", broker: ScopeBroker{Source: "git", Label: "git-broker", RemoteHost: "github.com", Pack: "git-owner",
			Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "github.com", ReadCapability: "fetch"},
			Readable: &ReadableBrokerDeclaration{Pack: "spoofed-owner", Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "github.com", ReadCapability: "fetch"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\n")
			workspace := t.TempDir()
			read, err := ReadWorkspaceForGate(workspace)
			if err != nil {
				t.Fatal(err)
			}
			selected := []ReadablePackDeclaration{{Pack: "git-owner"}}
			check := NewSelectedScopeCheck(workspace, []ScopeBroker{tc.broker}, read, selected, nil)
			if check == nil || check.Fatal == nil {
				t.Fatalf("selected scope accepted missing/conflicting pack provenance: %+v", check)
			}
			if check.ReadableInput != nil || len(check.Sources) != 1 || len(check.Sources[0].Grants) != 0 {
				t.Fatalf("failed provenance reached grants or readable input: %+v", check)
			}
		})
	}
}

func TestSelectedScopeRejectsMixedCanonicalAndMetadataFreeBrokers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	git := ScopeBroker{Source: "git", Label: "git-broker", RemoteHost: "github.com", Pack: "git-owner",
		Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "github.com", ReadCapability: "fetch"}}
	legacy := ScopeBroker{Source: "github", Label: "github-broker", RemoteHost: "github.com"}
	check := NewSelectedScopeCheck(workspace, []ScopeBroker{git, legacy}, read, []ReadablePackDeclaration{{Pack: "git-owner"}}, []ReadableSelectedBroker{{Pack: "git-owner", Label: "git-broker", Brokered: git.Brokered}})
	if check == nil || check.Fatal == nil {
		t.Fatalf("mixed provenance fell through to legacy scope: %+v", check)
	}
	if check.ReadableInput != nil || len(check.Sources) != 2 {
		t.Fatalf("mixed provenance produced a partial readable input: %+v", check)
	}
	for _, source := range check.Sources {
		if len(source.Grants) != 0 {
			t.Fatalf("mixed provenance granted %s: %+v", source.Source, source.Grants)
		}
	}
}

func TestSelectedScopeDoesNotDropUnknownAllowanceAuthority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\n\n[allow.unselected-auth]\nprofile = \"prod\"\n")
	workspace := t.TempDir()
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	brokered := loopholedecl.Brokered{Source: "git", RemoteHost: "github.com", ReadCapability: "fetch"}
	broker := ScopeBroker{Source: "git", Label: "git-broker", RemoteHost: "github.com", Pack: "git-owner", Brokered: brokered}
	check := NewSelectedScopeCheck(workspace, []ScopeBroker{broker}, read, []ReadablePackDeclaration{{Pack: "git-owner"}}, []ReadableSelectedBroker{{Pack: "git-owner", Label: "git-broker", Brokered: brokered}})
	if check == nil || check.Fatal == nil || !strings.Contains(check.Fatal.Error(), "unselected-auth") {
		t.Fatalf("unselected allowance was silently discarded or granted: %+v", check)
	}
	if check.ReadableInput != nil || len(check.Sources) != 1 || len(check.Sources[0].Grants) != 0 {
		t.Fatalf("unknown allowance reached a readable grant: %+v", check)
	}
}

func TestReadableLegacyBranchProjectionNamesOnlyUnrepresentableRepository(t *testing.T) {
	first := brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/first", Branch: "main"}
	second := brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/second", Branch: "main"}
	projection := CompareReadableLegacyGrants("git", []brokerscope.Grant{first}, []brokerscope.Grant{first, second})
	if len(projection.Raw) != 1 || projection.Raw[0] != first || len(projection.Concrete) != 2 {
		t.Fatalf("projection lost its legacy or concrete semantics: %#v", projection)
	}
	if len(projection.Differences) != 1 || !strings.Contains(projection.Differences[0], second.String()) || strings.Contains(projection.Differences[0], first.String()) || !strings.Contains(projection.Differences[0], "rather than write") {
		t.Fatalf("migration difference did not isolate the widened repository: %#v", projection.Differences)
	}
}

func TestReadableScopeProducerReadsProjectRequestThroughPublicGateBoundary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.shared]\nrepo = \"github.com/acme/shared\"\n[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if len(check.Sources) != 1 {
		t.Fatalf("scope sources = %#v", check.Sources)
	}
	source := check.Sources[0]
	if !readableScopeEntry(source.Entry, "acme/shared", WorkspaceTOMLConfigName) {
		t.Fatalf("the real gate consumer dropped the named request: %#v", source)
	}
	want := brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/shared"}
	if !containsGrant(source.Grants, want) || !containsGrant(source.UserGrants, want) {
		t.Fatalf("the concrete user ceiling was not delivered through the gate consumer: %#v", source)
	}
	if source.Dormant {
		t.Fatal("a concrete requested repository with readable authority was treated as dormant")
	}
}

func TestReadableGitRequestKeepsConcreteRemoteAndNewRequestOriginAtGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[requests.git]\npush = [\"main\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"})
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if len(check.Sources) != 1 || check.Sources[0].Dormant {
		t.Fatalf("Git's selected declaration did not produce a live gate source: %#v", check)
	}
	source := check.Sources[0]
	if len(source.Read.Remotes) != 1 || source.Read.Remotes[0].Repo != "https://github.com/acme/repo.git" {
		t.Fatalf("the existing remote-url scope was changed: %#v", source.Read)
	}
	if !containsGrant(source.Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/repo", Branch: "main"}) {
		t.Fatalf("the declared push request did not become a concrete branch grant: %#v", source.Grants)
	}
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	if !report.ScopeChanged || len(report.EntryEdits) == 0 || report.EntryEdits[0].Key != "requests.git.push" || !inList(report.EntryEdits[0].Files, WorkspaceTOMLConfigName) {
		t.Fatalf("gate remediation projected the request back to a legacy key: %#v", report)
	}
	for _, edit := range report.EntryEdits {
		if !strings.HasPrefix(edit.Key, "requests.") {
			t.Errorf("readable grant remediation uses non-request key %q", edit.Key)
		}
	}
}

func TestReadableScopeAdaptsLegacyUserReadCeilingForReadableWorkspaceRequest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "config.jsonc"), []byte(`{"brokered":{"github":{"grants":[{"kind":"read","repo":"*"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.shared]\nrepo = \"github.com/acme/shared\"\n[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if len(check.Sources) != 1 || !readableScopeEntry(check.Sources[0].Entry, "acme/shared", WorkspaceTOMLConfigName) {
		t.Fatalf("legacy user allowance did not preserve the readable project request: %#v", check)
	}
	want := brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/shared"}
	if !containsGrant(check.Sources[0].Grants, want) {
		t.Fatalf("legacy read * did not become a concrete gate grant for the requested repo: %#v", check.Sources[0])
	}
}

// RC-D26: a github.com repository a project requests joins the remote-url broker's scope as its
// HTTPS and SSH URLs and a READ grant, and a push grant the user's own config offers for every
// repository (`requests.git.push`) never reaches it.
func TestReadableNamedRepositoryJoinsRemoteURLBrokerReadScopeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.private]\nrepo = \"github.com/acme/private\"\n[requests.github]\n[requests.git]\npush = [\"main\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	brokers := []ScopeBroker{
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"}),
		readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil),
	}
	check := NewScopeCheck(workspace, brokers, read)
	if check.ReadableInput == nil {
		t.Fatal("H scope adapter lost the typed outside-Git repository request")
	}
	var git, github *ScopeSource
	for i := range check.Sources {
		switch check.Sources[i].Source {
		case "git":
			git = &check.Sources[i]
		case "github":
			github = &check.Sources[i]
		}
	}
	if git == nil || git.Read.Problem != "" {
		t.Fatalf("a requested github.com repository is still called unrepresentable: %#v", git)
	}
	for _, url := range []string{"https://github.com/acme/private", "git@github.com:acme/private.git"} {
		if !readableScopeEntry(git.Entry, url, WorkspaceTOMLConfigName) {
			t.Errorf("the git broker's scope lacks the requested repository's URL %q: %#v", url, git.Entry)
		}
	}
	if git.EntryKey != "repos.private.repo" {
		t.Errorf("remediation names %q, want the request's own key", git.EntryKey)
	}
	if !containsGrant(git.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/private"}) {
		t.Fatalf("the requested repository gained no read grant on the git broker: %#v", git.Grants)
	}
	for _, g := range git.Grants {
		if g.Kind != brokerscope.KindRead {
			t.Errorf("a requested repository gained a %q grant on the git broker: %#v", g.Kind, git.Grants)
		}
	}
	if github == nil || !readableScopeEntry(github.Entry, "acme/private", WorkspaceTOMLConfigName) || !containsGrant(github.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/private"}) {
		t.Fatalf("the named-host broker lost its representable target: %#v", github)
	}
}

func TestDormantReadableSourcePreservesHistoryWithoutLiveAuthorityOrPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = []\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := githubScope(origin)
	if err := RecordApproval(workspace, decode(t, `{}`), stale); err != nil {
		t.Fatal(err)
	}
	beforeScope, err := os.ReadFile(ApprovalScopePath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	beforeSources, err := os.ReadFile(ApprovalScopeSourcesPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if len(check.Sources) != 1 || !check.Sources[0].Dormant {
		t.Fatalf("no-read source should be dormant: %#v", check)
	}
	prompter := &reportPrompter{accept: true}
	ok, approved, err := ApproveConfigAndScope(workspace, decode(t, `{}`), check, true, false, prompter)
	if err != nil || !ok || prompter.got != nil {
		t.Fatalf("preserving inert scope history prompted: ok=%v err=%v report=%#v", ok, err, prompter.got)
	}
	// BB-D110 keeps old records inert, not deleted or lent to a current launch.
	afterScope, err := os.ReadFile(ApprovalScopePath(workspace))
	if err != nil || string(afterScope) != string(beforeScope) {
		t.Fatalf("dormant scope history changed: %q, %v", afterScope, err)
	}
	afterSources, err := os.ReadFile(ApprovalScopeSourcesPath(workspace))
	if err != nil || string(afterSources) != string(beforeSources) {
		t.Fatalf("dormant source history changed: %q, %v", afterSources, err)
	}
	if len(approved[broker.Label]) != 0 || len(check.ApprovedGrants()[broker.Label]) != 0 {
		t.Fatalf("dormant history reached live authority: %v, %v", approved, check.ApprovedGrants())
	}

}

func TestReadableScopeVacuousStateHasNoPhantomRecordAndRealRemovalPromptsOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"create-branch", "branch"}, map[string]string{"create-branch": "new_branches", "branch": "push"})
	readGate := func() *ScopeCheck {
		t.Helper()
		read, err := ReadWorkspaceForGate(workspace)
		if err != nil {
			t.Fatal(err)
		}
		return NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	}
	check := readGate()
	if len(check.Sources) != 1 || !check.Sources[0].Dormant || len(check.Sources[0].Grants) != 0 {
		t.Fatalf("vacuous scope should have no usable grants: %#v", check)
	}
	prompter := &reportPrompter{accept: true}
	ok, err := CheckConfigAndScopeChanges(workspace, decode(t, `{}`), check, true, false, prompter)
	if err != nil || !ok || prompter.got != nil {
		t.Fatalf("vacuous scope prompted or failed: ok=%v err=%v report=%#v", ok, err, prompter.got)
	}
	stored, _, err := readScopeRecord(ApprovalScopePath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := stored["git"]; exists {
		t.Fatalf("a vacuous scope created a phantom source record: %#v", stored)
	}
	if _, exists := stored[grantsRecordKey("git")]; exists {
		t.Fatalf("a ceiling alone created a phantom grant record: %#v", stored)
	}
	storedSources, err := readSourcesRecord(ApprovalScopeSourcesPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := storedSources["git"]; exists {
		t.Fatalf("a vacuous scope created a phantom source record: %#v", storedSources)
	}

	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
	prompter = &reportPrompter{accept: true}
	ok, err = CheckConfigAndScopeChanges(workspace, decode(t, `{}`), readGate(), true, false, prompter)
	if err != nil || !ok || prompter.got == nil || !prompter.got.ScopeChanged {
		t.Fatalf("first concrete remote did not prompt: ok=%v err=%v report=%#v", ok, err, prompter.got)
	}

	writeReadableScopeRemote(t, workspace, "")
	prompter = &reportPrompter{accept: true}
	ok, err = CheckConfigAndScopeChanges(workspace, decode(t, `{}`), readGate(), true, false, prompter)
	if err != nil || !ok || prompter.got == nil || !prompter.got.ScopeChanged || !strings.Contains(strings.Join(prompter.got.ScopeBlock, "\n"), "removed") {
		t.Fatalf("removing prior real scope did not prompt once: ok=%v err=%v report=%#v", ok, err, prompter.got)
	}
	prompter = &reportPrompter{accept: true}
	ok, err = CheckConfigAndScopeChanges(workspace, decode(t, `{}`), readGate(), true, false, prompter)
	if err != nil || !ok || prompter.got != nil {
		t.Fatalf("accepted removal prompted again: ok=%v err=%v report=%#v", ok, err, prompter.got)
	}
	stored, _, err = readScopeRecord(ApprovalScopePath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	storedSources, err = readSourcesRecord(ApprovalScopeSourcesPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := stored["git"]; exists {
		t.Fatalf("an emptied source retained a phantom scope key: %#v", stored)
	}
	if _, exists := storedSources["git"]; exists {
		t.Fatalf("an emptied source retained a phantom source key: %#v", storedSources)
	}
}

func TestReadableRequestSourcesMustBeInsideWorkspace(t *testing.T) {
	for _, mode := range []string{"include", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\n")
			workspace := t.TempDir()
			outside := filepath.Join(t.TempDir(), "outside.toml")
			if err := os.WriteFile(outside, []byte("[repos.private]\nrepo = \"github.com/acme/private\"\n[requests.github]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(workspace, WorkspaceTOMLConfigName)
			if mode == "include" {
				parts := filepath.Join(workspace, "parts")
				if err := os.MkdirAll(parts, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(parts, "requests.toml")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte("include_if_found = ['parts/requests.toml']\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(outside, config); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadWorkspaceForGate(workspace); err == nil || !strings.Contains(err.Error(), "outside this workspace") {
				t.Fatalf("outside readable request accepted: %v", err)
			}
		})
	}
}

func TestReadableSemanticRequestErrorsAreFatalBeforeApproval(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-use", true: "previously-approved"}[approved], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
			workspace := t.TempDir()
			writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
			if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[requests.git]\npush = [\"bad..name\"]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			broker := readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"})
			if approved {
				if err := RecordApproval(workspace, decode(t, `{}`), &ScopeCheck{Sources: []ScopeSource{{Source: "git", Label: "git-broker", Grants: []brokerscope.Grant{{Kind: "read", Repo: "github.com/acme/repo"}}}}}); err != nil {
					t.Fatal(err)
				}
			}
			read, err := ReadWorkspaceForGate(workspace)
			if err != nil {
				t.Fatal(err)
			}
			check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
			prompter := &reportPrompter{accept: true}
			ok, err := CheckConfigAndScopeChanges(workspace, decode(t, `{}`), check, true, false, prompter)
			if err == nil || ok || prompter.got != nil {
				t.Fatalf("semantic error was not fatal before approval: ok=%v err=%v prompt=%#v", ok, err, prompter.got)
			}
			var authorityErr *ReadableAuthorityError
			if !errors.As(err, &authorityErr) {
				t.Fatalf("gate returned untyped authority failure: %T %v", err, err)
			}
			if approved && !containsGrant(ApprovedGrants(workspace, "git"), brokerscope.Grant{Kind: "read", Repo: "github.com/acme/repo"}) {
				t.Fatal("fatal readable request error overwrote the previously approved grant")
			}
		})
	}
}

func TestReadableGitRequestForExistingRemotePreservesExistingGrants(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.repo]\nrepo = \"github.com/acme/repo\"\n[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	brokers := []ScopeBroker{
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"}),
		readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil),
	}
	check := NewScopeCheck(workspace, brokers, read)
	for i := range check.Sources {
		if check.Sources[i].Source == "git" && !containsGrant(check.Sources[i].Grants, brokerscope.Grant{Kind: "read", Repo: "github.com/acme/repo"}) {
			t.Fatalf("request for configured remote erased its Git read grant: %#v", check.Sources[i])
		}
	}
}

func TestReadableLegacyWorkspaceScopeAndGrantsSurviveReadableUserAllowance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"push\"]\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"packs":["github"],"brokered":{"github":{"repos":["acme/one","acme/two"],"grants":[{"kind":"branch","repo":"github.com/acme/one","branch":"main"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"branch"}, map[string]string{"branch": "push"})
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.ReadableInput == nil || len(check.ReadableInput.AdditionalRepositories) != 2 ||
		check.ReadableInput.AdditionalRepositories[0].Repository != "acme/one" || check.ReadableInput.AdditionalRepositories[1].Repository != "acme/two" {
		t.Fatalf("legacy named-host targets did not reach the shared producer input before gate projection: %#v", check.ReadableInput)
	}
	if len(check.Sources) != 1 || !readableScopeEntry(check.Sources[0].Entry, "acme/one", WorkspaceConfigName) || !readableScopeEntry(check.Sources[0].Entry, "acme/two", WorkspaceConfigName) {
		t.Fatalf("legacy workspace repositories were discarded: %#v", check.Sources)
	}
	if !containsGrant(check.Sources[0].Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/one", Branch: "main"}) {
		t.Fatalf("legacy repository-specific grant was discarded: %#v", check.Sources[0].Grants)
	}
}

func TestReadableLegacyGitBranchGrantMatchesOnlyItsExactRemote(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"one\"]\nurl = git@github.com:acme/one.git\n[remote \"two\"]\nurl = git@github.com:acme/two.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"packs":["git","github"],"brokered":{"git":{"grants":[{"kind":"branch","repo":"github.com/acme/one","branch":"main"}]},"github":{"repos":["acme/three"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	brokers := []ScopeBroker{
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"}),
		readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil),
	}
	check := NewScopeCheck(workspace, brokers, read)
	var gitSource *ScopeSource
	for i := range check.Sources {
		if check.Sources[i].Source == "git" {
			gitSource = &check.Sources[i]
		}
	}
	if gitSource == nil || !containsGrant(gitSource.Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/one", Branch: "main"}) {
		t.Fatalf("legacy exact-remote branch grant disappeared: %#v", check.Sources)
	}
	if containsGrant(gitSource.Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/two", Branch: "main"}) {
		t.Fatalf("first-remote branch grant widened to the second remote: %#v", gitSource.Grants)
	}
	if check.ReadableInput == nil || len(check.ReadableInput.AdditionalRepositories) != 1 || check.ReadableInput.AdditionalRepositories[0].Source != "github" || check.ReadableInput.AdditionalRepositories[0].Repository != "acme/three" {
		t.Fatalf("legacy named-host target did not reach other gate consumers: %#v", check.ReadableInput)
	}
}

func TestReadableRecordApprovalRejectsFatalBeforeAnyWrite(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-use", true: "previously-approved"}[approved], func(t *testing.T) {
			workspace := approvalWorkspace(t)
			if approved {
				if err := RecordApproval(workspace, decode(t, `{"packages":["jq"]}`), githubScope(origin)); err != nil {
					t.Fatal(err)
				}
			}
			paths := []string{ApprovalSnapshotPath(workspace), ApprovalScopePath(workspace), ApprovalScopeSourcesPath(workspace)}
			before := make(map[string][]byte, len(paths))
			for _, path := range paths {
				before[path], _ = os.ReadFile(path)
			}
			fatal := &ReadableAuthorityError{Cause: errors.New("malformed authority")}
			err := RecordApproval(workspace, decode(t, `{"packages":["changed"]}`), &ScopeCheck{Fatal: fatal, Sources: []ScopeSource{{Source: "github", Label: "github-broker"}}})
			if !errors.Is(err, fatal) {
				t.Fatalf("RecordApproval error = %v, want fatal authority error", err)
			}
			for _, path := range paths {
				after, readErr := os.ReadFile(path)
				if approved && readErr != nil {
					t.Fatalf("previous approval file %s disappeared: %v", path, readErr)
				}
				if string(after) != string(before[path]) {
					t.Fatalf("fatal authority changed %s: before=%q after=%q", path, before[path], after)
				}
				if !approved && readErr == nil {
					t.Fatalf("first-use fatal authority created %s", path)
				}
			}
		})
	}
}

func TestReadableRemovingLastRepositoryUsesAuthoredRequestKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	requestPath := filepath.Join(workspace, WorkspaceTOMLConfigName)
	if err := os.WriteFile(requestPath, []byte("[repos.shared]\nrepo = \"github.com/acme/shared\"\n[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordApproval(workspace, decode(t, `{}`), NewScopeCheck(workspace, []ScopeBroker{broker}, read)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, []byte("[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err = ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ScopeChanges(workspace, NewScopeCheck(workspace, []ScopeBroker{broker}, read))
	if err != nil {
		t.Fatal(err)
	}
	// The removed table names no key any more; the edit still lands on the file that held it.
	// Its key falls back to brokered.<source>.repos (scopeapproval.go, pre-existing), flagged in the report.
	if len(report.EntryEdits) != 1 || !slices.Equal(report.EntryEdits[0].Files, []string{WorkspaceTOMLConfigName}) {
		t.Fatalf("removing final repository lost its file remediation: %#v", report.EntryEdits)
	}
}

func TestReadableNamedHostLegacyGrantUsesActualRemoteWithoutRepoEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"push\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/one.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"packs":["github"],"brokered":{"github":{"grants":[{"kind":"branch","repo":"github.com/acme/one","branch":"main"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"branch"}, map[string]string{"branch": "push"})
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if len(check.Sources) != 1 || !containsGrant(check.Sources[0].Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/one", Branch: "main"}) {
		t.Fatalf("legacy named-host grant without explicit repos disappeared: %#v", check.Sources)
	}
}

func TestReadableRepositoryEntryEditsUseAuthoredRequestKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\n")
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.shared]\nrepo = \"github.com/acme/shared\"\n[requests.github]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(workspace, []ScopeBroker{readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)}, read)
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.EntryEdits) != 1 || report.EntryEdits[0].Key != "repos.shared.repo" {
		t.Fatalf("repository remediation does not name the authored request key: %#v", report.EntryEdits)
	}
	for _, edit := range report.EntryEdits {
		if strings.HasPrefix(edit.Key, "brokered.") {
			t.Errorf("remediation references rejected key %q", edit.Key)
		}
	}
}

// A named repository reaches the git broker as the two URLs its github.com identity spells, and the
// read grant its table makes; the requested-only rows carry no push. A pattern repo line is a
// pattern, not a repository, so it names no URL and no note (RC-D26).
func TestANamedRepositoryReachesTheGitBrokerAsItsTwoURLsAndNoPush(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n[allow.github]\nalways = [\"read\"]\n")
	brokers := []ScopeBroker{
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"}),
		readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil),
	}
	t.Run("a literal repository", func(t *testing.T) {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.notes]\nrepo = \"github.com/example/notes\"\n[requests.git]\npush = [\"main\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		read, err := ReadWorkspaceForGate(workspace)
		if err != nil {
			t.Fatal(err)
		}
		check := NewScopeCheck(workspace, brokers, read)
		var git *ScopeSource
		for i := range check.Sources {
			if check.Sources[i].Source == "git" {
				git = &check.Sources[i]
			}
		}
		if git == nil || git.Read.Problem != "" {
			t.Fatalf("the git broker is missing or calls the named repository unrepresentable: %#v", check.Sources)
		}
		for _, url := range []string{"https://github.com/example/notes", "git@github.com:example/notes.git"} {
			if !readableScopeEntry(git.Entry, url, WorkspaceTOMLConfigName) {
				t.Errorf("the git broker's scope lacks %q: %#v", url, git.Entry)
			}
		}
		if !containsGrant(git.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/example/notes"}) {
			t.Fatalf("the named repository gained no read grant on the git broker: %#v", git.Grants)
		}
		for _, g := range git.Grants {
			if g.Kind != brokerscope.KindRead {
				t.Errorf("the named repository gained a %q grant on the git broker: %s", g.Kind, g.String())
			}
		}
	})
	t.Run("a pattern repository", func(t *testing.T) {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.org]\nrepo = \"github.com/example/*\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		read, err := ReadWorkspaceForGate(workspace)
		if err != nil {
			t.Fatal(err)
		}
		check := NewScopeCheck(workspace, brokers, read)
		for _, s := range check.Sources {
			if s.Source != "git" {
				continue // the named-host broker serves its own host's pattern, as it always has
			}
			if s.Read.Problem != "" {
				t.Errorf("a pattern repository is called unrepresentable on %s: %q", s.Source, s.Read.Problem)
			}
			for _, e := range s.Entry {
				if strings.Contains(e.Repo, "*") || strings.Contains(e.Repo, "example/") {
					t.Errorf("a pattern repository put %q in %s's scope", e.Repo, s.Source)
				}
			}
		}
	})
}
