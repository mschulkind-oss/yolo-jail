package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func readableMap(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	value, err := json5.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := value.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decoded %T, want ordered map", value)
	}
	return m
}

func readableBrokerDeclarations() []config.ReadableBrokerDeclaration {
	return []config.ReadableBrokerDeclaration{
		{Pack: "git", Label: "git-broker", Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "*", Grants: []string{"create-branch", "branch", "tag"}},
			ReadCapability: "fetch", KindCapabilities: map[string]string{"create-branch": "new_branches", "branch": "push", "tag": "push_tags"}},
		{Pack: "github", Label: "github-broker", Brokered: loopholedecl.Brokered{Source: "github", RemoteHost: "github.com"},
			ReadCapability: "read", KindCapabilities: map[string]string{}},
	}
}

func TestReadableAllowanceNormalizesCeilingsAndSkipsOutsideRequest(t *testing.T) {
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"],"on_request":["push"]}}}`),
		Workspace: readableMap(t, `{"requests":{"git":{"push":["main"],"new_branches":true}}}`), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Requests) != 2 || plan.Requests[0].Status != config.ReadableRequestGranted || plan.Requests[1].Status != config.ReadableRequestSkipped || !strings.Contains(plan.Requests[1].Reason, "allow.git.on_request") {
		t.Fatalf("request decisions = %#v", plan.Requests)
	}
}

// The readable spellings take patterns and tags as the JSONC grants do (git-broker.md GB-D25):
// `requests.git.push` names branch patterns, `requests.git.push_tags` tag patterns.
func TestReadableAllowanceTakesPatternsAndTags(t *testing.T) {
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"],"on_request":["push","push_tags"]}}}`),
		Workspace: readableMap(t, `{"requests":{"git":{"push":["patch/*"],"push_tags":["v*"]}}}`), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	granted := map[string]bool{}
	for _, r := range plan.Requests {
		granted[r.Path] = r.Status == config.ReadableRequestGranted
	}
	if !granted["requests.git.push"] || !granted["requests.git.push_tags"] {
		t.Fatalf("request decisions = %#v", plan.Requests)
	}
}

