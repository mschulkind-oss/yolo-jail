package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostManagementUnsetIsAssert is OQ-CO2's whole shape: the unset state is `assert`,
// silently, so upgrade day changes nothing for anyone and nobody is interrupted to be told
// that. The `declared` half is the one thing that separates unset from a written "assert" —
// `yolo apply --sealed` is the only place the difference bites (§4.3 item 3).
func TestHostManagementUnsetIsAssert(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// No file at all. An absent CONFIG is not an unreadable one: nothing is broken, the key
	// simply has no value, so the default carries it.
	if mode, declared := HostManagementDeclared(); mode != HostManagementAssert || declared {
		t.Errorf("with no user config = (%q, %v), want (\"assert\", false)", mode, declared)
	}
	write(t, userCfgPath, `{"packs": []}`)
	if mode, declared := HostManagementDeclared(); mode != HostManagementAssert || declared {
		t.Errorf("with the key absent = (%q, %v), want (\"assert\", false)", mode, declared)
	}
	for _, want := range KnownHostManagements {
		write(t, userCfgPath, `{"host_management": "`+string(want)+`"}`)
		mode, declared := HostManagementDeclared()
		if mode != want || !declared {
			t.Errorf("with %q written = (%q, %v), want (%q, true)", want, mode, declared, want)
		}
		if HostManagementMode() != want {
			t.Errorf("HostManagementMode() = %q, want %q", HostManagementMode(), want)
		}
	}
}

// TestHostManagementUnreadableConfigIsNone is the FAIL DIRECTION, and it is the half a shared
// helper cannot express: UserScopeConfigOrEmpty returns an empty map for BOTH "no file" and
// "unreadable file", and §4.2 gives those two opposite answers. The test above pins the first
// (assert); this pins the second (none). Both have to hold at once, or the reader is built on
// the helper that cannot tell them apart.
//
// ⚠ It is NOT enough that a broken config yields "not assert": the direction is per key.
// `agent_updates` reads user scope through the same boundary and deliberately fails OPEN.
func TestHostManagementUnreadableConfigIsNone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, userCfgPath, `{"host_management": "own`)
	if mode, declared := HostManagementDeclared(); mode != HostManagementNone || declared {
		t.Errorf("with an unparseable user config = (%q, %v), want (\"none\", false) — a "+
			"declaration nobody could read has granted no write claim", mode, declared)
	}
	// A present but unusable VALUE is the same thing one level in: the declaration could not
	// be read. Never silently `assert`, which would make a typo indistinguishable from a
	// working declaration.
	for _, bad := range []string{`{"host_management": "asert"}`, `{"host_management": true}`,
		`{"host_management": {"claude": "own"}}`} {
		write(t, userCfgPath, bad)
		if mode, declared := HostManagementDeclared(); mode != HostManagementNone || declared {
			t.Errorf("with %s = (%q, %v), want (\"none\", false)", bad, mode, declared)
		}
	}
}

// TestHostManagementIgnoresWorkspaceScope is the SECURITY test, and the claim it guards is
// the largest of the four user-scope host keys: /workspace is bind-mounted rw, so a
// repository — or an agent editing one — can set any workspace key. Read from the merged
// config, `host_management: own` in a cloned repo would declare yolo the owner of its user's
// real ~/.claude/settings.json. Reading user scope directly makes that INEXPRESSIBLE; this
// asserts the value is never consulted, not merely rejected.
func TestHostManagementIgnoresWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_management": "own"}`)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, userCfgPath, `{"packs": []}`)

	if mode, declared := HostManagementDeclared(); mode != HostManagementAssert || declared {
		t.Fatalf("a WORKSPACE config set host_management to %q (declared=%v) — a cloned "+
			"repository can now declare itself the owner of its user's real home", mode, declared)
	}
	write(t, userCfgPath, `{"host_management": "none"}`)
	if HostManagementMode() != HostManagementNone {
		t.Error("a workspace \"own\" overrode the user's explicit \"none\"")
	}
}

