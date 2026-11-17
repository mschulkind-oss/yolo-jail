package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func callRootsRepairFiles(t *testing.T, user, project string) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_IN_JAIL", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(userFile, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	t.Chdir(ws)
	file := filepath.Join(ws, config.WorkspaceTOMLConfigName)
	if err := os.WriteFile(file, []byte(project), 0600); err != nil {
		t.Fatal(err)
	}
	return userFile, file
}

func TestConfigEditCallRootsWithDeclaredReadAllowance(t *testing.T) {
	for _, field := range []string{"always", "on_request"} {
		t.Run(field, func(t *testing.T) {
			_, file := callRootsRepairFiles(t, "packs=[\"aws\"]\n[allow.aws]\n"+field+"=[\"read\"]\n", "")
			for _, args := range [][]string{
				{"check"},
				{"add", "call_roots.aws", "ce://host-login", "--project"},
				{"set", "network.mode", "bridge", "--project"},
				{"set", "call_roots.aws", "[\"s3://bkt/team/\"]", "--project"},
				{"unset", "call_roots.aws", "--project"},
				{"check"},
			} {
				rc, out, errs := runMainCaptured(t, append([]string{"config"}, args...)...)
				if rc != 0 {
					t.Fatalf("%v rc=%d out=%s err=%s", args, rc, out, errs)
				}
			}
			data, err := os.ReadFile(file)
			if err != nil || !strings.Contains(string(data), "aws = []") {
				t.Fatalf("unset data=%s err=%v", data, err)
			}
		})
	}
}

func TestConfigEditReadAllowanceRejectsUndeclaredAndScopedRead(t *testing.T) {
	for _, token := range []string{"undeclared", "all.read", "working_set.read", "all.unknown_cell"} {
		t.Run(token, func(t *testing.T) {
			_, file := callRootsRepairFiles(t, "packs=[\"aws\"]\n[allow.aws]\nalways=[\""+token+"\"]\n", "")
			rc, out, errs := runMainCaptured(t, "config", "add", "call_roots.aws", "ce://host-login", "--project")
			if rc == 0 {
				t.Fatalf("invalid allowance accepted: %s%s", out, errs)
			}
			data, err := os.ReadFile(file)
			if err != nil || len(data) != 0 {
				t.Fatalf("refusal wrote file: %q %v", data, err)
			}
		})
	}
}

func TestConfigWhyCallRootsUsesCanonicalValueAndWriters(t *testing.T) {
	_, _ = callRootsRepairFiles(t, "packs=[\"aws\"]\n", "[call_roots]\naws=[\"ce://host-login\"]\n")
	rc, out, errs := runMainCaptured(t, "config", "why", "call_roots.aws")
	if rc != 0 || !strings.Contains(out, "ce://host-login") || !strings.Contains(out, "yolo-jail.toml:2:") || strings.Contains(out, "is not set") {
		t.Fatalf("why rc=%d out=%s err=%s", rc, out, errs)
	}
}

func TestConfigEditUnknownCallRootsNamesAuthoredPath(t *testing.T) {
	_, _ = callRootsRepairFiles(t, "packs=[\"aws\"]\n", "")
	rc, out, errs := runMainCaptured(t, "config", "add", "call_roots.nope", "ce://host-login", "--project")
	text := out + errs
	if rc == 0 || !strings.Contains(text, "config.call_roots.nope") || !strings.Contains(text, "yolo-jail.toml:") {
		t.Fatalf("rc=%d out=%s err=%s", rc, out, errs)
	}
}

func TestConfigCheckDualCallRootsNamesBothWriters(t *testing.T) {
	_, file := callRootsRepairFiles(t, "packs=[\"aws\"]\n", "[call_roots]\naws=[\"ce://host-login\"]\n")
	local := filepath.Join(filepath.Dir(file), "yolo-jail.local.jsonc")
	if err := os.WriteFile(local, []byte(`{"brokered":{"aws":{"roots":["s3://bkt"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runMainCaptured(t, "config", "check")
	if rc == 0 || !strings.Contains(out+errs, "yolo-jail.toml:") || !strings.Contains(out+errs, "yolo-jail.local.jsonc:") {
		t.Fatalf("rc=%d out=%s err=%s", rc, out, errs)
	}
}

func TestConfigCheckAndMigrationRejectLegacyCallRootsUnchanged(t *testing.T) {
	_, file := callRootsRepairFiles(t, "packs=[\"aws\"]\n", "")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(filepath.Dir(file), "yolo-jail.jsonc")
	original := `{"call_roots":{"aws":["ce://host-login"]}}`
	if err := os.WriteFile(legacy, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check"}, {"migrate", "--dry-run"}, {"migrate", "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			rc, out, errs := runMainCaptured(t, append([]string{"config"}, args...)...)
			if rc == 0 || !strings.Contains(out+errs, "brokered.<source>.roots") {
				t.Fatalf("%v rc=%d out=%s err=%s", args, rc, out, errs)
			}
			data, err := os.ReadFile(legacy)
			if err != nil || string(data) != original {
				t.Fatalf("original changed: %q %v", data, err)
			}
			for _, unexpected := range []string{file, legacy + ".pre-migrate"} {
				if _, err := os.Lstat(unexpected); !os.IsNotExist(err) {
					t.Fatalf("refusal created %s: %v", unexpected, err)
				}
			}
		})
	}
}
