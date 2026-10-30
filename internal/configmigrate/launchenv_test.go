package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// launchResolvedEnv is the env a launch in ws resolves: the merged config the launch loads,
// through the resolver every launch path calls.
func launchResolvedEnv(t *testing.T, ws string) map[string]string {
	t.Helper()
	merged, err := config.LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	env, removals := config.ResolveEnvSourcesFull(ws, merged, nil)
	out := map[string]string{}
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		out[k], _ = v.(string)
	}
	for _, k := range removals {
		out[k] = "<removed>"
	}
	return out
}

// TestMigratedEnvSourcesKeepTheLaunchEnv is W3-O's finding: `yolo config migrate` proved a
// config with env_sources equal, and the converted config then lost its env at launch, because
// the proof compared the lowered `env.files` key the launch never read. Now the proof carries the
// launch's own env entry list, and the written files resolve to the env the old ones did.
func TestMigratedEnvSourcesKeepTheLaunchEnv(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "user.env"), "FROM_USER=u\nSHARED=user\n")
	write(t, filepath.Join(dir, "config.jsonc"), `{"env_sources": ["user.env"]}`+"\n")
	ws := realDir(t)
	write(t, filepath.Join(ws, "dev.env"), "FROM_WS=w\nSHARED=ws\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"env_sources": ["dev.env"]}`+"\n")

	before := launchResolvedEnv(t, ws)
	if want := map[string]string{"FROM_USER": "u", "FROM_WS": "w", "SHARED": "ws"}; !reflect.DeepEqual(before, want) {
		t.Fatalf("legacy fixture resolves %v, want %v", before, want)
	}

	p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
	if !p.Equal {
		t.Fatalf("unequal: %+v %+v", p.Differences, p.NotConverted)
	}
	if !strings.Contains(p.canonical.env, "dev.env") || !strings.Contains(p.canonical.env, "user.env") {
		t.Fatalf("the proof does not carry the launch's env entries: %q", p.canonical.env)
	}
	if _, err := Apply(p, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if after := launchResolvedEnv(t, ws); !reflect.DeepEqual(after, before) {
		t.Fatalf("the converted config resolves a different env at launch:\n  before %v\n  after  %v", before, after)
	}
}

// TestTheProofNamesALaunchEnvDifference pins the comparison itself: two reads whose merged
// configs spell the same files in a different order resolve differently (a later file wins), and
// the difference is reported under the launch's env, not passed as equal.
func TestTheProofNamesALaunchEnvDifference(t *testing.T) {
	a := jsonx.NewOrderedMap()
	a.Set("env_sources", []any{"/x/a.env", "/x/b.env"})
	b := jsonx.NewOrderedMap()
	env := jsonx.NewOrderedMap()
	env.Set("files", []any{"/x/b.env", "/x/a.env"})
	b.Set("env", env)
	diffs := diffValues("the environment the launch resolves", launchEnv(a), launchEnv(b))
	if len(diffs) == 0 {
		t.Fatal("a reordered env file list was proved equal")
	}
	same := jsonx.NewOrderedMap()
	sameEnv := jsonx.NewOrderedMap()
	sameEnv.Set("files", []any{"/x/a.env", "/x/b.env"})
	same.Set("env", sameEnv)
	if diffs := diffValues("env", launchEnv(a), launchEnv(same)); len(diffs) != 0 {
		t.Fatalf("env_sources and its env.files spelling differ: %+v", diffs)
	}
}

// TestInlineVariablesAfterEveryFileConvertToEnvSet is the owner's first real migration (refused
// as "a yolo bug"): a user env_sources ending in an inline table, in the top file or an included
// one. env.set applies after every file, so inline variables that follow every file mean the same
// there, a null one becoming env.unset; the converted config resolves the env the old one did.
func TestInlineVariablesAfterEveryFileConvertToEnvSet(t *testing.T) {
	const sources = `"env_sources": ["~/.config/yolo-jail/local.env", {"PI_TRUE_COLOR": "1", "YOLO_JAIL": "1"}, {"DROPPED": null}]`
	for _, where := range []string{"config", "overrides", "work"} {
		t.Run(where, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			write(t, filepath.Join(dir, "local.env"), "FROM_FILE=f\nPI_TRUE_COLOR=0\n")
			files := map[string]string{"config": `"include_if_found": ["overrides.jsonc", "work.jsonc"]`, "overrides": ``, "work": ``}
			if files[where] != "" {
				files[where] += ", "
			}
			files[where] += sources
			for name, body := range files {
				write(t, filepath.Join(dir, name+".jsonc"), "{"+body+"}\n")
			}
			ws := realDir(t)
			before := launchResolvedEnv(t, ws)
			if before["PI_TRUE_COLOR"] != "1" || before["FROM_FILE"] != "f" || before["DROPPED"] != "<removed>" {
				t.Fatalf("legacy fixture resolves %v", before)
			}
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			converted := readFile(t, filepath.Join(dir, where+".toml"))
			for _, want := range []string{"[env.set]", "PI_TRUE_COLOR", "unset", "DROPPED"} {
				if !strings.Contains(converted, want) {
					t.Errorf("%s.toml lacks %q:\n%s", where, want, converted)
				}
			}
			if after := launchResolvedEnv(t, ws); !reflect.DeepEqual(after, before) {
				t.Fatalf("the converted config resolves a different env:\n  before %v\n  after  %v", before, after)
			}
		})
	}
}