// TestValidateHostManagementWorkspaceScopeErrors is the defense-in-depth half: the value is
// already inert, and saying so loudly is what keeps it from LOOKING like it worked.
func TestValidateHostManagementWorkspaceScopeErrors(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_management": "own"}`)

	var errs []string
	validateHostManagement(decode(t, `{"host_management": "own"}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want an error for a workspace-scoped host_management")
	}
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") {
		t.Errorf("errors = %v, want one saying 'user-scope only'", errs)
	}
}

func TestValidateHostManagementUserScopeQuiet(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	for _, v := range KnownHostManagements {
		var errs []string
		validateHostManagement(decode(t, `{"host_management": "`+string(v)+`"}`), ws, &errs)
		if len(errs) != 0 {
			t.Errorf("user-scoped host_management %q produced errors: %v", v, errs)
		}
	}
}

// TestValidateHostManagementTypeCheck pins the three values as the accepted set — OQ-CO1's
// ruling that there are THREE, not a boolean.
func TestValidateHostManagementTypeCheck(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	for _, bad := range []string{`{"host_management": "yes"}`, `{"host_management": true}`,
		`{"host_management": ["own"]}`} {
		var errs []string
		validateHostManagement(decode(t, bad), ws, &errs)
		if len(errs) == 0 {
			t.Fatalf("%s produced no error, want one naming the accepted values", bad)
		}
		for _, want := range KnownHostManagements {
			if !strings.Contains(errs[0], string(want)) {
				t.Errorf("%s: error %q never names the accepted value %q", bad, errs[0], want)
			}
		}
	}
	for _, ok := range []string{`{"host_management": null}`, `{}`} {
		var errs []string
		validateHostManagement(decode(t, ok), ws, &errs)
		if len(errs) != 0 {
			t.Errorf("%s produced errors: %v", ok, errs)
		}
	}
}

// TestValidateHostManagementIsReachedFromValidateConfig pins the CALL SITE, not the
// validator: deleting the `validateHostManagement(...)` line from ValidateConfig leaves every
// test above green while a workspace-scoped key goes back to looking accepted.
func TestValidateHostManagementIsReachedFromValidateConfig(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_management": "own"}`)

	errs, _ := ValidateConfig(decode(t, `{"host_management": "own"}`), ws, nil)
	if !strings.Contains(strings.Join(errs, "\n"), hostManagementKey) {
		t.Errorf("ValidateConfig does not reach validateHostManagement — a workspace value "+
			"is inert and nothing says so; got %v", errs)
	}
	// The shape half through the same call site: a bad VALUE must be reported too, or the
	// line could be present and the validator gutted.
	errs, _ = ValidateConfig(decode(t, `{"host_management": "asert"}`), ws, nil)
	if !strings.Contains(strings.Join(errs, "\n"), hostManagementKey) {
		t.Errorf("ValidateConfig accepted a misspelled host_management value; got %v", errs)
	}
}

// TestHostManagementIsAKnownKey: an unlisted top-level key is a hard "unknown key" error, so
// the schema set and the validator have to agree or the key is unusable.
func TestHostManagementIsAKnownKey(t *testing.T) {
	if _, known := knownTopLevelConfigKeys[hostManagementKey]; !known {
		t.Error("host_management is not in knownTopLevelConfigKeys")
	}
}

// TestHostManagementIsNeverInherited pins the census VALUE through FilterInherit, which is
// the call site that acts on it — TestInheritCensusIsTotal only proves the key is
// CLASSIFIED, and would stay green if it were classified into both scopes.
//
// The classification is NEITHER because the key's referent rebinds: "the home yolo renders
// into" is the human's laptop on the host and /home/agent in a jail, so an inherited `own`
// would hand a throwaway home an ownership contract nobody declared about it.
func TestHostManagementIsNeverInherited(t *testing.T) {
	effective := decode(t, `{"host_management": "own", "packs": ["claude"]}`)
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		out, unknown := FilterInherit(effective, scope)
		if _, present := out.Get(hostManagementKey); present {
			t.Errorf("the %s scope emits host_management — a nested jail would read an "+
				"ownership declaration about a home that is not the one it names", scope)
		}
		if len(unknown) != 0 {
			t.Errorf("%s scope reported unknown keys %v — host_management must be CLASSIFIED, "+
				"not merely absent", scope, unknown)
		}
	}
}