func TestReadableAllowanceRejectsMalformedValuesAndUndeclaredKinds(t *testing.T) {
	for _, tc := range []struct{ name, user, project, want string }{
		{"bad list type", `{"allow":{"git":{"always":["fetch"]}}}`, `{"requests":{"git":{"push":true}}}`, "requests.git.push"},
		{"unknown allowance", `{"allow":{"git":{"always":["mystery"]}}}`, `{}`, "declared capabilities"},
		{"unknown declared request regardless of ceiling", `{}`, `{"requests":{"github":{"create_pr":true}}}`, "not a canonical request"},
		{"wildcard source rejects repo list", `{"allow":{"git":{"always":["fetch"]}}}`, `{"requests":{"git":{"repos":["acme/tools"]}}}`, "remote_host"},
		{"invalid branch is fatal", `{}`, `{"requests":{"git":{"push":["bad..name"]}}}`, "is not a ref name a grant can take"},
		{"two stars are fatal", `{}`, `{"requests":{"git":{"push_tags":["v*.*"]}}}`, "is not a ref name a grant can take"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := config.ReadableAllowanceInput{User: readableMap(t, tc.user), Workspace: readableMap(t, tc.project), SelectedPacks: []string{"git", "github"}, Declarations: readableBrokerDeclarations()}
			_, err := config.ResolveReadableAllowance(input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadableSettingsUseDeclaredTypeScopeSelectionAndOrigin(t *testing.T) {
	declarations := []config.ReadablePackDeclaration{{Pack: "auth-pack", Settings: []loopholedecl.Setting{
		{Key: "profile", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser},
		{Key: "visible", Type: loopholedecl.SettingTypeStringList, Scope: loopholedecl.SettingScopeWorkspace},
	}}}
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"auth-pack":{"profile":"work","visible":["a"]}}}`),
		Workspace: readableMap(t, `{"requests":{"auth-pack":{"visible":[]}}}`), SelectedPacks: []string{"auth-pack"}, PackSettings: declarations,
		UserOrigins: map[string][]string{"allow.auth-pack.visible": {"user.toml"}}, WorkspaceOrigins: map[string][]string{"requests.auth-pack.visible": {"project.toml"}}}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.PackSettings["auth-pack"]; got["profile"] != "work" || !reflect.DeepEqual(got["visible"], []string{}) {
		t.Fatalf("settings = %#v", got)
	}
	if !reflect.DeepEqual(plan.PackSettingOrigins["auth-pack"]["visible"], []string{"user.toml", "project.toml"}) {
		t.Errorf("user origins = %#v", plan.PackSettingOrigins)
	}
	input.Workspace = readableMap(t, `{"requests":{"auth-pack":{"profile":"other"}}}`)
	if _, err := config.ResolveReadableAllowance(input); err == nil || !strings.Contains(err.Error(), "user-scope only") {
		t.Fatalf("scope error = %v", err)
	}
	input.User = readableMap(t, `{"allow":{"auth-pack":{"profile":false}}}`)
	input.Workspace = readableMap(t, `{}`)
	if _, err := config.ResolveReadableAllowance(input); err == nil || !strings.Contains(err.Error(), "allow.auth-pack.profile") {
		t.Fatalf("type error = %v", err)
	}
	input.User = readableMap(t, `{"allow":{"auth-pack":{"profile":"work"}}}`)
	input.SelectedPacks = nil
	plan, err = config.ResolveReadableAllowance(input)
	if err != nil || len(plan.PackSettings) != 0 || len(plan.Diagnostics) == 0 {
		t.Fatalf("unselected setting result=%#v err=%v", plan, err)
	}
}

func TestReadableGateProducerSpiesOnConcreteInputForBothBrokers(t *testing.T) {
	ws := t.TempDir()
	writeReadableGitConfig(t, ws, `[remote "origin"]
	url = https://github.com/acme/app.git
[remote "upstream"]
	url = git@github.com:acme/upstream.git
`)
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"],"on_request":["push"]},"github":{"always":["read"]}}}`),
		Workspace:        readableMap(t, `{"requests":{"git":{"push":["main"]}},"repos":{"other":{"repo":"github.com/acme/other"}}}`),
		WorkspaceOrigins: map[string][]string{"repos.other.repo": {"sub/team.toml"}}, SelectedPacks: []string{"git", "github"}, Declarations: readableBrokerDeclarations()}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var observed config.ReadableGateInput
	err = config.WithReadableGateInput(ws, plan, func(got config.ReadableGateInput) error { calls++; observed = got; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(observed.Sources) != 2 {
		t.Fatalf("gate calls=%d input=%#v", calls, observed)
	}
	bySource := map[string]config.ReadableGateSource{}
	for _, source := range observed.Sources {
		bySource[source.Source] = source
	}
	git := bySource["git"]
	if got, want := git.Read.Repos(), []string{"git@github.com:acme/upstream.git", "https://github.com/acme/app.git"}; !reflect.DeepEqual(got, want) {
		t.Errorf("any-host actual remotes = %v want %v", got, want)
	}
	if len(git.Entry) != 0 {
		t.Errorf("any-host Git was given legacy entries: %#v", git.Entry)
	}
	if !containsReadableGrant(git.Grants, brokerscope.Grant{Kind: "read", Repo: "github.com/acme/other"}) || !containsReadableGrant(git.Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/app", Branch: "main"}) {
		t.Errorf("Git grants = %#v", git.Grants)
	}
	for _, grant := range git.Grants {
		if grant.Repo == brokerscope.AnyRepo {
			t.Errorf("wildcard grant: %#v", grant)
		}
	}
	if len(git.Targets) != 3 {
		t.Errorf("Git typed concrete targets (including named-host request) = %#v", git.Targets)
	}
	github := bySource["github"]
	if !containsReadableGrant(github.Grants, brokerscope.Grant{Kind: "read", Repo: "github.com/acme/other"}) {
		t.Errorf("GitHub gate source = %#v", github)
	}
}

func TestReadableNamedRepositoryRequestWorksOutsideGitCheckoutAndEmptyScopesDoNotPrompt(t *testing.T) {
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"github":{"always":["read"]},"git":{"always":["fetch"]}}}`),
		Workspace:        readableMap(t, `{"repos":{"private":{"repo":"github.com/acme/private"}}}`),
		WorkspaceOrigins: map[string][]string{"repos.private.repo": {"project.toml"}}, SelectedPacks: []string{"github", "git"}, Declarations: readableBrokerDeclarations()}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]config.ReadableGateSource{}
	for _, source := range gate.Sources {
		bySource[source.Source] = source
	}
	if _, ok := bySource["git"]; !ok {
		t.Fatal("selected Git consumer did not receive the named-host canonical scope target")
	}
	if !containsReadableGrant(bySource["git"].Grants, brokerscope.Grant{Kind: "read", Repo: "github.com/acme/private"}) {
		t.Fatalf("Git did not use its own fetch ceiling for extra target: %#v", bySource["git"])
	}
	if !containsReadableGrant(bySource["github"].Grants, brokerscope.Grant{Kind: "read", Repo: "github.com/acme/private"}) {
		t.Fatalf("GitHub did not get its own read grant: %#v", bySource["github"])
	}

	for _, source := range []string{"git", "github"} {
		ws := t.TempDir()
		// An empty .git config is separately covered from a non-Git workspace.
		if source == "git" {
			writeReadableGitConfig(t, ws, "")
		}
		bare, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"]},"github":{"always":["read"]}}}`),
			Workspace: readableMap(t, `{}`), SelectedPacks: []string{source}, Declarations: readableBrokerDeclarations()})
		if err != nil {
			t.Fatal(err)
		}
		got, err := config.ProduceReadableGateInput(ws, bare)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Sources) != 0 || len(got.AdditionalRepositories) != 0 || len(got.ReadCapabilities) != 1 || !got.ReadCapabilities[0].Always {
			t.Errorf("%s empty scope creates grant/prompt or loses read ceiling metadata: %#v", source, got)
		}
	}
}

