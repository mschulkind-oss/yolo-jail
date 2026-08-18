package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// fakeResolver backs LoopholeResolver with a fixed known set (the file-backed
// loopholes a real Resolver would discover).
type fakeResolver map[string]LoopholeInfo

func (f fakeResolver) Known() (map[string]LoopholeInfo, bool) { return f, true }

// R5 of loophole-packaging.md found knownHostServiceKeys contradicting the rest
// of the loophole machinery: the loader reads `description` and `doctor_cmd`
// (discover.go), and validateInlineService itself prefix-checks `jail_endpoint`
// — yet all three were "unknown key" errors on an inline entry. The census is
// reconciled: every key the loader reads validates.
func TestInlineLoopholeKeysLoaderReadsAreKnown(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {
		"description": "a test service",
		"command": ["/bin/true"],
		"env": {"A": "b"},
		"doctor_cmd": ["/bin/true", "--ok"],
		"preamble": true,
		"jail_endpoint": "`+paths.JailHostServicesDir+`/svc.endpoint"
	}}}`)
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — every key the loader reads must be a known inline key", errs)
	}
}

// validateScoped writes the given workspace yolo-jail.jsonc, then runs
// ValidateConfig over the merged map a real load would produce (user merged
// under workspace). Host behavior is pinned via YOLO_VERSION="" — this project
// develops inside its own jail, where the var is otherwise set.
func validateScoped(t *testing.T, userJSON, wsJSON string, resolver LoopholeResolver) (ws string, errs, warns []string) {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	ws = t.TempDir()
	if wsJSON == "" {
		wsJSON = "{}"
	} else {
		write(t, filepath.Join(ws, WorkspaceConfigName), wsJSON)
	}
	if userJSON == "" {
		userJSON = "{}"
	}
	merged := MergeConfig(decode(t, userJSON), decode(t, wsJSON))
	errs, warns = ValidateConfig(merged, ws, resolver)
	return ws, errs, warns
}

func containing(list []string, subs ...string) []string {
	var out []string
	for _, s := range list {
		all := true
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				all = false
				break
			}
		}
		if all {
			out = append(out, s)
		}
	}
	return out
}

// §4.3b (RULED): an inline entry — the `command` shape — is an INSTALL, legal
// only in the user config. In a workspace file it is an error naming the
// offending file and the exact fix.
func TestWorkspaceInlineLoopholeIsError(t *testing.T) {
	ws, errs, _ := validateScoped(t, "",
		`{"loopholes": {"svc": {"command": ["/bin/true"]}}}`, nil)
	hits := containing(errs, "config.loopholes.svc")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want exactly one for the inline entry", errs)
	}
	for _, want := range []string{
		filepath.Join(ws, WorkspaceConfigName),
		"user-scope only",
		"~/.config/yolo-jail/config.jsonc",
	} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("error %q does not name %q", hits[0], want)
		}
	}
}

// The same entry in the USER config alone is legal (that IS the install).
func TestUserInlineLoopholeIsClean(t *testing.T) {
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"svc": {"command": ["/bin/true"]}}}`, "", nil)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
	if hits := containing(warns, "loopholes"); len(hits) != 0 {
		t.Errorf("warnings = %v, want none about loopholes", hits)
	}
}

// Override-shape `env` reaches a FIRST-PARTY daemon's spawn environment
// (§4.1 finding 3 — LD_PRELOAD into the broker), so it is user-scope only too.
func TestWorkspaceOverrideEnvIsError(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	ws, errs, _ := validateScoped(t, "",
		`{"loopholes": {"svc": {"env": {"LD_PRELOAD": "/tmp/evil.so"}}}}`, resolver)
	hits := containing(errs, "config.loopholes.svc.env", "user-scope only")
	if len(hits) != 1 || !strings.Contains(hits[0], filepath.Join(ws, WorkspaceConfigName)) {
		t.Errorf("errors = %v, want one env scope error naming the workspace file", errs)
	}

	// The same override from the USER config is legal.
	_, errs, _ = validateScoped(t,
		`{"loopholes": {"svc": {"env": {"A": "b"}}}}`, "", resolver)
	if hits := containing(errs, "user-scope only"); len(hits) != 0 {
		t.Errorf("user-scope env drew a scope error: %v", hits)
	}
}

