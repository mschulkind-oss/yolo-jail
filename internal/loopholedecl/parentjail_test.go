package loopholedecl_test

// parentjail_test.go pins the `inherit_from_parent_jail` block (loopholedecl/parentjail.go;
// docs/design/sso-backed-bedrock.md SSO-D2): the variables a nested launch may take a loophole's
// pointer from, and the line it prints when it does, carried verbatim and refused where the
// block could inherit silently or for a loophole with no pointer to stand in for.

import (
	"slices"
	"strings"
	"testing"
)

func inheritingManifest(block any, jail bool) map[string]any {
	m := map[string]any{"name": "adapter", "description": "x", "inherit_from_parent_jail": block}
	if jail {
		m["jail_daemon"] = doorwayDaemon(nil)
	}
	return m
}

func TestAParentJailBlockIsCarriedVerbatim(t *testing.T) {
	m, err := decodeMap(t, "adapter", inheritingManifest(map[string]any{
		"vars": []any{"POINTER_URI", "POINTER_TOKEN"}, "disclose": "uses the parent's own pointer",
	}, true))
	if err != nil {
		t.Fatal(err)
	}
	if m.InheritFromParentJail == nil {
		t.Fatal("the block decoded to nil")
	}
	if !slices.Equal(m.InheritFromParentJail.Vars, []string{"POINTER_URI", "POINTER_TOKEN"}) ||
		m.InheritFromParentJail.Disclose != "uses the parent's own pointer" {
		t.Errorf("block = %+v", *m.InheritFromParentJail)
	}
}

func TestAManifestWithoutTheBlockInheritsNothing(t *testing.T) {
	m, err := decodeMap(t, "adapter", map[string]any{"name": "adapter", "description": "x",
		"jail_daemon": doorwayDaemon(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if m.InheritFromParentJail != nil {
		t.Errorf("a manifest saying nothing inherits %+v", *m.InheritFromParentJail)
	}
}

func TestAParentJailBlockIsRefusedWhereItCouldInheritSilently(t *testing.T) {
	ok := map[string]any{"vars": []any{"POINTER_URI"}, "disclose": "says so"}
	for name, tc := range map[string]struct {
		block any
		jail  bool
		want  string
	}{
		"not a mapping":   {"yes", true, "must be a mapping"},
		"no jail daemon":  {ok, false, "needs a 'jail_daemon'"},
		"no vars":         {map[string]any{"disclose": "says so"}, true, "non-empty list"},
		"empty vars":      {map[string]any{"vars": []any{}, "disclose": "says so"}, true, "non-empty list"},
		"bad name":        {map[string]any{"vars": []any{"NOT A NAME"}, "disclose": "says so"}, true, "not an environment variable name"},
		"twice":           {map[string]any{"vars": []any{"A", "A"}, "disclose": "says so"}, true, "twice"},
		"no disclosure":   {map[string]any{"vars": []any{"A"}}, true, "'inherit_from_parent_jail.disclose'"},
		"blank":           {map[string]any{"vars": []any{"A"}, "disclose": "  "}, true, "'inherit_from_parent_jail.disclose'"},
		"misspelled key":  {map[string]any{"vars": []any{"A"}, "disclose": "x", "discloze": "y"}, true, "unknown key"},
		"control in line": {map[string]any{"vars": []any{"A"}, "disclose": "x\x1b[2J"}, true, "control"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeMap(t, "adapter", inheritingManifest(tc.block, tc.jail))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