func TestReadableAlwaysPushNeedsNamedTargetAndAlwaysNewBranchesNeedsRead(t *testing.T) {
	for _, tc := range []struct {
		name, user, project string
		wantRead            bool
		wantWrites          []brokerscope.Grant
		wantReadRefusal     bool
	}{
		{name: "always push without target", user: `{"allow":{"git":{"always":["fetch","push"]}}}`, project: `{}`, wantRead: true},
		{name: "always push with explicit target", user: `{"allow":{"git":{"always":["fetch","push"]}}}`, project: `{"requests":{"git":{"push":["main"]}}}`, wantRead: true,
			wantWrites: []brokerscope.Grant{{Kind: brokerscope.KindBranch, Repo: "github.com/acme/app", Branch: "main"}}},
		{name: "new branches false", user: `{"allow":{"git":{"always":["fetch"],"on_request":["new_branches"]}}}`, project: `{"requests":{"git":{"new_branches":false}}}`, wantRead: true},
		{name: "always new branches alone without a read ceiling", user: `{"allow":{"git":{"always":["new_branches"]}}}`, project: `{}`},
		{name: "always new branches lacks its read prerequisite", user: `{"allow":{"git":{"on_request":["new_branches"]}}}`, project: `{"requests":{"git":{"new_branches":true}}}`, wantReadRefusal: true},
		{name: "always new branches on concrete scope", user: `{"allow":{"git":{"always":["fetch","new_branches"]}}}`, project: `{}`, wantRead: true,
			wantWrites: []brokerscope.Grant{{Kind: "create-branch", Repo: "github.com/acme/app"}}},
		{name: "requested always new branches stays concrete", user: `{"allow":{"git":{"always":["fetch","new_branches"]}}}`, project: `{"requests":{"git":{"new_branches":true}}}`, wantRead: true,
			wantWrites: []brokerscope.Grant{{Kind: "create-branch", Repo: "github.com/acme/app"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			writeReadableGitConfig(t, ws, `[remote "origin"]
	url = https://github.com/acme/app.git
`)
			plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, tc.user), Workspace: readableMap(t, tc.project), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()})
			if err != nil {
				t.Fatal(err)
			}
			gate, err := config.ProduceReadableGateInput(ws, plan)
			if err != nil {
				t.Fatal(err)
			}

			gotGrants := map[brokerscope.Grant]int{}
			for _, source := range gate.Sources {
				for _, grant := range source.Grants {
					gotGrants[grant]++
				}
			}
			wantGrants := map[brokerscope.Grant]int{}
			if tc.wantRead {
				wantGrants[brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/app"}]++
			}
			for _, grant := range tc.wantWrites {
				wantGrants[grant]++
			}
			if !reflect.DeepEqual(gotGrants, wantGrants) {
				t.Fatalf("exact grants = %#v, want %#v (no additional or wildcard grants)", gotGrants, wantGrants)
			}

			if tc.wantReadRefusal {
				found := false
				for _, request := range gate.Requests {
					if request.Path == "requests.git.new_branches" {
						found = request.Status == config.ReadableRequestSkipped && strings.Contains(request.Reason, "requires its own always read capability")
					}
				}
				if !found {
					t.Fatalf("missing independent read-prerequisite refusal: %#v", gate.Requests)
				}
			}
		})
	}
}

