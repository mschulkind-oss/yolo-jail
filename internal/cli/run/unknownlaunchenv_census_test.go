package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// notLaunchSettings is every YOLO_ name the module spells that a `yolo` launch must still
// warn about, with the reason it is not one a launch reads or inherits. A name the module
// spells is either recognized by knownLaunchYOLOEnvNames or listed here; the census below
// fails on any other, so a new setting cannot be honored by a launch and warned about by the
// same launch (YOLO_CLAUDE_CREDENTIAL_VIEW was, docs/lab-reports/
// 2026-10-29-mac-native-witnesses-runbook.md §4.3).
//
// A name belongs HERE only when a launch neither reads it nor inherits it from a jail it was
// started in. Anything a launch writes into a container's environment is inherited by a
// nested launch from that container and belongs in the recognized list instead.
var notLaunchSettings = func() map[string]string {
	const (
		macosUser  = "macos-user sandbox/bootstrap contract; a macos-user sandbox cannot launch a jail (canNest)"
		launcher   = "read by a generated in-jail program launcher for one invocation, not by `yolo`"
		internal   = "handoff between yolo's own processes, consumed before any launch reads the environment"
		hook       = "environment yolo gives a user's broker hook program"
		testOnly   = "test-only override of a host daemon, never read by a launch"
		daemonHalf = "a host or in-jail daemon's own environment, never a launch's"
	)
	m := map[string]string{
		"YOLO_DARWIN_CACHE_ALIASES": macosUser, "YOLO_DARWIN_CACHE_RELOCATIONS": macosUser,
		"YOLO_DARWIN_ENV_FILE": macosUser, "YOLO_DARWIN_HOME_OVERLAY": macosUser,
		"YOLO_DARWIN_HOME_SIDECAR": macosUser, "YOLO_DARWIN_LOGIN_PATH": macosUser,
		"YOLO_DARWIN_MACHINE_HOME": macosUser, "YOLO_DARWIN_REAL_HOME": macosUser,
		"YOLO_DARWIN_SHARED_HOME": macosUser, "YOLO_DARWIN_WORKSPACE": macosUser,
		"YOLO_CTX_STAGED_PARENT": macosUser, "YOLO_GIT": macosUser, "YOLO_GIT_EMAIL": macosUser,
		"YOLO_GIT_NAME": macosUser, "YOLO_GLOBAL_GITIGNORE": macosUser, "YOLO_WORKSPACE": macosUser,
		"YOLO_HOME_CONTENT_DELIVERY": macosUser, "YOLO_INTERNAL_SANDBOX_TMPDIR_CONTEXT": macosUser,
		"YOLO_INTERNAL_SANDBOX_TMPDIR_NAMES": macosUser, "YOLO_MACOS_USER_TMPDIR_CONTEXT": macosUser,

		"YOLO_INSTALL_ONLY": launcher, "YOLO_PACK_UPDATE": launcher, "YOLO_NO_LAUNCH_FLAGS": launcher,

		"YOLO_IO_PRIORITY_REEXEC": internal, "YOLO_UPDATE_REEXEC": internal,
		"YOLO_INVOCATION_CWD": internal, "YOLO_JAIL_DAEMON_READY_FD": internal,
		"YOLO_HOST_SERVICE_ATTEMPT": daemonHalf, "YOLO_HOST_SERVICE_HALF": daemonHalf,
		"YOLO_HOST_SERVICE_INPUT": daemonHalf, "YOLO_HOST_SERVICE_LIFELINE_FD": daemonHalf,
		"YOLO_HOST_SERVICE_LISTEN_FDS": daemonHalf, "YOLO_HOST_SERVICE_NAME": daemonHalf,
		"YOLO_HOST_SERVICE_REASON_FD": daemonHalf, "YOLO_DOCTOR_SERVICE_NOT_STARTED_ON": daemonHalf,
		"YOLO_NIX_ROOTS_LOG": daemonHalf, "YOLO_JOURNALCTL_PASSTHROUGH_HELP": daemonHalf,
		"YOLO_OPENAI_AUTH_HOST_SOCKET": daemonHalf, "YOLO_BUILDER_PUBKEY": daemonHalf,

		"YOLO_REQUEST_EXPIRES": hook, "YOLO_REQUEST_ID": hook, "YOLO_REQUEST_JAIL": hook,
		"YOLO_REQUEST_REPO": hook, "YOLO_REQUEST_SET": hook, "YOLO_REQUEST_SUMMARY": hook,
		"YOLO_REQUEST_WORKSPACE": hook, "YOLO_EVENT": hook, "YOLO_SOURCE": hook, "YOLO_BIN": hook,

		"YOLO_BROKER_STATE_DIR": testOnly, "YOLO_BROKER_UPSTREAM_URL": testOnly,

		"YOLO_REPO_ROOT": "retired; the launch refuses it with its own message naming the replacement",
	}
	// Loophole endpoints: recognized through the SELECTED pack that ships the loophole
	// (TestKnownLaunchYOLOEnvNamesIncludeSelectedLoopholeEndpoints), never as fixed names.
	for _, name := range []string{"YOLO_SERVICE_HOST_PROCESSES_ENDPOINT", "YOLO_SERVICE_JOURNAL_ENDPOINT",
		"YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT"} {
		m[name] = "a loophole's endpoint, recognized when the pack shipping it is selected"
	}
	return m
}()

