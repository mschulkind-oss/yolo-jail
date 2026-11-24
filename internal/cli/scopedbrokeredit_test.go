package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func assertScopedEditingAWSSelected(t *testing.T, userPath string) {
	t.Helper()
	raw, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := configdoc.Parse(raw, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := doc.Values.Get("packs")
	if !ok || !reflect.DeepEqual(selected, []any{"aws"}) {
		t.Fatalf("fixture packs=%#v", selected)
	}
	pack, err := resolveConfiguredPack(config.PackEntry{Name: "aws", IsEmbedded: true, EmbeddedName: "aws"})
	if err != nil {
		t.Fatal(err)
	}
	brokers, _, err := config.ReadableDeclarationsForPacks([]*packload.Pack{pack})
	if err != nil {
		t.Fatal(err)
	}
	for _, broker := range brokers {
		if broker.CallBroker != nil && broker.CallBroker.Source == "aws" && len(broker.CallBroker.Cells) > 0 {
			return
		}
	}
	t.Fatal("selected aws has no actual call-broker declaration/cells")
}

func TestScopedBrokerEditingQuotedContextFieldsRoundTrip(t *testing.T) {
	root := t.TempDir()
	packDir := filepath.Join(root, "context-fixture")
	moduleDir := filepath.Join(packDir, "loopholes", "fixture")
	if err := os.MkdirAll(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack.toml"), []byte("name=\"context-fixture\"\n[[loophole]]\nfrom=\"loopholes/fixture\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `name="fixture"
transport="loopback-tls"
[call_broker]
source="fixture"
front="cli"
program="synthetic-no-execution"
classifier=["yolo","internal","call-broker-classify","fixture"]
[call_broker.scope]
roots="declared"
[[call_broker.resource]]
type="root"
[[call_broker.cell]]
name="list"
kind="read"
[call_broker.context.fields]
"Profile équipe"="SYNTHETIC_PROFILE"
`
	if err := os.WriteFile(filepath.Join(moduleDir, "loophole.toml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	user, _ := genericFixture(t, fmt.Sprintf("packs=[\"src/context-fixture\"]\n[pack_sources]\nsrc=%q\n# Keep this authored comment.\n[contexts.\"Finance équipe\"]\nsource=\"fixture\"\n\"Profile équipe\"=\"old selector\"\n", root), configdoc.TOML, "")
	rc, out, errs := runConfigVerb(t, "set", `contexts."Finance équipe"."Profile équipe"`, "new selector", "--user")
	if rc != 0 {
		t.Fatalf("quoted context edit rc=%d out=%s err=%s", rc, out, errs)
	}
	body, err := os.ReadFile(user.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "# Keep this authored comment.") {
		t.Fatal("comment lost")
	}
	doc, err := configdoc.Parse(body, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	contexts, _ := doc.Values.Get("contexts")
	context, _ := contexts.(*jsonx.OrderedMap).Get("Finance équipe")
	field, _ := context.(*jsonx.OrderedMap).Get("Profile équipe")
	if field != "new selector" {
		t.Fatalf("exact context/field identity lost: %v", field)
	}
	rc, out, errs = runConfigVerb(t, "why", `contexts."Finance équipe"."Profile équipe"`)
	if rc != 0 || !strings.Contains(out, "new selector") || !strings.Contains(out, "config.toml:") {
		t.Fatalf("why lost value/writer: rc=%d out=%s err=%s", rc, out, errs)
	}
	rc, out, errs = runConfigVerb(t, "check")
	if rc != 0 {
		t.Fatalf("post-write config check rc=%d out=%s err=%s", rc, out, errs)
	}
	rc, out, errs = runConfigVerb(t, "set", `contexts."Finance équipe"."Unknown Field"`, "synthetic", "--user")
	if rc == 0 || !strings.Contains(out+errs, "undeclared") {
		t.Fatalf("unknown field accepted rc=%d out=%s err=%s", rc, out, errs)
	}
}

func TestScopedBrokerEditingSourceAskWordIsMetadata(t *testing.T) {
	_, project := genericFixture(t, "packs=[\"aws\"]\n", configdoc.TOML, "")
	rc, out, errs := runConfigVerb(t, "set", "requests.aws.working_set.put_objects", "ask", "--project")
	if rc != 0 {
		t.Fatalf("source ask edit rc=%d out=%s err=%s", rc, out, errs)
	}
	body, err := os.ReadFile(project.Path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := configdoc.Parse(body, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	requests, _ := doc.Values.Get("requests")
	source, _ := requests.(*jsonx.OrderedMap).Get("aws")
	table, _ := source.(*jsonx.OrderedMap).Get("working_set")
	word, _ := table.(*jsonx.OrderedMap).Get("put_objects")
	if word != "ask" {
		t.Fatalf("source request word changed: %v", word)
	}
}

func TestScopedBrokerEditingNativeReadConflictNamesBothWriters(t *testing.T) {
	user, _ := genericFixture(t, "packs=[\"git\"]\n[allow.git]\nalways=[\"fetch\"]\non_request=[]\n", configdoc.TOML, "")
	rc, out, errs := runConfigVerb(t, "add", "allow.git.on_request", "read", "--user")
	if rc == 0 || !strings.Contains(out+errs, "same native read") || !strings.Contains(out+errs, "config.toml:3:") || !strings.Contains(out+errs, "config.toml:4:") {
		t.Fatalf("conflict lacks positioned writers rc=%d out=%s err=%s", rc, out, errs)
	}
	body, err := os.ReadFile(user.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `on_request=["read"]`) {
		t.Fatal("refused alias edit wrote user config")
	}
}

func TestScopedBrokerEditingLegacyNewVocabularyRefusesWithoutWrite(t *testing.T) {
	for _, tc := range []struct{ name, user, key, value string }{
		{"scopes", `{"packs":["aws"]}`, "scopes.one", `{source="aws",resource="s3://b"}`},
		{"native-read-alias", `{"packs":["git"],"allow":{"git":{"always":["fetch"],"on_request":[]}}}`, "allow.git.on_request", `["read"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user, _ := genericFixture(t, "packs=[\"aws\",\"git\"]\n", configdoc.TOML, "")
			if err := os.Remove(user.Path); err != nil {
				t.Fatal(err)
			}
			user.Path = filepath.Join(filepath.Dir(user.Path), "config.jsonc")
			if err := os.WriteFile(user.Path, []byte(tc.user), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(user.Path)
			if err != nil {
				t.Fatal(err)
			}
			rc, out, errs := runConfigVerb(t, "set", tc.key, tc.value, "--user")
			if rc == 0 || !strings.Contains(out+errs, "TOML-only") {
				t.Fatalf("legacy edit accepted rc=%d out=%s err=%s", rc, out, errs)
			}
			after, err := os.ReadFile(user.Path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("refused legacy edit changed authored file")
			}
		})
	}
}

func TestScopedBrokerEditingRejectsUndeclaredNeverToken(t *testing.T) {
	user, _ := genericFixture(t, `packs = ["aws"]
[allow.aws]
on_request = ["read"]
`, configdoc.TOML, `[call_roots]
aws = ["s3://b"]
`)
	assertScopedEditingAWSSelected(t, user.Path)
	rc, out, errs := runConfigVerb(t, "add", "allow.aws.never", "not_declared", "--user")
	if rc == 0 || !strings.Contains(out+errs, "not_declared") {
		t.Fatalf("undeclared token accepted or not identified: rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
}

func TestScopedBrokerEditingVerbsReachNewTOMLPaths(t *testing.T) {
	user, ws := genericFixture(t, `# fixture only
packs = ["aws"]
[allow.aws]
on_request = ["read", "all.put_objects", "all.delete_objects"]
[scopes.reports]
source = "aws"
resource = "s3://b/old/"
`, configdoc.TOML, `# fixture only
[call_roots]
aws = ["s3://a", "s3://b"]
[scopes.team]
source = "aws"
resource = "s3://a/team/"
[scopes.team.requests.working_set]
put_objects = true
`)
	assertScopedEditingAWSSelected(t, user.Path)
	for _, args := range [][]string{
		{"add", "scopes.reports.allow.never", "all", "--user"},
		{"set", "scopes.reports.resource", "s3://b/reports/", "--user"},
		{"set", "scopes.team.requests.all.put_objects", "true", "--project"},
	} {
		rc, out, errs := runConfigVerb(t, args...)
		if rc != 0 {
			t.Fatalf("args=%q rc=%d stdout=%q stderr=%q", args, rc, out, errs)
		}
	}
	for _, tc := range []struct {
		path string
		keys []string
		want any
	}{
		{user.Path, []string{"scopes", "reports", "allow", "never"}, []any{"all"}},
		{user.Path, []string{"scopes", "reports", "resource"}, "s3://b/reports/"},
		{ws.Path, []string{"scopes", "team", "requests", "all", "put_objects"}, true},
		{ws.Path, []string{"scopes", "team", "requests", "working_set", "put_objects"}, true},
	} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := configdoc.Parse(data, configdoc.TOML)
		if err != nil {
			t.Fatal(err)
		}
		var value any = doc.Values
		for _, key := range tc.keys {
			table, ok := value.(*jsonx.OrderedMap)
			if !ok {
				t.Fatalf("path=%v reached %T before %s", tc.keys, value, key)
			}
			value, ok = table.Get(key)
			if !ok {
				t.Fatalf("path=%v missing %s", tc.keys, key)
			}
		}
		if !reflect.DeepEqual(value, tc.want) {
			t.Fatalf("path=%v got=%#v want=%#v", tc.keys, value, tc.want)
		}
	}
}