func TestReadableDeclaredFutureActionsRemainPendingRuntime(t *testing.T) {
	decls := readableBrokerDeclarations()
	decls[1].PendingCapabilities = []string{"create_pr", "create_issue"}
	decls[1].Brokered.Grants = []string{"create-pr", "create-issue"}
	decls[1].KindCapabilities = map[string]string{"create-pr": "create_pr", "create-issue": "create_issue"}
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["create_pr","create_issue"]}}}`),
		Workspace: readableMap(t, `{"requests":{"github":{"create_pr":true,"create_issue":true}}}`), SelectedPacks: []string{"github"}, Declarations: decls})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range plan.Requests {
		if request.Status != config.ReadableRequestPending {
			t.Errorf("declared action status=%#v", request)
		}
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 0 {
		t.Fatalf("pending actions materialized writes without transport: %#v", gate)
	}
}

func TestReadableLegacyProjectionPreservesIdentityDormancyAndScopeOrigin(t *testing.T) {
	declarations := []loopholedecl.Setting{{Key: "profile", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser}, {Key: "visible", Type: loopholedecl.SettingTypeStringList, Scope: loopholedecl.SettingScopeWorkspace}}
	projection := config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{
		{Key: "profile", Value: "work", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"},
		{Key: "visible", Value: []any{"one", ""}, DeclaredScope: loopholedecl.SettingScopeWorkspace, OriginScope: "user"},
	}, declarations, config.ReadableLegacyPackState{Selected: true, DefaultEnabled: boolPointer(true)}, map[string][]string{"profile": {"user.toml"}})
	if projection.AllowSettings["profile"] != "work" || !reflect.DeepEqual(projection.AllowSettings["visible"], []string{"one", ""}) || len(projection.Differences) != 0 {
		t.Fatalf("explicit mapping/user-origin projection=%#v", projection)
	}
	literalOff := config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "profile", Value: "off", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"}}, declarations, config.ReadableLegacyPackState{Selected: true, DefaultEnabled: boolPointer(true)}, nil)
	if literalOff.AllowSettings["profile"] != "off" {
		t.Fatalf("literal string off was reinterpreted: %#v", literalOff)
	}
	projection = config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "visible", Value: []any{"one"}, DeclaredScope: loopholedecl.SettingScopeWorkspace, OriginScope: "workspace"}}, declarations, config.ReadableLegacyPackState{Selected: true, DefaultEnabled: boolPointer(true)}, nil)
	if len(projection.RequestSettings) != 1 {
		t.Fatalf("workspace origin did not map to requests: %#v", projection)
	}
	projection = config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "profile", Value: "work", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"}}, declarations,
		config.ReadableLegacyPackState{Selected: true, DefaultEnabled: boolPointer(false), WorkspaceEnabled: boolPointer(false)}, nil)
	if len(projection.AllowSettings) != 0 || projection.MachineOff == nil || !*projection.MachineOff || projection.DefaultEnabled == nil || len(projection.Differences) == 0 {
		t.Fatalf("dormant legacy state lost or activated: %#v", projection)
	}
	projection = config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "profile", Value: "work", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"}}, declarations,
		config.ReadableLegacyPackState{Selected: true, UserEnabled: boolPointer(true), DefaultEnabled: boolPointer(false)}, nil)
	if !*projection.EffectiveEnabled || projection.AllowSettings["profile"] != "work" || projection.UserEnabled == nil || !*projection.UserEnabled {
		t.Fatalf("explicit user enable did not override false manifest default: %#v", projection)
	}
	projection = config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "profile", Value: "work", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"}}, declarations,
		config.ReadableLegacyPackState{Selected: true, UserEnabled: boolPointer(false), DefaultEnabled: boolPointer(false), WorkspaceEnabled: boolPointer(true)}, nil)
	if !*projection.EffectiveEnabled || len(projection.AllowSettings) != 1 {
		t.Fatalf("workspace true override did not outrank false user/default: %#v", projection)
	}
	projection = config.ProjectReadableLegacyPack(config.ReadableLegacyPackMapping{LegacyName: "aws-auth", PackHandle: "local/my-auth"}, []config.ReadableLegacySetting{{Key: "profile", Value: "work", DeclaredScope: loopholedecl.SettingScopeUser, OriginScope: "user"}}, declarations,
		config.ReadableLegacyPackState{Selected: true}, nil)
	if projection.EffectiveEnabled == nil || *projection.EffectiveEnabled || len(projection.AllowSettings) != 0 || len(projection.Differences) == 0 {
		t.Fatalf("absent legacy default was activated: %#v", projection)
	}
}

func TestReadableLegacyGrantExpansionIsBoundedByConcreteScope(t *testing.T) {
	legacy := []brokerscope.Grant{{Kind: "read", Repo: brokerscope.AnyRepo}}
	targets := []config.ReadableRepository{{Source: "github", RemoteHost: "github.com", Repository: "acme/one"}, {Source: "github", RemoteHost: "github.com", Repository: "acme/two"}}
	projection := config.ExpandReadableLegacyGrants("github", legacy, targets)
	if len(projection.Concrete) != 2 || len(projection.Differences) != 0 || !projection.ScopeFreeRead {
		t.Fatalf("bounded expansion=%#v", projection)
	}
	empty := config.ExpandReadableLegacyGrants("github", legacy, nil)
	if !empty.ScopeFreeRead || len(empty.Concrete) != 0 || len(empty.Raw) != 1 || len(empty.Differences) != 1 || !strings.Contains(empty.Differences[0], "scope-free reads") {
		t.Fatalf("empty-scope scope-free legacy read difference was erased: %#v", empty)
	}
	outside := config.ExpandReadableLegacyGrants("github", legacy, targets[:1])
	if len(outside.Concrete) != 1 || len(outside.Raw) != 1 {
		t.Fatalf("scope-bounded raw projection=%#v", outside)
	}
}

func TestReadableLegacyGrantDifferenceReportsNarrowScopedBranch(t *testing.T) {
	legacy := []brokerscope.Grant{{Kind: "branch", Repo: "github.com/acme/one", Branch: "main"}}
	canonical := []brokerscope.Grant{{Kind: "branch", Repo: "github.com/acme/one", Branch: "main"}, {Kind: "branch", Repo: "github.com/acme/two", Branch: "main"}}
	projection := config.CompareReadableLegacyGrants("git", legacy, canonical)
	if len(projection.Differences) != 1 || !strings.Contains(projection.Differences[0], "github.com/acme/two") {
		t.Fatalf("legacy mapping difference=%#v", projection)
	}
}

func TestReadableOnRequestReadRequiresTrueRequestAndConcreteTarget(t *testing.T) {
	declarations := readableBrokerDeclarations()
	for _, tc := range []struct {
		name, workspace string
		wantGrant       bool
	}{
		{"true plus named target", `{"requests":{"github":{"read":true}},"repos":{"private":{"repo":"github.com/acme/private"}}}`, true},
		{"false read request", `{"requests":{"github":{"read":false}},"repos":{"private":{"repo":"github.com/acme/private"}}}`, false},
		{"true without target", `{"requests":{"github":{"read":true}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"github":{"on_request":["read"]}}}`), Workspace: readableMap(t, tc.workspace), SelectedPacks: []string{"github"}, Declarations: declarations}
			plan, err := config.ResolveReadableAllowance(input)
			if err != nil {
				t.Fatal(err)
			}
			gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, source := range gate.Sources {
				for _, grant := range source.Grants {
					if grant.Kind == brokerscope.KindRead {
						got = true
					}
				}
			}
			if got != tc.wantGrant {
				t.Fatalf("read grant=%v want %v gate=%#v", got, tc.wantGrant, gate)
			}
			if tc.name == "true without target" {
				if len(gate.Sources) != 0 || len(gate.ReadCapabilities) != 1 || !gate.ReadCapabilities[0].OnRequest || !gate.ReadCapabilities[0].Requested || gate.ReadCapabilities[0].RequestStatus != config.ReadableRequestGranted {
					t.Fatalf("scope-free read capability metadata was erased or treated as a repository grant: %#v", gate)
				}
			}
		})
	}
}

func TestReadableMultipleBrokerDeclarationsPerPackMaterializeDeclaredGrantKinds(t *testing.T) {
	declarations := []config.ReadableBrokerDeclaration{
		{Pack: "shared", Label: "git-broker", Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "*", Grants: []string{"create-branch", "branch"}}, ReadCapability: "fetch", KindCapabilities: map[string]string{"create-branch": "new_branches", "branch": "push"}},
		{Pack: "shared", Label: "deployment-broker", Brokered: loopholedecl.Brokered{Source: "deployment", RemoteHost: "github.com", Grants: []string{"deploy"}}, ReadCapability: "read", KindCapabilities: map[string]string{"deploy": "deploy"}},
	}
	ws := t.TempDir()
	writeReadableGitConfig(t, ws, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n")
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch","new_branches"]},"deployment":{"always":["read","deploy"]}}}`),
		Workspace: readableMap(t, `{"requests":{"deployment":{"deploy":true}}}`), SelectedPacks: []string{"shared"}, Declarations: declarations})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 2 || gate.Sources[0].Source != "git" || gate.Sources[1].Source != "deployment" {
		t.Fatalf("broker declaration order/identity, plan=%#v gate=%#v", plan.Sources, gate.Sources)
	}
	if !containsReadableGrant(gate.Sources[1].Grants, brokerscope.Grant{Kind: "deploy", Repo: "github.com/acme/app"}) {
		t.Fatalf("declared repo-only grant kind was dropped: %#v", gate.Sources[1])
	}

	ambiguous := append([]config.ReadableBrokerDeclaration(nil), declarations...)
	ambiguous[0].Settings = []loopholedecl.Setting{{Key: "profile", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser}}
	ambiguous[1].Settings = []loopholedecl.Setting{{Key: "profile", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser}}
	_, err = config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"shared":{"profile":"x"}}}`), Workspace: readableMap(t, `{}`), SelectedPacks: []string{"shared"}, Declarations: ambiguous})
	if err == nil || !strings.Contains(err.Error(), "ambiguous setting declarations") {
		t.Fatalf("same-key pack setting collision error = %v", err)
	}
}

func TestReadableBranchOperandShapeComesFromDeclaredGrantKind(t *testing.T) {
	declaration := config.ReadableBrokerDeclaration{Pack: "scm", Label: "scm-broker", Brokered: loopholedecl.Brokered{Source: "scm", RemoteHost: "*", Grants: []string{"branch"}}, ReadCapability: "fetch", KindCapabilities: map[string]string{"branch": "publish"}}
	ws := t.TempDir()
	writeReadableGitConfig(t, ws, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n")
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"scm":{"always":["fetch"],"on_request":["publish"]}}}`), Workspace: readableMap(t, `{"requests":{"scm":{"publish":["release"]}}}`), SelectedPacks: []string{"scm"}, Declarations: []config.ReadableBrokerDeclaration{declaration}})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 1 {
		t.Fatalf("branch request produced no source: %#v", gate)
	}
	if !containsReadableGrant(gate.Sources[0].Grants, brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/app", Branch: "release"}) {
		t.Fatalf("declared branch-shaped capability did not materialize: %#v", gate.Sources)
	}
}

