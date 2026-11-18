package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This fixture loads authored TOML through the public reader and gate reader, and
// projects actual selected pack declarations. No classifier or program is run.
func scopedPolicyFixture(t *testing.T, user, project string) (ReadableAllowanceInput, string) {
	t.Helper()
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_IN_JAIL", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0700); err != nil {
		t.Fatal(err)
	}
	userPath := filepath.Join(userDir, "config.toml")
	if err := os.WriteFile(userPath, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReadableConfig(workspace, true, func(string) {}); err != nil {
		t.Fatal(err)
	}
	u, sources, err := LoadReadableUserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	w, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	packs, complete := resolveSelectedPacks()
	if !complete {
		t.Fatal("selected declaration coverage incomplete")
	}
	t.Cleanup(packload.ReleaseEmbedded)
	declarations, settings, err := ReadableDeclarationsForPacks(packs)
	if err != nil {
		t.Fatal(err)
	}
	var selected []string
	for _, p := range packs {
		selected = append(selected, p.Name)
	}
	return ReadableAllowanceInput{User: u, Workspace: w.Config, SelectedPacks: selected, Declarations: declarations, PackSettings: settings, UserOrigins: readableUserOrigins(u, sources), WorkspaceOrigins: readableWorkspaceOrigins(w.Config, w.sources)}, userPath
}

func TestScopedBrokerPolicyPublicTOMLAndProvenance(t *testing.T) {
	input, userPath := scopedPolicyFixture(t, `packs = ["aws"]
# user owns the ceiling
[allow.aws]
on_request = ["read", "all.put_objects"]
[scopes.reports]
source = "aws"
resource = "s3://b/reports/"
[scopes.reports.allow]
always = ["read"]
never = ["all", "working_set"]
`, `[call_roots]
aws = ["s3://b"]
[scopes.team]
source = "aws"
resource = "s3://b/team/"
[scopes.team.requests]
read = "ask"
[scopes.team.requests.working_set]
put_objects = true
[scopes.team.requests.all]
put_objects = true
`)
	selected := false
	for _, decl := range input.Declarations {
		if decl.CallBroker != nil && decl.CallBroker.Source == "aws" && len(decl.CallBroker.Cells) == 9 {
			selected = true
		}
	}
	if !selected {
		t.Fatal("selected AWS declaration missing")
	}
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Scopes) != 2 {
		t.Fatalf("scopes=%+v", plan.Scopes)
	}
	ceiling, ask := plan.Scopes[0], plan.Scopes[1]
	if ceiling.Layer != "ceiling" || ceiling.Resource != "s3://b/reports/" || !reflect.DeepEqual(ceiling.Never, []string{"all", "working_set"}) || ask.Reads[""] != "ask" || ask.Tables["all"]["put_objects"] != "true" {
		t.Fatalf("canonical scopes=%+v", plan.Scopes)
	}
	if len(ceiling.Origins["allow.never"]) == 0 || !strings.Contains(strings.Join(ceiling.Origins["allow.never"], ","), "config.toml") {
		t.Fatalf("user writer lost: %v (%s)", ceiling.Origins, userPath)
	}
	if !strings.Contains(strings.Join(ask.Origins["requests.all.put_objects"], ","), WorkspaceTOMLConfigName) {
		t.Fatalf("ask writer lost: %v", ask.Origins)
	}
}

