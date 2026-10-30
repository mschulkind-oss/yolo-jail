package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

// convertFirstFixture is a host workspace and user config in the formats asked for, with HOME
// and the cwd pointed at them, as newNamedVerbFixture sets them up.
func convertFirstFixture(t *testing.T, userFormat configdoc.Format, user string, wsFormat configdoc.Format, project string) (userPath, projectPath string) {
	t.Helper()
	ws := testconfig.WorkspaceFormat(t, wsFormat, project)
	u := testconfig.UserFormat(t, userFormat, user)
	t.Setenv("HOME", u.Root)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_WORKSPACE", "")
	t.Setenv("YOLO_HOST_DIR", "")
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)
	withConfigEditCWD(t, ws.Root)
	return u.Path, ws.Path
}

func assertConvertFirst(t *testing.T, verb, path, errs string) {
	t.Helper()
	for _, want := range []string{
		"yolo " + verb + ": " + path + " is still in the deprecated JSONC format, and ",
		" needs the TOML format. Convert it first: `yolo config migrate` (preview with `yolo config migrate --dry-run`), " +
			"then run this command again. Nothing was written.",
	} {
		if !strings.Contains(errs, want) {
			t.Fatalf("the refusal lacks %q: %q", want, errs)
		}
	}
}

const legacyUserWithBrokered = `{"packs": ["git", "github"], "brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}], "grant_kinds": ["branch"]}}}`

// `yolo allow` into a user config still on the legacy `brokered` grants would make an `allow`
// table that silently drops them (TestAnAllowTableSuppressesLegacyBrokeredUserGrants), so it
// refuses convert-first and writes nothing, dry run or not.
func TestAllowRefusesAJSONCUserConfigHoldingBrokered(t *testing.T) {
	userPath, _ := convertFirstFixture(t, configdoc.JSONC, legacyUserWithBrokered, configdoc.TOML, "")
	rec := recordAllowGate(t, true)
	for _, args := range [][]string{{"allow", "git", "fetch"}, {"allow", "git", "on_request", "push", "--dry-run"}} {
		rc, out, errs := captureStd(t, func() int { return registry["allow"](args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", args, rc, out, errs)
		}
		assertConvertFirst(t, "allow", userPath, errs)
		if got := readFile(t, userPath); got != legacyUserWithBrokered {
			t.Fatalf("%v wrote the user config:\n%s", args, got)
		}
	}
	if rec.calls != 0 {
		t.Fatalf("a refused allow ran the approval gate %d times", rec.calls)
	}
}

// A JSONC user config without `brokered` is written: the readers honor `allow` there
// (TestTheTOMLVocabularyTakesEffectInJSONCFiles).
func TestAllowWritesAJSONCUserConfigWithoutBrokered(t *testing.T) {
	userPath, _ := convertFirstFixture(t, configdoc.JSONC, `{"packs": ["git", "github"]}`, configdoc.TOML, "")
	recordAllowGate(t, false)
	rc, out, errs := captureStd(t, func() int { return registry["allow"]([]string{"allow", "git", "fetch"}) })
	if rc != 0 || strings.Contains(errs, "deprecated JSONC") {
		t.Fatalf("rc=%d stdout=%q stderr=%q", rc, out, errs)
	}
	if got := readFile(t, userPath); !strings.Contains(got, `"allow"`) || !strings.Contains(got, "fetch") {
		t.Fatalf("the JSONC user config was not written:\n%s", got)
	}
}

const legacyProjectWithBrokered = `{"brokered": {"git": {"grants": [{"kind": "branch", "repo": "*", "branch": "dev"}]}}}`

// `yolo request` into a JSONC project file still carrying `brokered` would leave one file with
// both vocabularies for the same grants, so it refuses convert-first and writes nothing.
func TestRequestRefusesAJSONCProjectHoldingBrokered(t *testing.T) {
	_, projectPath := convertFirstFixture(t, configdoc.TOML,
		"packs = [\"git\", \"github\"]\n\n[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\", \"new_branches\"]\n",
		configdoc.JSONC, legacyProjectWithBrokered)
	for _, args := range [][]string{{"request", "git", "push", "main"}, {"request", "git", "new_branches"}, {"request", "git", "push", "main", "--dry-run"}} {
		rc, out, errs := captureStd(t, func() int { return registry["request"](args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", args, rc, out, errs)
		}
		assertConvertFirst(t, "request", projectPath, errs)
		if got := readFile(t, projectPath); got != legacyProjectWithBrokered {
			t.Fatalf("%v wrote the project file:\n%s", args, got)
		}
		if strings.Contains(out, "yolo config apply") {
			t.Fatalf("%v named the approval step for a request it did not write: %q", args, out)
		}
	}
}

// A JSONC project file without `brokered` is written: the gate reads `requests` from it.
func TestRequestWritesAJSONCProjectWithoutBrokered(t *testing.T) {
	_, projectPath := convertFirstFixture(t, configdoc.TOML,
		"packs = [\"git\", \"github\"]\n\n[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n",
		configdoc.JSONC, `{}`)
	rc, out, errs := captureStd(t, func() int { return registry["request"]([]string{"request", "git", "push", "main"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	if strings.Contains(errs, "deprecated JSONC") {
		t.Fatalf("a JSONC project without brokered was refused: %q", errs)
	}
	if got := readFile(t, projectPath); !strings.Contains(got, `"requests"`) || !strings.Contains(got, "main") {
		t.Fatalf("the JSONC project file was not written:\n%s", got)
	}
}