// Override-shape `doctor_cmd` on a manifest-backed loophole is not a SCOPE
// problem at all — it is refused at every scope, because a manifest loophole's
// doctor_cmd is fixed by its manifest and applyWorkspaceOverrides honors only
// enabled/env/jail_env. Saying "user-scope only — move this key to the user
// config" sent the reader to a dead end: the same key there answered "unknown
// key". One mistake, one message, and the message has to be actionable.
func TestOverrideDoctorCmdIsRefusedAtEitherScopeWithOneMessage(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	for _, tc := range []struct{ what, user, ws string }{
		{"workspace", "", `{"loopholes": {"svc": {"doctor_cmd": ["/tmp/evil", "--ok"]}}}`},
		{"user", `{"loopholes": {"svc": {"doctor_cmd": ["/tmp/evil", "--ok"]}}}`, ""},
	} {
		_, errs, _ := validateScoped(t, tc.user, tc.ws, resolver)
		hits := containing(errs, "config.loopholes.svc.doctor_cmd")
		if len(hits) != 1 {
			t.Fatalf("%s scope: errors = %v, want exactly one doctor_cmd error "+
				"(one mistake reported twice is the defect)", tc.what, errs)
		}
		if !strings.Contains(hits[0], "not overridable") {
			t.Errorf("%s scope: error %q must say the key is not overridable", tc.what, hits[0])
		}
		// The old advice, which did not work: moving the key to the user config
		// only changed which error you got.
		for _, dead := range []string{"user-scope only", "Move this key to"} {
			if strings.Contains(hits[0], dead) {
				t.Errorf("%s scope: error %q still gives the dead-end advice %q",
					tc.what, hits[0], dead)
			}
		}
		if strings.Contains(hits[0], "unknown key") {
			t.Errorf("%s scope: error %q is the generic unknown-key message, not the "+
				"explanation", tc.what, hits[0])
		}
	}
}

// The doctor_cmd of an INLINE service is a different key in a different shape:
// legal user-side (it is part of the install), and in a workspace file it draws
// the scope error whose fix — move the whole entry — does work.
func TestInlineDoctorCmdKeepsTheScopeError(t *testing.T) {
	ws, errs, _ := validateScoped(t, "",
		`{"loopholes": {"svc": {"command": ["/bin/true"], "doctor_cmd": ["/tmp/evil"]}}}`, nil)
	hits := containing(errs, "config.loopholes.svc", "user-scope only")
	if len(hits) != 1 || !strings.Contains(hits[0], filepath.Join(ws, WorkspaceConfigName)) {
		t.Fatalf("errors = %v, want the one install-scope error naming the workspace file", errs)
	}
	if !strings.Contains(hits[0], loopholeUserConfigHint) {
		t.Errorf("the install-scope error must still point at the user config: %q", hits[0])
	}
}

// A workspace doctor_cmd for a name that is NOT a manifest loophole keeps the
// move-it advice, because there the fix really is the user config: an inline
// entry with a command and a doctor_cmd is a legal install.
func TestWorkspaceDoctorCmdForUnknownNameStillSaysMoveIt(t *testing.T) {
	_, errs, _ := validateScoped(t, "",
		`{"loopholes": {"ghost": {"doctor_cmd": ["/tmp/evil"]}}}`, nil)
	if hits := containing(errs, "config.loopholes.ghost.doctor_cmd", "user-scope only"); len(hits) != 1 {
		t.Errorf("errors = %v, want the scope error for an unknown name's doctor_cmd", errs)
	}
}

// `enabled` and `jail_env` stay legal at BOTH scopes — that is the ruling.
func TestWorkspaceEnableAndJailEnvStayLegal(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	_, errs, _ := validateScoped(t, "",
		`{"loopholes": {"svc": {"enabled": true, "jail_env": {"FOO": "bar"}}}}`, resolver)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — enabled/jail_env are legal at either scope", errs)
	}
}

// RULED (OQ-LP2): a workspace enabling a loophole that is NOT installed is a
// FATAL error naming the loophole, the file that asked, and the user-config
// snippet that would install it — this error IS the human-in-the-loop moment.
// It replaces the every-launch "treating the entry as an override" warning.
func TestWorkspaceEnableUninstalledIsFatal(t *testing.T) {
	ws, errs, warns := validateScoped(t, "",
		`{"loopholes": {"ghost": {"enabled": true}}}`, nil)
	hits := containing(errs, "config.loopholes.ghost")
	if len(hits) != 1 {
		t.Fatalf("errors = %v, want exactly one for the uninstalled enable", errs)
	}
	for _, want := range []string{
		filepath.Join(ws, WorkspaceConfigName),
		"not installed",
		"~/.config/yolo-jail/config.jsonc",
		`"loopholes": {"ghost": {"command": [`,
	} {
		if !strings.Contains(hits[0], want) {
			t.Errorf("error %q does not carry %q", hits[0], want)
		}
	}
	if leftover := containing(warns, "treating the entry as an override"); len(leftover) != 0 {
		t.Errorf("the fallback warning must be REPLACED by the error, still got %v", leftover)
	}
}

