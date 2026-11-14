package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// EVERY KEY ADVICE TELLS A USER TO UNSET IS ONE `yolo config unset` REMOVES. Advice names a verb,
// not a file to hand-edit (AGENTS.md), so a message naming `yolo config unset <key>` is a dead end
// when the verb refuses that key. It did for every key the schema types `any` and every table
// with no removal spelling ("this key has no scalar removal policy"): `macos_user_shared_tmp`,
// the hatch the launch's `tmp:` line tells the user to remove (the 2026-11-13 Mac lab report,
// §2), `resource_groups`, `setups.<name>` and `setups.<name>.<field>`, `cache_relocations.<subdir>`.
//
// The keys are read where advice is written: every `yolo config unset …` hint in a non-test Go
// string (scanHints, the reader TestEveryHintedYoloCommandExists uses, so a placeholder piece
// reads as `<…>`), plus config-ref and the reference docs. A placeholder component is filled
// with a sample name. dynamicUnsetKeys covers the hints whose whole key is spliced in.
func TestEveryKeyAdviceNamesForUnsetIsRemovable(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{} // key -> where advice names it
	for _, h := range scanHints(t, root) {
		if key, ok := unsetHintKey(h.text); ok {
			keys[key] = h.pos
		}
	}
	docKey := regexp.MustCompile("yolo config unset ([^`\\n|]*)")
	for _, doc := range unsetAdviceDocs(t, root) {
		data, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, doc)
		for _, m := range docKey.FindAllStringSubmatch(string(data), -1) {
			if key, ok := unsetHintKey("yolo config unset " + m[1]); ok {
				keys[key] = rel
			}
		}
	}
	for key, where := range dynamicUnsetKeys {
		keys[key] = where
	}
	for _, want := range []string{"macos_user_shared_tmp", "resource_groups", "network.mode", "setups.x.policy", "env.secrets.set.x"} {
		if _, ok := keys[want]; !ok {
			t.Fatalf("the scan found no advice naming `yolo config unset %s`; it has lost its input (found %v)", want, keys)
		}
	}
	for key, where := range keys {
		path, err := parseConfigEditKey(key)
		if err != nil {
			t.Errorf("%s: `yolo config unset %s` names no key: %v", where, key, err)
			continue
		}
		if path[0].Key == "repos" {
			continue // namedRepoUnset plans these; TestNamedRepoUnset* covers them
		}
		format, empty := configdoc.TOML, []byte(nil)
		if path[0].Key == "loopholes" {
			// A legacy file's switch; TOML has no loopholes table.
			format, empty = configdoc.JSONC, []byte("{}\n")
		}
		for _, shape := range unsetFixtureShapes(path) {
			plan, err := configedit.PlanEdit(empty, format, configedit.Mutation{Action: configedit.Add, Path: shape.at, Value: shape.value})
			if err != nil {
				t.Fatalf("%s: cannot write a fixture for %s: %v", where, key, err)
			}
			if _, err := configEditUnsetMutation(format, path, path, plan.Updated); err != nil {
				t.Errorf("%s: advice names `yolo config unset %s`, which refuses a %s value: %v\n%s",
					where, key, shape.name, err, plan.Updated)
			}
		}
	}
}

// dynamicUnsetKeys are keys whose whole spelling a message splices in, so the hint reads
// `yolo config unset <…>`: each is a sample of what the splice produces, and where.
var dynamicUnsetKeys = map[string]string{
	"allow.aws-auth.off":          "internal/cli/run/offswitch.go (a pack's off switch)",
	"requests.aws-auth.off":       "internal/cli/run/offswitch.go (a project's off switch)",
	"loopholes.aws-auth.enabled":  "internal/cli/run/offswitch.go (a legacy switch)",
	"requests.aws-auth":           "internal/cli/run/loopholegrants.go (a project's requests.<pack> = \"off\")",
	"pack_sources.tools":          "internal/cli/packsourcerename.go",
	"requests.git.push":           "internal/cli/approveconfiggrant.go",
	"requests.git.push[0]":        "internal/cli/approveconfiggrant.go",
	"allow.github.always":         "internal/config/loopholegrants.go",
	"allow.call-broker.on_demand": "internal/config/callbroker.go (an allow.<source>.<field>)",
}

// unsetHintKey is the key a `yolo config unset …` hint names, a placeholder component filled
// with "x" and a placeholder index with 0; ok is false for another command, or a hint whose whole
// key is a placeholder.
func unsetHintKey(text string) (string, bool) {
	fields, ok := strings.CutPrefix(text, "yolo config unset ")
	if !ok {
		return "", false
	}
	for _, field := range strings.Fields(fields) {
		if strings.HasPrefix(field, "-") || field == "cannot" {
			continue
		}
		key := strings.Trim(field, "'\"`.,;:)")
		key = regexp.MustCompile(`\[[^\]]*[<…%][^\]]*\]`).ReplaceAllString(key, "[0]")
		key = regexp.MustCompile(`<[^>]*>|%[sdqv]|…`).ReplaceAllString(key, "x")
		if key == "" || strings.Trim(key, "x.") == "" || strings.HasSuffix(field, ".") && !strings.HasSuffix(key, "x") {
			return "", false
		}
		return key, true
	}
	return "", false
}