var yoloEnvName = regexp.MustCompile(`^YOLO_[A-Z0-9_]*[A-Z0-9]`)

// moduleYOLOEnvNames returns every YOLO_ environment name spelled by a string literal in the
// module's non-test sources: a whole-literal name (`"YOLO_X"`, which is how every constant is
// defined) or a `"YOLO_X=` assignment. A literal ending in `_` is a prefix, not a name.
func moduleYOLOEnvNames(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				text, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				name := yoloEnvName.FindString(text)
				if name == "" {
					return true
				}
				if rest := text[len(name):]; rest != "" && rest != "=" && !strings.HasPrefix(rest, "=") {
					return true
				}
				if _, seen := found[name]; !seen {
					rel, _ := filepath.Rel(root, path)
					found[name] = rel
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return found
}

// TestEveryYOLOEnvNameTheModuleSpellsIsClassified is the structural guard for the unknown-
// setting warning: every YOLO_ name the module spells is either recognized by a launch or
// listed in notLaunchSettings with its reason. It fails on a NEW name until someone decides
// which, so a launch can no longer honor a setting and warn its user to unset it.
func TestEveryYOLOEnvNameTheModuleSpellsIsClassified(t *testing.T) {
	known := knownLaunchYOLOEnvNames(nil, nil)
	spelled := moduleYOLOEnvNames(t)
	if len(spelled) < 50 {
		t.Fatalf("the census found only %d YOLO_ names; it no longer measures the module", len(spelled))
	}
	for name, where := range spelled {
		_, excluded := notLaunchSettings[name]
		recognized := slices.Contains(known, name)
		switch {
		case recognized && excluded:
			t.Errorf("%s is both recognized and listed in notLaunchSettings; drop it from one", name)
		case !recognized && !excluded:
			t.Errorf("%s (spelled in %s) is neither a recognized launch setting nor listed in "+
				"notLaunchSettings. If a launch reads it, or writes it into a container's "+
				"environment, add it to knownLaunchYOLOEnvNames by its constant; otherwise list "+
				"it in notLaunchSettings with the reason a launch must warn about it", name, where)
		}
	}
	for name := range notLaunchSettings {
		if _, ok := spelled[name]; !ok {
			t.Errorf("notLaunchSettings lists %s, which no source spells any more; delete the entry", name)
		}
	}
}

// The runbook's false warning (§4.3): the launch obeys the credential-view switch, so it must
// not tell the user to unset it.
func TestTheClaudeCredentialViewSwitchIsARecognizedLaunchSetting(t *testing.T) {
	if !slices.Contains(knownLaunchYOLOEnvNames(nil, nil), claudeview.SwitchEnv) {
		t.Fatalf("%s is honored by the launch but not recognized, so the launch warns the user "+
			"to unset the switch it just obeyed", claudeview.SwitchEnv)
	}
}

// A loophole's endpoint variable is in the environment of every jail that ran it (this one's
// YOLO_SERVICE_GIT_BROKER_ENDPOINT), so a nested launch inherits it. It is recognized through
// the selected pack that ships the loophole, like a service's.
func TestKnownLaunchYOLOEnvNamesIncludeSelectedLoopholeEndpoints(t *testing.T) {
	selected := &packload.Pack{Name: "selected", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindLoophole, From: "loopholes/acme-proxy"},
	}}}
	want := paths.ServiceEnvVarPrefix + "ACME_PROXY" + paths.ServiceEnvVarSuffix
	if !slices.Contains(knownLaunchYOLOEnvNames([]*packload.Pack{selected}, nil), want) {
		t.Errorf("selected pack's loophole endpoint %s is not recognized", want)
	}
	if slices.Contains(knownLaunchYOLOEnvNames(nil, nil), want) {
		t.Errorf("%s is recognized with no pack selected", want)
	}
	inert := paths.LoopholeInertEnv("acme-proxy")
	if !slices.Contains(knownLaunchYOLOEnvNames([]*packload.Pack{selected}, nil), inert) {
		t.Errorf("selected pack's loophole inert variable %s is not recognized", inert)
	}
	if slices.Contains(knownLaunchYOLOEnvNames(nil, nil), inert) {
		t.Errorf("%s is recognized with no pack selected", inert)
	}
}