// enabled:false (or any other override key) naming an unknown loophole is a
// harmless no-op and stays a warning, not an error.
func TestWorkspaceEnableFalseUnknownStaysWarning(t *testing.T) {
	_, errs, warns := validateScoped(t, "",
		`{"loopholes": {"ghost": {"enabled": false}}}`, nil)
	if hits := containing(errs, "config.loopholes.ghost"); len(hits) != 0 {
		t.Errorf("errors = %v, want none — disabling an unknown loophole is a no-op", hits)
	}
	if hits := containing(warns, "treating the entry as an override"); len(hits) != 1 {
		t.Errorf("warnings = %v, want the fallback override warning", warns)
	}
}

// §4.3b consequence 2: after the ruling, scope no longer protects the OFF
// direction, so a workspace-sourced enabled:false on an INSTALLED loophole
// must print one launch-time line naming the loophole AND the file (the only
// protection left for the broker default).
func TestWorkspaceDisableInstalledIsDisclosed(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	ws, errs, warns := validateScoped(t, "",
		`{"loopholes": {"svc": {"enabled": false}}}`, resolver)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — disabling is legal at workspace scope", errs)
	}
	hits := containing(warns, "config.loopholes.svc", "disabled by",
		filepath.Join(ws, WorkspaceConfigName))
	if len(hits) != 1 {
		t.Errorf("warnings = %v, want one disclosure naming loophole and file", warns)
	}
}

// The disclosure also covers a USER-CONFIG-inline install disabled by the
// workspace: the user's entry is an install too.
func TestWorkspaceDisableUserInlineIsDisclosed(t *testing.T) {
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"svc": {"command": ["/bin/true"]}}}`,
		`{"loopholes": {"svc": {"enabled": false}}}`, nil)
	if hits := containing(errs, "user-scope only"); len(hits) != 0 {
		t.Errorf("scope errors = %v, want none", hits)
	}
	if hits := containing(warns, "config.loopholes.svc", "disabled by"); len(hits) != 1 {
		t.Errorf("warnings = %v, want the disable disclosure", warns)
	}
}

// OQ-A13 (loophole-activation.md): the ON direction gets the same launch-time
// line the OFF direction has had, and for a sharper reason. R5 was written when a
// workspace `enabled: true` was INERT — manifests defaulted to on, so the only
// thing the weak, agent-editable scope could do was subtract. R2 flipped that
// default and made this key the ACTIVATION VERB, which left the newly dangerous
// direction as the silent one.
//
// R5 itself is NOT narrowed: enabling stays legal at workspace scope, so this must
// be a warning and never an error.
func TestWorkspaceEnableInstalledIsDisclosed(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	ws, errs, warns := validateScoped(t, "",
		`{"loopholes": {"svc": {"enabled": true}}}`, resolver)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — R5 stands: a workspace may still enable", errs)
	}
	hits := containing(warns, "config.loopholes.svc", "enabled by",
		filepath.Join(ws, WorkspaceConfigName))
	if len(hits) != 1 {
		t.Fatalf("warnings = %v, want one disclosure naming the loophole and the file", warns)
	}
	// The caveat, made testable. This ships as READABILITY: the line reports where
	// the switch lives, and nothing it can observe establishes that a human read the
	// file. A line that implied review would be worth less than no line, because the
	// reader would stop looking for the mechanism that actually asks.
	if !strings.Contains(hits[0], "agent-editable") {
		t.Errorf("disclosure %q does not say the file is agent-editable, which is the "+
			"whole reason the direction is worth a line", hits[0])
	}
	for _, claim := range []string{"approv", "review", "confirm", "consent"} {
		if strings.Contains(hits[0], claim) {
			t.Errorf("disclosure %q claims %q; this line is readability, not a record "+
				"that anyone signed off", hits[0], claim)
		}
	}
	// And it must not be mistakable for its opposite at a glance.
	if strings.Contains(hits[0], "disabled by") {
		t.Errorf("the ON disclosure reads like the OFF one: %q", hits[0])
	}
}

// A USER-scope enable produces NO line. Only a workspace enable does.
//
// This is the constraint that keeps the disclosure worth reading: the user config
// is not agent-editable, so an enable there is the ordinary way to turn a loophole
// on. A line under it would print on every launch for everybody, which is exactly
// how the one that matters gets skimmed past.
func TestUserScopeEnableIsNotDisclosed(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"svc": {"enabled": true}}}`, "", resolver)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — the user config is where enabling belongs", errs)
	}
	if hits := containing(warns, "enabled by"); len(hits) != 0 {
		t.Errorf("warnings = %v — a user-scope enable is not a workspace-scope one, and "+
			"disclosing it would put a line under every enabled loophole on every launch", hits)
	}
}

