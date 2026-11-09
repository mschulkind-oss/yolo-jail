package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestRunConfigEditAutomaticBothScopeDefaultsToProject(t *testing.T) {
	for _, tc := range []struct {
		name, user, project, local       string
		format                           configdoc.Format
		userName, projectName, localName string
	}{
		{
			name: "TOML", format: configdoc.TOML,
			userName: paths.UserConfigTOMLName, projectName: paths.WorkspaceConfigTOMLName, localName: paths.WorkspaceLocalConfigTOMLName,
			user: "[machine.resources]\ncpus = 1\n", project: "[machine.resources]\ncpus = 2\n", local: "[machine.resources]\ncpus = 3\n",
		},
		{
			name: "JSONC", format: configdoc.JSONC,
			userName: paths.UserConfigLegacyJSONCName, projectName: paths.WorkspaceConfigLegacyJSONCName, localName: paths.WorkspaceLocalConfigLegacyJSONCName,
			user: `{"machine":{"resources":{"cpus":1}}}`, project: `{"machine":{"resources":{"cpus":2}}}`, local: `{"machine":{"resources":{"cpus":3}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testconfig.WorkspaceFormat(t, tc.format, tc.project)
			user := testconfig.UserFormat(t, tc.format, tc.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(project.Root, tc.localName)
			if err := os.WriteFile(localPath, []byte(tc.local), 0o600); err != nil {
				t.Fatal(err)
			}
			userBefore, _ := os.ReadFile(user.Path)
			rc, out, errw := runConfigEditCapture(t, project.Root, []string{"set", "machine.resources.cpus", "4"})
			if rc != 0 || errw != "" || !strings.Contains(out, project.Path) {
				t.Fatalf("automatic set rc=%d stdout=%q stderr=%q; want Project %s", rc, out, errw, project.Path)
			}
			if after, _ := os.ReadFile(user.Path); !bytes.Equal(after, userBefore) {
				t.Fatalf("automatic Both-scope edit changed winning User bytes: %q", after)
			}
			if !strings.Contains(out, "Written. Run `yolo check`") {
				t.Fatalf("automatic Project edit omitted owed post-write steps: %q", out)
			}
		})
	}
}

func TestRunConfigEditCanWriteUserScopeOnHost(t *testing.T) {
	project := testconfig.Workspace(t, "machine = {}\n")
	user := testconfig.User(t, "packs = []\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	projectBefore, _ := os.ReadFile(project.Path)
	rc, out, errw := runConfigEditCapture(t, project.Root,
		[]string{"add", "packs", `"claude"`, "--user"})
	if rc != 0 || errw != "" || !strings.Contains(out, user.Path) {
		t.Fatalf("host User edit rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if after, _ := os.ReadFile(user.Path); !bytes.Contains(after, []byte(`"claude"`)) {
		t.Fatalf("host User edit did not append the pack: %q", after)
	}
	if after, _ := os.ReadFile(project.Path); !bytes.Equal(after, projectBefore) {
		t.Fatalf("host User edit changed workspace bytes: %q", after)
	}
}

func TestRunConfigEditSetAndAddUseResolvedTargetsInBothFormats(t *testing.T) {
	for _, tc := range []struct {
		name, project, local   string
		format                 configdoc.Format
		projectPath            string
		wantProject, wantLocal string
	}{
		{
			name: "TOML", format: configdoc.TOML,
			project:     "[machine.resources]\ncpus = 2 # project comment\n\n[env]\nunset = [\"OLD\"]\n",
			local:       "[machine.resources]\ncpus = 3 # local comment\n\n[env]\nunset = [\"LOCAL\"]\n",
			projectPath: "machine.resources.cpus",
			wantProject: "cpus = 4 # project comment", wantLocal: "cpus = 5 # local comment",
		},
		{
			name: "legacy JSONC", format: configdoc.JSONC,
			project:     `{"machine":{"resources":{"cpus":2}},"env":{"unset":["OLD"]}}`,
			local:       `{"machine":{"resources":{"cpus":3}},"env":{"unset":["LOCAL"]}}`,
			projectPath: "machine.resources.cpus",
			wantProject: `"cpus":4`, wantLocal: `"cpus":5`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testconfig.WorkspaceFormat(t, tc.format, tc.project)
			userText := ""
			if tc.format == configdoc.JSONC {
				userText = "{}"
			}
			user := testconfig.UserFormat(t, tc.format, userText)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			localPath := filepath.Join(project.Root, paths.WorkspaceLocalConfigTOMLName)
			if tc.format == configdoc.JSONC {
				localPath = filepath.Join(project.Root, paths.WorkspaceLocalConfigLegacyJSONCName)
			}
			if err := os.WriteFile(localPath, []byte(tc.local), 0o600); err != nil {
				t.Fatal(err)
			}
			projectBefore, err := os.ReadFile(project.Path)
			if err != nil {
				t.Fatal(err)
			}

			rc, out, errw := runConfigEditCapture(t, project.Root,
				[]string{"set", tc.projectPath, "4", "--project"})
			if rc != 0 || errw != "" || !strings.Contains(out, "Written. Run `yolo check`") {
				t.Fatalf("set rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			gotProject, err := os.ReadFile(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(gotProject, []byte(tc.wantProject)) {
				t.Fatalf("project edit = %q, want %q", gotProject, tc.wantProject)
			}
			if bytes.Equal(gotProject, projectBefore) {
				t.Fatal("set did not write the resolved Project target")
			}

			rc, out, errw = runConfigEditCapture(t, project.Root,
				[]string{"set", tc.projectPath, "5", "--local"})
			if rc != 0 || errw != "" || !strings.Contains(out, localPath) {
				t.Fatalf("local set rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			gotLocal, err := os.ReadFile(localPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(gotLocal, []byte(tc.wantLocal)) {
				t.Fatalf("local edit = %q, want %q", gotLocal, tc.wantLocal)
			}
			if got, _ := os.ReadFile(project.Path); !bytes.Equal(got, gotProject) {
				t.Fatalf("local edit changed Project bytes: %q", got)
			}

			rc, out, errw = runConfigEditCapture(t, project.Root,
				[]string{"add", "env.unset", `"NEW"`, "--project"})
			if rc != 0 || errw != "" {
				t.Fatalf("add rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			gotProject, err = os.ReadFile(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.format == configdoc.TOML && !bytes.Contains(gotProject, []byte(`unset = ["OLD", "NEW"]`)) {
				t.Fatalf("TOML add did not append to the selected list: %q", gotProject)
			}
			if tc.format == configdoc.JSONC && (!bytes.Contains(gotProject, []byte(`"OLD"`)) || !bytes.Contains(gotProject, []byte(`"NEW"`))) {
				t.Fatalf("JSONC add did not append to the selected list: %q", gotProject)
			}

			rc, out, errw = runConfigEditCapture(t, project.Root,
				[]string{"add", "env.unset", `"NEW"`, "--project"})
			if rc != 0 || errw != "" || !strings.Contains(out, "No change; no write.") {
				t.Fatalf("duplicate add rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			if after, _ := os.ReadFile(project.Path); !bytes.Equal(after, gotProject) {
				t.Fatalf("duplicate add changed bytes: before=%q after=%q", gotProject, after)
			}
			beforeDryRun, _ := os.ReadFile(project.Path)
			rc, out, errw = runConfigEditCapture(t, project.Root,
				[]string{"set", tc.projectPath, "6", "--project", "--dry-run"})
			if rc != 0 || errw != "" || !strings.Contains(out, "Dry run; nothing was written.") {
				t.Fatalf("dry-run rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			if after, _ := os.ReadFile(project.Path); !bytes.Equal(after, beforeDryRun) {
				t.Fatalf("dry-run changed bytes: before=%q after=%q", beforeDryRun, after)
			}
		})
	}
}

func TestRunConfigEditUnsetAbsentAndInvalidEditDoNotWrite(t *testing.T) {
	project := testconfig.Workspace(t, "[machine.resources]\ncpus = 2\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	localPath := filepath.Join(project.Root, paths.WorkspaceLocalConfigTOMLName)
	before := []byte("[machine.resources]\ncpus = 3\n\n[machine.gpu]\nenabled = false\n")
	if err := os.WriteFile(localPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runConfigEditCapture(t, project.Root, []string{"unset", "mcp_servers.tavily", "--local"})
	if rc != 0 || errw != "" || !strings.Contains(out, "No change; no write.") {
		t.Fatalf("absent unset rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if got, _ := os.ReadFile(localPath); !bytes.Equal(got, before) {
		t.Fatalf("absent unset changed bytes: %q", got)
	}

	rc, out, errw = runConfigEditCapture(t, project.Root,
		[]string{"set", "machine.resources.cpus", `"not-a-number"`, "--local"})
	if rc == 0 || out != "" || !strings.Contains(errw, "config edit refused:") {
		t.Fatalf("invalid set rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if got, _ := os.ReadFile(localPath); !bytes.Equal(got, before) {
		t.Fatalf("refused set changed bytes: %q", got)
	}

	rc, out, errw = runConfigEditCapture(t, project.Root,
		[]string{"set", "machine.gpu.enabled", `true
other = 3`, "--local"})
	if rc == 0 || out != "" || !strings.Contains(errw, "invalid config value:") {
		t.Fatalf("multiple TOML values rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if got, _ := os.ReadFile(localPath); !bytes.Equal(got, before) {
		t.Fatalf("invalid multi-value set changed bytes: %q", got)
	}
}

func TestRunConfigEditUnsetUsesReadableRemovalPolicy(t *testing.T) {
	project := testconfig.Workspace(t, "[mcp_servers.tavily]\ncommand = [\"npx\"]\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	localPath := filepath.Join(project.Root, paths.WorkspaceLocalConfigTOMLName)
	if err := os.WriteFile(localPath, []byte("[mcp_servers.tavily]\ncommand = [\"npx\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runConfigEditCapture(t, project.Root, []string{"unset", "mcp_servers.tavily", "--local"})
	if rc != 0 || errw != "" || !strings.Contains(out, `tavily = "off"`) {
		t.Fatalf("unset rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`tavily = "off"`)) {
		t.Fatalf("unset did not write the schema removal: %q", got)
	}
}

func TestRunConfigEditJailedUserScopesPrintQuotedHostRunBeforeReadingTargets(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "project's config")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	// Invalid project input makes this an observable no-config-read control: these user-scope
	// requests must be handed to the host before the target resolver can inspect the graph.
	if err := os.WriteFile(filepath.Join(workspace, paths.WorkspaceConfigTOMLName), []byte("broken = [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user := testconfig.User(t, "packs = []\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "test-jail")
	before, err := os.ReadFile(user.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"set dry-run", []string{"set", "packs", `["claude"]`, "--user", "--dry-run"}},
		{"add", []string{"add", "packs", `"claude"`, "--user"}},
		{"unset", []string{"unset", "packs", "--user"}},
		{"automatic user-only", []string{"add", "packs", "\"claude\""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, out, errw := runConfigEditCapture(t, workspace, tc.args)
			if rc == 0 || out != "" || !strings.Contains(errw, "user-scope config edits must run on the host") ||
				!strings.Contains(errw, "yolo host-run -C '") || !strings.Contains(errw, `project'"'"'s config`) ||
				!strings.Contains(errw, "-- yolo config ") {
				t.Fatalf("jailed User edit rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
		})
	}
	if after, _ := os.ReadFile(user.Path); !bytes.Equal(after, before) {
		t.Fatalf("jailed User edits changed bytes: %q", after)
	}
}

func TestRunConfigEditDoesNotOfferHostRunForProjectOnlyUserFlag(t *testing.T) {
	project := testconfig.Workspace(t, "machine = {}\n")
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "test-jail")
	rc, out, errw := runConfigEditCapture(t, project.Root,
		[]string{"set", "requests.github.repos", "[]", "--user"})
	if rc == 0 || out != "" || strings.Contains(errw, "host-run") || !strings.Contains(errw, "workspace-only") {
		t.Fatalf("Project-only --user rc=%d stdout=%q stderr=%q; want scope-specific refusal without host advice", rc, out, errw)
	}
}