// TestAnInlineEntryBeforeAFileIsNotConverted: a file after an inline table can override it, which
// env.set (applied after every file) cannot say, so the list is named, not converted.
func TestAnInlineEntryBeforeAFileIsNotConverted(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "local.env"), "A=f\n")
	write(t, filepath.Join(dir, "config.jsonc"), `{"env_sources": [{"A": "1"}, "local.env"]}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir, Workspace: realDir(t)}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) || len(ineq.Plan.NotConverted) != 1 || ineq.Plan.NotConverted[0].Path != "env_sources" {
		t.Fatalf("want env_sources not converted, got %v", err)
	}
}

// TestUserInlineVariablesBeforeAProjectFileAreNotConverted: the project's files follow the user's
// list at launch, so a user inline table moved to env.set would win over them. The proof refuses
// that plan, and the plan made again without the inline conversion names the list instead.
func TestUserInlineVariablesBeforeAProjectFileAreNotConverted(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"env_sources": [{"A": "user"}]}`+"\n")
	ws := realDir(t)
	write(t, filepath.Join(ws, "dev.env"), "A=ws\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"env_sources": ["dev.env"]}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir, Workspace: ws}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("want the plan refused, got %v", err)
	}
	found := false
	for _, nc := range ineq.Plan.NotConverted {
		found = found || nc.Path == "env_sources"
	}
	if !found {
		t.Fatalf("want env_sources named as not converted: %+v", ineq.Plan.NotConverted)
	}
}

func discoverFor(t *testing.T, req Request) *Graph {
	t.Helper()
	g, err := Discover(req)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return g
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestAnUnrelatedRefusalKeepsTheInlineConversion is the owner's work config: a refusal elsewhere
// (here a key with no TOML spelling) must not make the plan drop a trailing inline env_sources
// table it converts exactly, and name env_sources as not converted beside the real refusal.
func TestAnUnrelatedRefusalKeepsTheInlineConversion(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "local.env"), "A=f\n")
	write(t, filepath.Join(dir, "config.jsonc"), `{"env_sources": ["local.env", {"PI_TRUE_COLOR": "1"}], "brokered": {"github": {"grants": [{"kind": "read", "repo": "*"}]}}}`+"\n")
	_, err := Plan(discoverFor(t, Request{UserConfigDir: dir, Workspace: realDir(t)}))
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("want the plan refused for brokered, got %v", err)
	}
	for _, nc := range ineq.Plan.NotConverted {
		if nc.Path == "env_sources" {
			t.Fatalf("env_sources named as not converted beside an unrelated refusal: %+v", ineq.Plan.NotConverted)
		}
	}
}