func TestReadableUnselectedSourceCollisionDoesNotShadowSelectedDeclaration(t *testing.T) {
	selected := readableBrokerDeclarations()[0]
	unselected := selected
	unselected.Pack = "other"
	unselected.Label = "other-git-broker"
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"]}}}`), Workspace: readableMap(t, `{}`), SelectedPacks: []string{"git"}, Declarations: []config.ReadableBrokerDeclaration{unselected, selected}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Sources) != 1 || plan.Sources[0].Pack != "git" {
		t.Fatalf("unselected declaration shadowed selected source: %#v", plan.Sources)
	}
}

func TestReadableObservedSourceRetainsFailedAndExcludedScopeWithoutGrant(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{"symlink config problem", func(t *testing.T, ws string) {
			writeReadableGitConfig(t, ws, "")
			config := filepath.Join(ws, ".git", "config")
			if err := os.Remove(config); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(outside, []byte(`[remote "origin"]\nurl=https://github.com/acme/app.git\n`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, config); err != nil {
				t.Fatal(err)
			}
		}},
		{"excluded credential remote", func(t *testing.T, ws string) {
			writeReadableGitConfig(t, ws, "[remote \"origin\"]\n\turl = https://token@github.com/acme/app.git\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := t.TempDir()
			tc.setup(t, ws)
			plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"]}}}`), Workspace: readableMap(t, `{}`), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()})
			if err != nil {
				t.Fatal(err)
			}
			gate, err := config.ProduceReadableGateInput(ws, plan)
			if err != nil {
				t.Fatal(err)
			}
			if len(gate.Sources) != 0 || len(gate.ObservedSources) != 1 {
				t.Fatalf("problem/exclusion became authority or was lost: %#v", gate)
			}
			observed := gate.ObservedSources[0]
			if observed.Source != "git" || observed.RemoteHost != "*" {
				t.Fatalf("selected-source identity lost: %#v", observed)
			}
			if tc.name == "symlink config problem" && observed.Read.Problem == "" {
				t.Fatal("read Problem was lost")
			}
			if tc.name == "excluded credential remote" && len(observed.Read.Excluded) == 0 {
				t.Fatal("excluded-URL diagnostics were lost")
			}
		})
	}
}

func TestReadableSelectedPackWithoutSettingsOnlyProducesNudge(t *testing.T) {
	input := config.ReadableAllowanceInput{User: readableMap(t, `{}`), Workspace: readableMap(t, `{}`), SelectedPacks: []string{"auth"}, PackSettings: []config.ReadablePackDeclaration{{Pack: "auth", Settings: []loopholedecl.Setting{{Key: "token", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser}}}}}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.PackSettings) != 0 || len(plan.Diagnostics) != 1 || !strings.Contains(plan.Diagnostics[0], "no allow.auth settings") {
		t.Fatalf("missing-setting nudge served a default or was absent: %#v", plan)
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.PackSettings) != 0 || len(gate.Sources) != 0 {
		t.Fatalf("default credential setting reached the gate: %#v", gate)
	}
}