func TestRunConfigEditPreservesQuotedDottedAndStarMembersThroughApplyTargeted(t *testing.T) {
	project := testconfig.Workspace(t, `[mcp_servers."server.with.dots"]
command = ["dotted-old"]

[mcp_servers."*"]
command = ["star-old"]
`)
	user := testconfig.User(t, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	for _, tc := range []struct{ key, want, keep string }{
		{`mcp_servers."server.with.dots".command`, `"dotted-new"`, `"star-old"`},
		{`mcp_servers."*".command`, `"star-new"`, `"dotted-new"`},
	} {
		value := strings.Trim(tc.want, `"`)
		rc, out, errw := runConfigEditCapture(t, project.Root,
			[]string{"set", tc.key, `[` + `"` + value + `"` + `]`, "--project"})
		if rc != 0 || errw != "" || !strings.Contains(out, project.Path) {
			t.Fatalf("set %s rc=%d stdout=%q stderr=%q", tc.key, rc, out, errw)
		}
		got, err := os.ReadFile(project.Path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte(tc.want)) || !bytes.Contains(got, []byte(tc.keep)) {
			t.Fatalf("structured edit %s lost target/sibling values: %q", tc.key, got)
		}
	}
}

func TestParseConfigEditKeyPreservesQuotedDottedAndStarMembers(t *testing.T) {
	got, err := parseConfigEditKey(`providers."my.provider".models.'global.openai.gpt-6.1'."*"`)
	if err != nil {
		t.Fatal(err)
	}
	want := []configdoc.Step{
		{Key: "providers"}, {Key: "my.provider"}, {Key: "models"},
		{Key: "global.openai.gpt-6.1"}, {Key: "*"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed path = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parsed path = %#v, want %#v", got, want)
		}
	}
}

func TestParseConfigEditValueKeepsVersionStringsAndTOMLTypes(t *testing.T) {
	version, err := parseConfigEditValue("3.12", []configdoc.Step{{Key: "install"}, {Key: "mise"}, {Key: "python"}})
	if err != nil || version != "3.12" {
		t.Fatalf("version value = %#v, %v; want string 3.12", version, err)
	}
	count, err := parseConfigEditValue("272_000", []configdoc.Step{{Key: "providers"}, {Key: "bedrock"}, {Key: "models"}, {Key: "global.openai.gpt"}, {Key: "context"}})
	if err != nil {
		t.Fatalf("typed TOML number: %v", err)
	}
	if count != int64(272000) {
		t.Fatalf("typed TOML number = %#v, want int64 272000", count)
	}
}

// A PACK'S SETTING HAS A VERB EACH WAY. Advice names `yolo config set allow.<pack>.<setting>
// <value>` and, for a list such as the GitHub broker's sets, its removal; `unset` refused every
// `allow.*.*` key ("no scalar removal policy"), and `set` refused a bare profile name as TOML.
func TestAPacksSettingIsSetBareAndUnsetWhole(t *testing.T) {
	profile := []configdoc.Step{{Key: "allow"}, {Key: "aws-auth"}, {Key: "profile"}}
	if v, err := parseConfigEditValue("sso", profile); err != nil || v != "sso" {
		t.Fatalf("bare profile = %#v, %v; want the string sso", v, err)
	}
	sets := []configdoc.Step{{Key: "allow"}, {Key: "github"}, {Key: "sets"}}
	data := []byte("packs = [\"github\"]\n\n[allow.github]\nsets = [{ name = \"read-write\", mode = \"standing\" }]\n")
	m, err := configEditUnsetMutation(configdoc.TOML, sets, sets, data)
	if err != nil {
		t.Fatalf("unset allow.github.sets: %v", err)
	}
	if m.Action != configedit.Remove || len(m.Path) != 3 || m.Path[2].Key != "sets" {
		t.Fatalf("unset allow.github.sets = %+v; want the line removed", m)
	}
	if m, err := configEditUnsetMutation(configdoc.TOML, profile, profile, data); err != nil || m.Action != configedit.Remove {
		t.Fatalf("unset allow.aws-auth.profile = %+v, %v; want the line removed", m, err)
	}
}

func runConfigEditCapture(t *testing.T, workspace string, args []string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := runConfigEditWith(args, &out, &errw, workspace)
	return rc, out.String(), errw.String()
}

// `yolo config set|add|unset` runs `yolo check` right after a project edit, so the line before
// that check must not tell the reader to run it.
func TestProjectWrittenLineDoesNotAskForTheCheckThatFollows(t *testing.T) {
	if got := projectWrittenLine(true); strings.Contains(got, "yolo check") {
		t.Errorf("projectWrittenLine(true) = %q; the check runs next, so it must not ask for one", got)
	}
	if got := projectWrittenLine(false); !strings.Contains(got, "Run `yolo check`") {
		t.Errorf("projectWrittenLine(false) = %q; with no check after it, it must ask for one", got)
	}
}