// A workspace file that touches an installed loophole WITHOUT setting `enabled`
// says nothing about the switch, so neither disclosure is due. `jail_env` is legal
// at workspace scope (§4.3b) and is the shape that makes this non-hypothetical.
//
// The seam has to distinguish "workspace scope said nothing" from "workspace scope
// said false"; reading a missing key as false would disclose a disable nobody wrote.
func TestWorkspaceEntryWithoutEnabledIsNotDisclosed(t *testing.T) {
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	_, errs, warns := validateScoped(t, "",
		`{"loopholes": {"svc": {"jail_env": {"K": "v"}}}}`, resolver)
	if len(errs) != 0 {
		t.Errorf("errors = %v, want none — jail_env is legal at workspace scope", errs)
	}
	if hits := containing(warns, "enabled by"); len(hits) != 0 {
		t.Errorf("warnings = %v, want no ON disclosure for an entry that sets no switch", hits)
	}
	if hits := containing(warns, "disabled by"); len(hits) != 0 {
		t.Errorf("warnings = %v — an absent `enabled` was read as false", hits)
	}
}

// In a jail every scope violation DOWNGRADES to a warning — same asymmetry as
// the retired `agents` key, for the same reason: /workspace is live-mounted,
// so an in-jail hard error would refuse every nested launch mid-migration.
func TestScopeViolationsDowngradeToWarningsInJail(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"loopholes": {"svc": {"command": ["/bin/true"]}, "ghost": {"enabled": true}}}`)
	merged := decode(t, `{"loopholes": {"svc": {"command": ["/bin/true"]}, "ghost": {"enabled": true}}}`)
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	errs, warns := ValidateConfig(merged, ws, nil)
	if hits := containing(errs, "config.loopholes"); len(hits) != 0 {
		t.Errorf("in-jail errors = %v, want none (must not brick nested preflight)", hits)
	}
	if hits := containing(warns, "config.loopholes.svc", "user-scope only"); len(hits) != 1 {
		t.Errorf("in-jail warnings = %v, want the downgraded inline violation", warns)
	}
	if hits := containing(warns, "config.loopholes.ghost", "not installed"); len(hits) != 1 {
		t.Errorf("in-jail warnings = %v, want the downgraded uninstalled-enable violation", warns)
	}
}

// The workspace-local file is workspace scope too (it lives in the workspace,
// so it is exactly as agent-editable), and later files win for `enabled`.
func TestWorkspaceLocalScopeAndEnablePrecedence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	resolver := fakeResolver{"svc": {Name: "svc", HasHostDaemon: true}}
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"loopholes": {"svc": {"enabled": false}}}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName), `{"loopholes": {"svc": {"enabled": true}}}`)
	merged := decode(t, `{"loopholes": {"svc": {"enabled": true}}}`)
	_, warns := ValidateConfig(merged, ws, resolver)
	if hits := containing(warns, "disabled by"); len(hits) != 0 {
		t.Errorf("warnings = %v — the local file re-enabled it, no disclosure due", hits)
	}

	// And the local file alone disabling it IS disclosed, naming the local file.
	write(t, filepath.Join(ws, WorkspaceConfigName), `{}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName), `{"loopholes": {"svc": {"enabled": false}}}`)
	merged = decode(t, `{"loopholes": {"svc": {"enabled": false}}}`)
	_, warns = ValidateConfig(merged, ws, resolver)
	if hits := containing(warns, "disabled by", WorkspaceLocalConfigName); len(hits) != 1 {
		t.Errorf("warnings = %v, want the disclosure naming the local file", warns)
	}
}