func TestReadableTableOffAndEmptyListRemainDistinctWithoutActivation(t *testing.T) {
	declarations := []config.ReadablePackDeclaration{{Pack: "auth", Settings: []loopholedecl.Setting{{Key: "profile", Type: loopholedecl.SettingTypeString, Scope: loopholedecl.SettingScopeUser}, {Key: "visible", Type: loopholedecl.SettingTypeStringList, Scope: loopholedecl.SettingScopeWorkspace}, {Key: "count", Type: loopholedecl.SettingTypeInt, Scope: loopholedecl.SettingScopeUser}}}}
	input := config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"auth":"off"}}`), Workspace: readableMap(t, `{"requests":{"auth":"off"}}`), SelectedPacks: []string{"auth"}, PackSettings: declarations}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.TableTombstones) != 2 || plan.TableTombstones[0].Path != "allow.auth" || plan.TableTombstones[1].Path != "requests.auth" || len(plan.PackSettings) != 0 || len(plan.Sources) != 0 {
		t.Fatalf("table-off changed selection/settings or lost removal metadata: %#v", plan)
	}
	tombstonePlan := plan
	tombstoneGate, err := config.ProduceReadableGateInput(t.TempDir(), tombstonePlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombstoneGate.TableTombstones) != 2 || len(tombstoneGate.PackSettings) != 0 || len(tombstoneGate.Sources) != 0 {
		t.Fatalf("gate lost removal metadata or served tombstones: %#v", tombstoneGate)
	}
	input.User = readableMap(t, `{"allow":{"auth":{"profile":"off","visible":[""],"count":9223372036854775807}}}`)
	input.Workspace = readableMap(t, `{}`)
	plan, err = config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	settings := plan.PackSettings["auth"]
	if settings["profile"] != "off" || !reflect.DeepEqual(settings["visible"], []string{""}) || settings["count"] != int(^uint(0)>>1) {
		t.Fatalf("literal off, empty string list item, or integer normalization changed: %#v", settings)
	}
	input.User = readableMap(t, `{"allow":{"auth":{"count":1.0}}}`)
	if _, err := config.ResolveReadableAllowance(input); err == nil {
		t.Fatal("floating-point setting was accepted as an integer")
	}
}

func TestReadableAnyHostUsesGitIdentityPathGrammar(t *testing.T) {
	ws := t.TempDir()
	writeReadableGitConfig(t, ws, `[remote "nested"]
 url = https://git.example.com/owners/team/repository.git
[remote "single"]
 url = ssh://git@git.example.com/standalone.git