// unsetAdviceDocs are the files besides Go strings that tell a user what to unset: config-ref and
// the user guide and reference docs. Design docs and research are proposals, not advice.
func unsetAdviceDocs(t *testing.T, root string) []string {
	t.Helper()
	out := []string{filepath.Join(root, "internal", "cli", "config_ref.txt")}
	for _, dir := range []string{"userguide", filepath.Join("docs", "reference")} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

type unsetFixtureShape struct {
	name  string
	at    []configdoc.Step
	value any
}

// unsetFixtureShapes are the values a file may hold at path: a scalar and a table, or, for an
// item of a list, the list.
func unsetFixtureShapes(path []configdoc.Step) []unsetFixtureShape {
	if last := path[len(path)-1]; last.IsIndex {
		return []unsetFixtureShape{{"list item", path[:len(path)-1], []any{"main"}}}
	}
	return []unsetFixtureShape{
		{"scalar", path, true},
		{"table", path, map[string]any{"k": "v"}},
	}
}

// THE LAB REPORT'S TWO UNSETS, END TO END (2026-11-13, §2 and §4): the hatch is removed from the
// user config, and two secrets unset one by one leave no whitespace line and no empty table.
func TestConfigUnsetRemovesTheSharedTmpHatch(t *testing.T) {
	project := testconfig.Workspace(t, "")
	user := testconfig.User(t, "macos_user_shared_tmp = true\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	rc, out, errw := runConfigEditCapture(t, project.Root, []string{"unset", "macos_user_shared_tmp", "--user"})
	if rc != 0 {
		t.Fatalf("unset macos_user_shared_tmp rc=%d stdout=%q stderr=%q", rc, out, errw)
	}
	if got, _ := os.ReadFile(user.Path); string(got) != "" {
		t.Fatalf("user config after unset = %q, want it empty", got)
	}
}

// A TABLE WITH NO REMOVAL SPELLING IS REMOVED WHOLE from the named file; the unset refused it
// ("not an ordinary scalar"), and the planner, asked to, refused its own splice, expecting the
// `machine` table only `[machine.gpu]` implied to outlive it.
func TestRunConfigEditUnsetRemovesAWholeTable(t *testing.T) {
	for _, tc := range []struct{ name, before, want string }{
		{"beside a sibling", "[machine.resources]\ncpus = 2\n\n[machine.gpu]\nenabled = true\ncapabilities = \"utility\"\n",
			"[machine.resources]\ncpus = 2\n\n"},
		{"alone", "[machine.gpu]\nenabled = true\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testconfig.Workspace(t, "")
			user := testconfig.User(t, "")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			path := filepath.Join(project.Root, paths.WorkspaceConfigTOMLName)
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatal(err)
			}
			rc, out, errw := runConfigEditCapture(t, project.Root, []string{"unset", "machine.gpu", "--project"})
			if rc != 0 {
				t.Fatalf("unset machine.gpu rc=%d stdout=%q stderr=%q", rc, out, errw)
			}
			if got, _ := os.ReadFile(path); string(got) != tc.want {
				t.Fatalf("project config after unset = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfigUnsetOfEachSecretLeavesNoBlankLineOrEmptyTable(t *testing.T) {
	for _, tc := range []struct{ name, before, want string }{
		{"table dropped", "[network]\nmode = \"bridge\"\n\n[env.secrets.set]\nAWS_ACCESS_KEY_ID = \"a\"\nAWS_SECRET_ACCESS_KEY = \"b\"\n",
			"[network]\nmode = \"bridge\"\n"},
		// A comment is never removed, so the table it sits in stays with it.
		{"commented table kept", "[env.secrets.set]\n# Bedrock keys\nAWS_ACCESS_KEY_ID = \"a\"\nAWS_SECRET_ACCESS_KEY = \"b\"\n",
			"[env.secrets.set]\n# Bedrock keys\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testconfig.Workspace(t, "")
			user := testconfig.User(t, "")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			path := filepath.Join(project.Root, paths.WorkspaceConfigTOMLName)
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
				rc, out, errw := runConfigEditCapture(t, project.Root, []string{"unset", "env.secrets.set." + name, "--project"})
				if rc != 0 {
					t.Fatalf("unset %s rc=%d stdout=%q stderr=%q", name, rc, out, errw)
				}
			}
			if got, _ := os.ReadFile(path); string(got) != tc.want {
				t.Fatalf("project config after both unsets = %q, want %q", got, tc.want)
			}
		})
	}
}