// WorkspaceLoopholeSwitches is the provenance seam `yolo check` uses to warn on a
// workspace-scope switch instead of green-passing it. It carries BOTH directions:
// it used to be WorkspaceDisabledLoopholes and dropped the `true` case on the floor
// (loophole-activation.md OQ-A13).
//
// The precedence half is what makes it a seam rather than a lookup: `enabled` is
// resolved the way the merge resolves it, so the file it names is the file that
// actually decided — including when the two workspace files disagree, which is the
// case a naive "first hit wins" gets backwards and a human then edits the wrong file.
func TestWorkspaceLoopholeSwitchesHelper(t *testing.T) {
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName),
		`{"loopholes": {"a": {"enabled": false}, "b": {"enabled": true},
		  "d": {"enabled": false}, "quiet": {"jail_env": {"K": "v"}}}}`)
	write(t, filepath.Join(ws, WorkspaceLocalConfigName),
		`{"loopholes": {"b": {"enabled": false}, "c": {"enabled": false},
		  "d": {"enabled": true}}}`)
	got := WorkspaceLoopholeSwitches(ws)

	tracked := filepath.Join(ws, WorkspaceConfigName)
	local := filepath.Join(ws, WorkspaceLocalConfigName)
	for _, tc := range []struct {
		name string
		WorkspaceLoopholeSwitch
	}{
		{"a", WorkspaceLoopholeSwitch{File: tracked, Enabled: false}},
		{"b", WorkspaceLoopholeSwitch{File: local, Enabled: false}},
		{"c", WorkspaceLoopholeSwitch{File: local, Enabled: false}},
		// The ON direction, and the one the old helper discarded: the local file
		// flipped the tracked file's `false` back to `true`, so both the value and
		// the file have to come from the LAST writer.
		{"d", WorkspaceLoopholeSwitch{File: local, Enabled: true}},
	} {
		if sw, ok := got[tc.name]; !ok || sw != tc.WorkspaceLoopholeSwitch {
			t.Errorf("%s: got %+v (present=%v), want %+v", tc.name, sw, ok, tc.WorkspaceLoopholeSwitch)
		}
	}
	// "workspace scope said nothing about the switch" must be ABSENT, not a zero
	// value: a `WorkspaceLoopholeSwitch{}` is indistinguishable from `enabled:
	// false`, and reading it as one would have every workspace that sets `jail_env`
	// disclose a disable nobody wrote.
	if sw, ok := got["quiet"]; ok {
		t.Errorf("an entry with no `enabled` key produced a switch %+v; only absence "+
			"can say that workspace scope left the decision alone", sw)
	}
	if len(got) != 4 {
		t.Errorf("got = %v, want exactly a, b, c, d", got)
	}
}

// jail_endpoint becoming a KNOWN key must not lose its prefix rule: the value
// still has to live under the jail host-services dir.
func TestInlineJailEndpointStillPrefixChecked(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {
		"command": ["/bin/true"],
		"jail_endpoint": "/tmp/evil.endpoint"
	}}}`)
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	var hit []string
	for _, e := range errs {
		if strings.Contains(e, "jail_endpoint") {
			hit = append(hit, e)
		}
	}
	if len(hit) != 1 || !strings.Contains(hit[0], "must start with "+paths.JailHostServicesDir+"/") {
		t.Errorf("jail_endpoint errors = %v, want exactly the prefix error (not an unknown-key error)", hit)
	}
}

// TestInlineLoopholePreambleMustBeBoolean: `preamble` is read through the
// schema's TRUTHINESS, where `"false"` — a non-empty string — is true. That is
// the one wrong value a human writes while believing they turned the key off,
// and it would be honored silently, so the type check refuses it here.
func TestInlineLoopholePreambleMustBeBoolean(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"svc": {
		"command": ["/bin/true"],
		"preamble": "false"
	}}}`)
	errs, _ := ValidateConfig(cfg, t.TempDir(), nil)
	if len(containing(errs, "preamble", "boolean")) == 0 {
		t.Errorf("errors = %v, want one naming preamble and 'boolean'", errs)
	}
}

// TestPreambleIsNotAnOverrideKey: `preamble` describes the connection yolo
// serves in front of a daemon the entry INSTALLS, and applyWorkspaceOverrides
// does not read it — so on an override of a manifest-backed loophole the key
// would declare nothing. That is the doctor_cmd failure mode, and the census
// answers it the same way: unknown here.
func TestPreambleIsNotAnOverrideKey(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	cfg := decode(t, `{"loopholes": {"known-one": {"preamble": false}}}`)
	resolver := fakeResolver{"known-one": {HasHostDaemon: true}}
	errs, _ := ValidateConfig(cfg, t.TempDir(), resolver)
	if len(containing(errs, "preamble")) == 0 {
		t.Errorf("errors = %v, want the key refused on an override", errs)
	}
}