`)
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User: readableMap(t, `{"allow":{"git":{"always":["fetch"]}}}`), Workspace: readableMap(t, `{}`),
		SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations(),
	})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 1 {
		t.Fatalf("valid any-host remotes disappeared: %#v", gate)
	}
	want := []config.ReadableRepository{
		{Source: "git", RemoteHost: "git.example.com", Repository: "owners/team/repository", Origin: "remote nested"},
		{Source: "git", RemoteHost: "git.example.com", Repository: "standalone", Origin: "remote single"},
	}
	for _, target := range want {
		if !containsReadableTarget(gate.Sources[0].Targets, target) {
			t.Errorf("Git identity target %#v missing from %#v", target, gate.Sources[0].Targets)
		}
		if !containsReadableGrant(gate.Sources[0].Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: target.RemoteHost + "/" + target.Repository}) {
			t.Errorf("Git identity read grant for %#v missing from %#v", target, gate.Sources[0].Grants)
		}
	}
	invalidNamed, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User: readableMap(t, `{"allow":{"github":{"always":["read"]}}}`), Workspace: readableMap(t, `{"repos":{"team":{"repo":"github.com/acme/team/repository"}}}`),
		SelectedPacks: []string{"github"}, Declarations: readableBrokerDeclarations(),
	})
	if err == nil || !strings.Contains(err.Error(), "repos.team.repo") || !strings.Contains(err.Error(), "OWNER/REPO") {
		t.Fatalf("named forge addition bypassed OWNER/REPO validation: plan=%#v err=%v", invalidNamed, err)
	}
}

func TestReadableAdditionalNamedHostScopeDoesNotDependOnItsReadAllowance(t *testing.T) {
	input := config.ReadableAllowanceInput{
		User:             readableMap(t, `{"allow":{"git":{"always":["fetch"]}}}`),
		Workspace:        readableMap(t, `{"repos":{"private":{"repo":"github.com/acme/private"}}}`),
		WorkspaceOrigins: map[string][]string{"repos.private.repo": {"project.toml"}},
		SelectedPacks:    []string{"git", "github"}, Declarations: readableBrokerDeclarations(),
	}
	plan, err := config.ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	calls := 0
	var observed config.ReadableGateInput
	err = config.WithReadableGateInput(ws, plan, func(got config.ReadableGateInput) error { calls++; observed = got; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("producer called gate spy %d times", calls)
	}
	if len(observed.Requests) != 0 {
		t.Fatalf("a named repository is a scope identity, not a request: %#v", observed.Requests)
	}
	var git, github *config.ReadableGateSource
	for i := range observed.Sources {
		switch observed.Sources[i].Source {
		case "git":
			git = &observed.Sources[i]
		case "github":
			github = &observed.Sources[i]
		}
	}
	if git == nil || !containsReadableGrant(git.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/acme/private"}) || len(git.Read.Remotes) != 0 {
		t.Fatalf("Git's independent fetch ceiling did not receive only the named identity: %#v", observed.Sources)
	}
	if github != nil {
		t.Fatalf("scope request accidentally became GitHub read authority: %#v", *github)
	}
	if !containsReadableTarget(git.Targets, config.ReadableRepository{Source: "git", RemoteHost: "github.com", Repository: "acme/private", Origin: "repos.private"}) {
		t.Fatalf("Git's typed scope target lost declared source or origin: %#v", git.Targets)
	}
}

func TestReadableSingleLabelLegacyGrantExpansionReportsUnsupportedSerialization(t *testing.T) {
	target := config.ReadableRepository{Source: "git", RemoteHost: "gitserver", Repository: "o/r", Origin: "remote origin"}
	raw := []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}}
	projection := config.ExpandReadableLegacyGrants("git", raw, []config.ReadableRepository{target})
	targetsField := reflect.ValueOf(projection).FieldByName("Targets")
	if !targetsField.IsValid() || !reflect.DeepEqual(targetsField.Interface(), []config.ReadableRepository{target}) || !reflect.DeepEqual(projection.Raw, raw) {
		t.Fatalf("typed target/raw legacy state not retained: %#v", projection)
	}
	if len(projection.Concrete) != 0 || len(projection.Differences) == 0 {
		t.Fatalf("unserializable concrete grant was dropped without a migration difference: %#v", projection)
	}
	joined := strings.Join(projection.Differences, "\n")
	for _, want := range []string{"gitserver/o/r", "cannot be serialized", "write nothing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("difference %q does not name %q", joined, want)
		}
	}

	ws := t.TempDir()
	writeReadableGitConfig(t, ws, `[remote "origin"]
 url = gitserver:o/r.git