func TestScopedBrokerPolicyValidation(t *testing.T) {
	for _, tc := range []struct{ name, user, project, want string }{
		{"B7-duplicate-resource", `packs=["aws"]
[scopes.one]
source="aws"
resource="s3://b"
[scopes.two]
source="aws"
resource="s3://b"
`, "", "same source/resource"},
		{"B7-exact-never-conflict", `packs=["aws"]
[allow.aws]
always=["all.put_objects"]
never=["all.put_objects"]
`, "", "both hold"},
		{"B4c-effective-order", `packs=["aws"]
[allow.aws]
always=["all.put_objects"]
[scopes.one]
source="aws"
resource="s3://b"
[scopes.one.allow]
never=["working_set.put_objects"]
`, "", "working_set >= all"},
		{"unknown-never-token", `packs=["aws"]
[allow.aws]
never=["not_declared"]
`, "", "not_declared"},
		{"H27-implicit-specific-read", `packs=["aws"]
[allow.aws]
never=["read.list_objects"]
`, "", "read.list_objects"},
		{"native-scopes", `packs=["git"]
[scopes.one]
source="git"
resource="x"
`, "", "[repos.one]"},
		{"A2c-audio-never", `packs=["audio"]
[allow.audio]
never=["use"]
`, "", "never"},
		{"H23-native-alias", `packs=["git"]
[allow.git]
always=["fetch"]
on_request=["read"]
`, "", "same native read"},
		{"H24-native-cell", `packs=["git"]
[allow.git]
never=["read.fetch"]
`, "", "fetch"},
		{"inherited-ask-order", `packs=["aws"]
[allow.aws]
on_request=["all.put_objects"]
`, `[scopes.one]
source="aws"
resource="s3://b"
[scopes.one.requests.all]
put_objects=true
`, "working_set >= all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := scopedPolicyFixture(t, tc.user, tc.project)
			if tc.name == "H27-implicit-specific-read" {
				raw, err := os.ReadFile(filepath.Join("..", "loopholedecl", "testdata", "scoped-old-aws.toml"))
				if err != nil {
					t.Fatal(err)
				}
				old, err := loopholedecl.Decode(raw, "aws-broker")
				if err != nil {
					t.Fatal(err)
				}
				for i := range input.Declarations {
					if input.Declarations[i].CallBroker != nil && input.Declarations[i].CallBroker.Source == "aws" {
						input.Declarations[i].CallBroker = old.CallBroker
					}
				}
			}
			_, err := ResolveReadableAllowance(input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want=%q", err, tc.want)
			}
		})
	}
}

func TestFrozenBrokerListCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, user string }{
		{"A2a-aws", `packs=["aws"]
[allow.aws]
always=["read","all.put_objects"]
on_request=["read","all.put_objects"]
`},
		{"A2b-aws", `packs=["aws"]
[allow.aws]
always=["all"]
on_request=["all.put_objects"]
`},
		{"A2a-git", `packs=["git"]
[allow.git]
always=["fetch"]
on_request=["fetch"]
`},
		{"A2c-audio", `packs=["audio"]
[allow.audio]
always=["use"]
on_request=["use"]
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := scopedPolicyFixture(t, tc.user, "")
			if _, err := ResolveReadableAllowance(input); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tc.name, "aws") && CallBrokerCurrentCeiling(input.User, "aws", "all", "put_objects") != "always" {
				t.Fatal("old shipped resolver changed")
			}
		})
	}
}

func TestGitHubBuiltRights(t *testing.T) {
	for _, token := range []string{"read", "create_pr", "create_issue", "runs", "approve_runs", "all", "working_set", "all.merge_prs", "read.read"} {
		t.Run("C7m-"+token, func(t *testing.T) {
			input, _ := scopedPolicyFixture(t, "packs=[\"github\"]\n[allow.github]\nnever=[\""+token+"\"]\n", "")
			_, err := ResolveReadableAllowance(input)
			valid := token == "read" || token == "create_pr" || token == "create_issue" || token == "runs" || token == "approve_runs"
			if (err == nil) != valid {
				t.Fatalf("token=%s error=%v", token, err)
			}
		})
	}
}

func TestScopedCeilingNeverExtensionRetainsShippedResolver(t *testing.T) {
	for _, tc := range []struct{ raw, table, cell, want string }{
		{`{"always":["all"],"on_request":["all.put"]}`, "all", "put", "always"},
		{`{"always":["all.put"],"on_request":["all.put"]}`, "all", "put", "always"},
		{`{"never":["all"],"always":["all.put"]}`, "all", "put", "always"},
		{`{"never":["all"],"always":["all.put"]}`, "all", "delete", "never"},
		{`{"never":["all"]}`, "working_set", "put", "absent"},
	} {
		allow := testConfigMap(t, tc.raw)
		if got := ScopedBrokerCeilingValue(allow, tc.table, tc.cell); got != tc.want {
			t.Fatalf("%s => %s want=%s", tc.raw, got, tc.want)
		}
	}
}

func TestScopedDeclarationCloneAndDisclosure(t *testing.T) {
	b := &loopholedecl.CallBroker{Source: "fixture", Scope: loopholedecl.CallBrokerScope{Nesting: "segment", Separator: "/", Anchors: []string{"x://"}}, Context: &loopholedecl.CallBrokerContext{Fields: map[string]string{"profile": "SYNTHETIC_PROFILE"}, Unset: []string{"SYNTHETIC_TOKEN"}, RouteAnchors: []string{"x://"}}, Cells: []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read", MaxFor: "1h"}}}
	c := cloneReadableCallBroker(b)
	c.Scope.Anchors[0] = "changed"
	c.Context.Fields["profile"] = "OTHER"
	c.Context.Unset[0] = "OTHER"
	c.Context.RouteAnchors[0] = "changed"
	c.Cells[0].Kind = "write"
	if b.Scope.Anchors[0] != "x://" || b.Context.Fields["profile"] != "SYNTHETIC_PROFILE" || b.Context.Unset[0] != "SYNTHETIC_TOKEN" || b.Context.RouteAnchors[0] != "x://" || b.Cells[0].Kind != "read" {
		t.Fatal("clone aliases declaration metadata")
	}
	disclosure := strings.Join(CallBrokerDisclosure(b), "\n")
	for _, word := range []string{"segment", "x://", "profile", "SYNTHETIC_PROFILE", "SYNTHETIC_TOKEN", "kind=read"} {
		if !strings.Contains(disclosure, word) {
			t.Fatalf("undisclosed %s", word)
		}
	}
}

func TestDeclaredExecutionContextsConfig(t *testing.T) {
	root := t.TempDir()
	pack := selectedCallBrokerFixturePack(t, root, "", []string{"working_set", "all"})
	manifest := filepath.Join(pack.Root, "loopholes", "tasks", "loophole.toml")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "roots = \"declared\"", "roots = \"declared\"\nnesting = \"segment\"\nseparator = \"/\"\nanchors = [\"s3://\", \"ce://\"]", 1))
	body = append(body, []byte("\n[call_broker.context]\nunset=[\"SYNTHETIC_TOKEN\"]\nroute_anchors=[\"s3://\"]\n[call_broker.context.fields]\nprofile=\"SYNTHETIC_PROFILE\"\n\n[[call_broker.cell]]\nname=\"list\"\nkind=\"read\"\n")...)
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, value, resource, field, want string }{
		{"F14-spaces", "team prod", "s3://b/", "profile", ""},
		{"F14-unicode", "équipe-ro", "s3://b/", "profile", ""},
		{"F14-bound", strings.Repeat("a", 1024), "s3://b/", "profile", ""},
		{"F15-empty", "", "s3://b/", "profile", "nonempty"},
		{"F15-over", strings.Repeat("a", 1025), "s3://b/", "profile", "1024"},
		{"F15-control", "team\nprod", "s3://b/", "profile", "control"},
		{"F10-field", "team", "s3://b/", "undeclared", "undeclared"},
		{"F10-anchor", "team", "ce://host-login", "profile", "route_anchors"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := fmt.Sprintf("packs=[\"src/local-tasks\"]\n[pack_sources]\nsrc=%q\n[contexts.team]\nsource=\"local-tasks\"\n%s=%q\n[scopes.one]\nsource=\"local-tasks\"\nresource=%q\ncontext=\"team\"\n", root, tc.field, tc.value, tc.resource)
			input, _ := scopedPolicyFixture(t, user, "")
			plan, err := ResolveReadableAllowance(input)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v want=%s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Contexts) != 1 || plan.Contexts[0].Fields["profile"] != tc.value || plan.Scopes[0].Context != "team" {
				t.Fatalf("context bytes not retained: %+v", plan)
			}
		})
	}
}

func TestNativeSourceReadVocabularyIsInactiveMetadata(t *testing.T) {
	input, _ := scopedPolicyFixture(t, "packs=[\"git\"]\n[allow.git]\nalways=[\"read\"]\n", "[requests.git]\nread=true\n")
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.NativeReads) != 2 || !plan.NativeReads[0].Always || plan.NativeReads[1].Request != "true" {
		t.Fatalf("source alias metadata omitted: %+v", plan.NativeReads)
	}
	for _, source := range plan.Sources {
		if source.Source == "git" && len(source.Always) != 0 {
			t.Fatalf("additive source alias activated old grant projection: %+v", source)
		}
	}
	for _, request := range plan.Requests {
		if request.Path == "requests.git.read" || request.Path == "requests.git.fetch" {
			t.Fatal("additive source alias entered old request consumer")
		}
	}
	gate, err := ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range gate.ReadCapabilities {
		if read.Source == "git" && (read.Always || read.Requested) {
			t.Fatal("additive source alias activated gate read authority")
		}
	}
	old, _ := scopedPolicyFixture(t, "packs=[\"git\"]\n[allow.git]\nalways=[\"fetch\"]\n", "[requests.git]\nfetch=true\n")
	legacy, err := ResolveReadableAllowance(old)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range legacy.Sources {
		if source.Source == "git" && len(source.Always) != 1 {
			t.Fatal("shipped fetch projection changed")
		}
	}
}

func TestNativeNamedReadMetadataDoesNotAdmitRepository(t *testing.T) {
	input, _ := scopedPolicyFixture(t, `packs=["github"]
[allow.github]
always=["read"]
[repos.one]
repo="github.com/acme/private"
[repos.one.allow.github]
always=["read"]
never=["create_pr"]
`, "")
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.NativeCeilings) != 1 || len(plan.NativeReads) != 1 || len(plan.ScopedCeilings) != 0 {
		t.Fatalf("read metadata entered legacy projection: %+v", plan)
	}
	gate, err := ProduceReadableGateInput(t.TempDir(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(gate.AdditionalRepositories) != 0 || len(gate.ScopedCeilings) != 0 {
		t.Fatal("new native metadata admitted a repository")
	}
	for _, source := range gate.Sources {
		if len(source.Grants) != 0 || len(source.Targets) != 0 {
			t.Fatalf("new native read activated scope/grants: %+v", source)
		}
	}
}

func TestNativeReadCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, user, project, want string }{
		{"H22-git-named", `packs=["git"]
[repos.remote]
repo="github.com/acme/app"
[repos.remote.allow]
always=["fetch"]
on_request=["fetch"]
`, `[repos.remote.requests]
fetch="ask"
`, ""},
		{"H23-named-alias", `packs=["git"]
[repos.remote]
repo="github.com/acme/app"
[repos.remote.allow]
always=["fetch"]
never=["read"]
`, "", "same native read"},
		{"H23-named-ask-alias", `packs=["git"]`, `[repos.remote.requests]
fetch="ask"
read=false
`, "same native read"},
		{"B9-github-named", `packs=["github"]
[repos.origin]
repo="github.com/acme/app"
[repos.origin.allow.github]
always=["read"]
never=["create_pr"]
`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := scopedPolicyFixture(t, tc.user, tc.project)
			plan, err := ResolveReadableAllowance(input)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v want=%s", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.NativeReads) == 0 {
				t.Fatal("native scoped read metadata omitted")
			}
		})
	}
}

func TestScopedBrokerUnselectedScopeIsInert(t *testing.T) {
	input, _ := scopedPolicyFixture(t, `packs=[]
[scopes.other]
source="unselected"
resource="gs://bad\n"
[scopes.other.allow]
never=["not_declared"]
`, "")
	plan, err := ResolveReadableAllowance(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Scopes) != 1 || plan.Scopes[0].Selected || len(plan.Diagnostics) == 0 {
		t.Fatalf("not inert: %+v", plan)
	}
}

func TestScopedBrokerJSONCNewFormsRefused(t *testing.T) {
	for _, raw := range []string{
		`{"scopes":{"one":{"source":"aws","resource":"s3://b"}}}`,
		`{"contexts":{"one":{"source":"aws","profile":"synthetic"}}}`,
		`{"allow":{"aws":{"never":["read"]}}}`,
		`{"allow":{"aws":{"always":["read.list_objects"]}}}`,
		`{"requests":{"aws":{"read":"ask"}}}`,
		`{"requests":{"aws":{"read":{"list_objects":true}}}}`,
		`{"repos":{"one":{"repo":"github.com/acme/app","allow":{"github":{"never":["read"]}}}}}`,
		`{"repos":{"one":{"requests":{"github":{"read":true}}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			dir := filepath.Join(home, ".config", "yolo-jail")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			if strings.Contains(raw, `"requests"`) {
				if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("packs=[\"aws\",\"github\",\"git\"]\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(raw), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"packs":["aws","github","git"],`+raw[1:]), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := LoadReadableConfig(workspace, true, func(string) {})
			if err == nil {
				user, us, e := LoadReadableUserScopeConfigWithSources(true, func(string) {})
				if e != nil {
					err = e
				} else {
					ws, e := ReadWorkspaceForGate(workspace)
					if e != nil {
						err = e
					} else {
						packs, complete := resolveSelectedPacks()
						if !complete {
							t.Fatal("selected declarations incomplete")
						}
						t.Cleanup(packload.ReleaseEmbedded)
						declarations, settings, e := ReadableDeclarationsForPacks(packs)
						if e != nil {
							err = e
						} else {
							var selected []string
							for _, pack := range packs {
								selected = append(selected, pack.Name)
							}
							_, err = ResolveReadableAllowance(ReadableAllowanceInput{User: user, Workspace: ws.Config, UserOrigins: readableUserOrigins(user, us), WorkspaceOrigins: readableWorkspaceOrigins(ws.Config, ws.sources), Declarations: declarations, PackSettings: settings, SelectedPacks: selected})
						}
					}
				}
			}
			if err == nil || (!strings.Contains(err.Error(), "TOML-only") && !strings.Contains(raw, "read.list_objects")) {
				t.Fatalf("new JSONC form accepted: %v", err)
			}
		})
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"allow":{"github":{"always":["read"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReadableUserScopeConfigWithSources(true, func(string) {}); err != nil {
		t.Fatalf("old JSONC broad grant refused: %v", err)
	}
}

func TestScopedPolicySourceGrantOwnership(t *testing.T) {
	b := &loopholedecl.CallBroker{Source: "fixture", Cells: []loopholedecl.CallBrokerCell{{Name: "write", Tables: []string{"all"}}}}
	input := ReadableAllowanceInput{User: testConfigMap(t, `{"allow":{"fixture":{"never":["read"]}}}`), Workspace: jsonx.NewOrderedMap(), SelectedPacks: []string{"fixture"}, Declarations: []ReadableBrokerDeclaration{readableCallBrokerDeclaration("fixture", "op", b, nil)}, PackSettings: []ReadablePackDeclaration{{Pack: "fixture", Loophole: "other", Grants: []loopholedecl.Grant{{Kind: "read"}}}}}
	if _, err := ResolveReadableAllowance(input); err == nil {
		t.Fatal("another module's read grant created implicit read")
	}
}
