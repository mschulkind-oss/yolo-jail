package configmigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The owner's dotfiles workspace (RC-D60): nine branch grants and one create-branch grant on the
// workspace's one remote. The nine converted and the tenth was refused as having "no request
// spelling", so nothing was written; it converts exactly now, the create-branch grant becoming
// requests.git.new_branches = true and the nine branches staying nine entries, in order.
func TestACreateBranchGrantConvertsToNewBranches(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "git@github.com:mschulkind/dotfiles.git")
	branches := []string{"main", "patch/claude", "patch/codex", "patch/copilot", "patch/gemini", "patch/pi", "patch/kilo", "patch/agy", "patch/yolo"}
	var grants []string
	for _, b := range branches {
		grants = append(grants, fmt.Sprintf(`{"kind": "branch", "repo": "github.com/mschulkind/dotfiles", "branch": %q}`, b))
	}
	grants = append(grants, `{"kind": "create-branch", "repo": "github.com/mschulkind/dotfiles"}`)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), "{\n  \"brokered\": {\"git\": {\"grants\": [\n    "+strings.Join(grants, ",\n    ")+"\n  ]}}\n}\n")

	p := plan(t, Request{Workspace: ws})
	if !p.Equal || len(p.NotConverted) > 0 {
		t.Fatalf("plan unequal or incomplete: differences %+v, not converted %+v", p.Differences, p.NotConverted)
	}
	if p.Refused != nil {
		t.Fatalf("the launch gate refused the converted file: %v", p.Refused)
	}
	got := target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes
	doc, err := configdoc.Parse(got, configdoc.TOML)
	if err != nil {
		t.Fatalf("converted file does not parse: %v\n%s", err, got)
	}
	requests := mustTable(t, doc.Values, "requests", "git")
	if v, _ := requests.Get("new_branches"); v != true {
		t.Fatalf("requests.git.new_branches = %#v, want true:\n%s", v, got)
	}
	push, _ := requests.Get("push")
	list, _ := push.([]any)
	if len(list) != len(branches) {
		t.Fatalf("requests.git.push = %#v, want the nine branches one each:\n%s", push, got)
	}
	for i, b := range branches {
		if list[i] != b {
			t.Fatalf("requests.git.push[%d] = %v, want %s (in order, never collapsed to a pattern):\n%s", i, list[i], b, got)
		}
	}
	if strings.Contains(string(got), "brokered") {
		t.Fatalf("the converted file still holds the legacy spelling:\n%s", got)
	}
}

// Nine branch grants are nine request entries even where a pattern would cover them: collapsing
// would widen the grant to branches nobody listed (RC-D60).
func TestNineBranchGrantsStayNineRequestEntries(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	var grants []string
	for i := 1; i <= 9; i++ {
		grants = append(grants, fmt.Sprintf(`{"kind": "branch", "repo": "github.com/acme/one", "branch": "patch/b%d"}`, i))
	}
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [`+strings.Join(grants, ", ")+`]}}}`+"\n")
	p := plan(t, Request{Workspace: ws})
	got := string(target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes)
	if strings.Contains(got, "patch/*") || strings.Count(got, `"patch/b`) != 9 {
		t.Fatalf("converted file does not list the nine branches one each:\n%s", got)
	}
}

// Every kind each shipped broker maps through kind_capabilities converts on the workspace's one
// remote: a kind that names something to the list of its names, a yes/no kind to `= true`. A
// kind added to a pack's declaration is covered with no edit here.
func TestEveryMappedGrantKindOfEveryShippedBrokerConverts(t *testing.T) {
	declarations, err := shippedBrokers()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, d := range declarations {
		pending := map[string]bool{}
		for _, c := range d.PendingCapabilities {
			pending[c] = true
		}
		kinds := make([]string, 0, len(d.KindCapabilities))
		for k := range d.KindCapabilities {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			capability := d.KindCapabilities[kind]
			if pending[capability] {
				continue
			}
			source := d.Brokered.Source
			t.Run(source+"/"+kind, func(t *testing.T) {
				env(t)
				ws := realDir(t)
				gitRemotes(t, ws, "https://github.com/acme/one.git")
				grant := fmt.Sprintf(`{"kind": %q, "repo": "github.com/acme/one"`, kind)
				field := brokerscope.GrantNameKey(kind)
				// A name the kind takes: a workflow is a file name in .github/workflows.
				name := "x-name"
				if kind == brokerscope.KindRuns {
					name = "x-name.yml"
				}
				if field != "" {
					grant += fmt.Sprintf(`, %q: %q`, field, name)
				}
				grant += "}"
				write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"`+source+`": {"grants": [`+grant+`]}}}`+"\n")
				p := plan(t, Request{Workspace: ws})
				if !p.Equal || len(p.NotConverted) > 0 {
					t.Fatalf("plan unequal or incomplete: %+v %+v", p.Differences, p.NotConverted)
				}
				doc, err := configdoc.Parse(target(t, p, filepath.Join(ws, "yolo-jail.jsonc")).Bytes, configdoc.TOML)
				if err != nil {
					t.Fatal(err)
				}
				v, _ := mustTable(t, doc.Values, "requests", source).Get(capability)
				if field == "" {
					if v != true {
						t.Fatalf("requests.%s.%s = %#v, want true", source, capability, v)
					}
					return
				}
				if list, _ := v.([]any); len(list) != 1 || list[0] != name {
					t.Fatalf("requests.%s.%s = %#v, want [%q]", source, capability, v, name)
				}
			})
			seen++
		}
	}
	if seen < 5 {
		t.Fatalf("only %d mapped kinds across the shipped brokers; the declarations did not load", seen)
	}
}

// A yes/no grant on a repository that is not the workspace's one remote stays unconverted, and
// its reason ends in the exact edit, never in "keep the old file for now" (RC-D60, RC-D61).
func TestAYesNoGrantOnAnotherRepositoryStaysNamingTheEdit(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"brokered": {"git": {"grants": [{"kind": "create-branch", "repo": "github.com/acme/other"}]}}}`+"\n")
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Plan(g)
	if p == nil || len(p.NotConverted) != 1 {
		t.Fatalf("want the one grant left unconverted: %+v", p)
	}
	reason := p.NotConverted[0].Reason
	for _, want := range []string{
		"the workspace's remote is github.com/acme/one, not github.com/acme/other",
		"To keep this grant on github.com/acme/other alone, leave the old file (this yolo keeps reading it)",
		`set "repo" to github.com/acme/one and convert again`,
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason lacks %q:\n%s", want, reason)
		}
	}
	if strings.Contains(reason, "keep the old file for now") {
		t.Errorf("reason still says keep the old file for now:\n%s", reason)
	}
}

func mustTable(t *testing.T, m *jsonx.OrderedMap, keys ...string) *jsonx.OrderedMap {
	t.Helper()
	for _, k := range keys {
		v, _ := m.Get(k)
		next, ok := v.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("%s is not a table: %#v", k, v)
		}
		m = next
	}
	return m
}

// RC-D61: "keep the old file for now" is not a step, it is what happens anyway, so no reason the
// migration gives says it. Read from the source, since a reason is reached only by its own case.
func TestNoNotConvertedReasonSaysKeepTheOldFile(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "keep the old file for now") {
			t.Errorf("%s still says \"keep the old file for now\"", f)
		}
	}
}