`)
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, `{"allow":{"git":{"always":["fetch"]}}}`), Workspace: readableMap(t, `{}`), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(ws, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 0 || len(gate.Differences) == 0 || len(gate.ObservedSources) != 1 || len(gate.ObservedSources[0].Targets) != 1 || gate.ObservedSources[0].Targets[0].RemoteHost != "gitserver" {
		t.Fatalf("unsupported serialization must retain typed scope and write nothing: %#v", gate)
	}
	for _, source := range gate.Sources {
		for _, grant := range source.Grants {
			if grant.Repo == "gitserver/o/r" {
				t.Fatalf("invalid single-label grant escaped: %#v", source)
			}
		}
	}
}

func TestReadableConcreteScopeEmptyMatrixUsesActualGateInput(t *testing.T) {
	for _, broker := range []string{"git", "github"} {
		for _, workspaceKind := range []string{"non-Git", "Git without remotes"} {
			t.Run(broker+"/"+workspaceKind, func(t *testing.T) {
				ws := t.TempDir()
				if workspaceKind == "Git without remotes" {
					writeReadableGitConfig(t, ws, "")
				}
				plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
					User:      readableMap(t, `{"allow":{"git":{"always":["fetch"]},"github":{"always":["read"]}}}`),
					Workspace: readableMap(t, `{}`), SelectedPacks: []string{broker}, Declarations: readableBrokerDeclarations(),
				})
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				var gate config.ReadableGateInput
				if err := config.WithReadableGateInput(ws, plan, func(got config.ReadableGateInput) error { calls++; gate = got; return nil }); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || len(gate.Sources) != 0 || len(gate.AdditionalRepositories) != 0 || len(gate.Requests) != 0 {
					t.Fatalf("empty scope created a prompt/grant/phantom source: calls=%d gate=%#v", calls, gate)
				}
				if len(gate.ReadCapabilities) != 1 || gate.ReadCapabilities[0].Source != broker || !gate.ReadCapabilities[0].Always {
					t.Fatalf("read ceiling metadata was not observed independently of scope: %#v", gate.ReadCapabilities)
				}
				if len(gate.ObservedSources) != 1 || gate.ObservedSources[0].Source != broker || len(gate.ObservedSources[0].Targets) != 0 || len(gate.ObservedSources[0].Read.Remotes) != 0 {
					t.Fatalf("actual empty source observation was lost or inherited: %#v", gate.ObservedSources)
				}
			})
		}
	}
}

func TestReadablePresentNullAllowanceTablesAreMalformed(t *testing.T) {
	for _, tc := range []struct{ name, user, workspace, want string }{
		{"user allow null", `{"allow":null}`, `{}`, "allow: expected a table"},
		{"workspace requests null", `{}`, `{"requests":null}`, "requests: expected a table"},
		{"nested user broker null", `{"allow":{"git":null}}`, `{}`, "allow.git"},
		{"nested workspace broker null", `{}`, `{"requests":{"git":null}}`, "requests.git"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{User: readableMap(t, tc.user), Workspace: readableMap(t, tc.workspace), SelectedPacks: []string{"git"}, Declarations: readableBrokerDeclarations()})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("null was treated as absence/tombstone: err=%v want containing %q", err, tc.want)
			}
		})
	}
}

func TestReadableLegacyScopeFreePrerequisiteMatchesRealGitHubBroker(t *testing.T) {
	legacy := []brokerscope.Grant{{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}}
	projection := config.ExpandReadableLegacyGrants("github", legacy, nil)
	if !projection.ScopeFreeRead || len(projection.Differences) == 0 {
		t.Fatalf("empty legacy read scope lacks explicit L zero-write difference: %#v", projection)
	}
	plan, err := config.ResolveReadableAllowance(config.ReadableAllowanceInput{
		User: readableMap(t, `{"allow":{"github":{"always":["read"]}}}`), Workspace: readableMap(t, `{}`),
		SelectedPacks: []string{"github"}, Declarations: readableBrokerDeclarations(),
	})
	if err != nil {
		t.Fatal(err)
	}
	gate, err := config.ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.Sources) != 0 || len(gate.ReadCapabilities) != 1 || !gate.ReadCapabilities[0].Always {
		t.Fatalf("canonical empty scope must keep capability metadata, not repo authority: %#v", gate)
	}
	for _, tc := range []struct {
		name   string
		grants []brokerscope.Grant
		wantOK bool
	}{
		{"legacy read star", legacy, true},
		{"no read", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("YOLO_USER_LAYER", "")
			workspace := filepath.Join(home, "workspace")
			if err := os.MkdirAll(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			sf := brokerscope.File{Source: ghbroker.Source, LaunchID: "readable-fixture", PID: os.Getpid(), Container: "readable-container", Workspace: workspace,
				Repos: []string{}, Grants: append([]brokerscope.Grant(nil), tc.grants...)}
			path, err := brokerscope.Write(sf)
			if err != nil {
				t.Fatal(err)
			}
			authority, err := ghbroker.BindAuthority(path, sf, "readable-start")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = authority.Close() })
			_, _, err = authority.Classify([]string{"repo", "gitignore", "list"}, "")
			if (err == nil) != tc.wantOK {
				t.Fatalf("actual GitHub broker read-prerequisite err=%v, want success=%v", err, tc.wantOK)
			}
			if err != nil && !strings.Contains(err.Error(), "holds no read grant") {
				t.Fatalf("no-read broker refusal lost its read-prerequisite distinction: %v", err)
			}
		})
	}
}

func writeReadableGitConfig(t *testing.T, ws, config string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func boolPointer(value bool) *bool { return &value }

func containsReadableTarget(targets []config.ReadableRepository, want config.ReadableRepository) bool {
	for _, target := range targets {
		if target.Source == want.Source && target.RemoteHost == want.RemoteHost && target.Repository == want.Repository && target.Origin == want.Origin && reflect.DeepEqual(target.Files, want.Files) {
			return true
		}
	}
	return false
}

func containsReadableGrant(grants []brokerscope.Grant, want brokerscope.Grant) bool {
	return containsValue(grants, want)
}
func containsReadableEntry(entries []config.EntryRepo, repo, origin string) bool {
	for _, entry := range entries {
		if strings.EqualFold(entry.Repo, repo) && containsValue(entry.Files, origin) {
			return true
		}
	}
	return false
}
func containsValue[T comparable](values []T, want T) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
