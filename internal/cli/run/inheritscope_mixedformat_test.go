package run

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// resolvedEnv is what the env resolver every launch path calls makes of cfg: each variable's
// value, a removal as "<removed>".
func resolvedEnv(t *testing.T, ws string, cfg *jsonx.OrderedMap) map[string]string {
	t.Helper()
	env, removals := config.ResolveEnvSourcesFull(ws, cfg, nil)
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

// A TOML user config with packs under a legacy JSONC workspace config with env_sources refused
// every launch: the packs entry could not cross as JSONC (the jail's legacy reader would misjudge
// it), and env_sources had no TOML spelling. Piecemeal migration always works (RC-D50), so
// through the launch's own caller the nested scope crosses as TOML for every env_sources shape,
// and resolves exactly the env the host's config does. It fails if the read stops lowering
// env_sources (the launch refuses again), or lowers it into a different order or meaning.
func TestMixedFormatEnvSourcesCrossAsTOMLForEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name, userTOML, sources string
	}{
		{"the owner's one inline table", "", `[{"HF_HOME": "/ctx/orca-dev/hf"}]`},
		{"paths only", "", `["a.env", "b.env"]`},
		{"trailing inline tables", "", `["a.env", {"A": "inline", "NEW": "n"}, {"B": null}]`},
		{"inline before a file", "", `[{"A": "inline", "ONLY": "o", "B": null}, "a.env", {"C": null}]`},
		{"only inline tables", "", `[{"A": "1"}, {"A": null, "Z": "z"}]`},
		{"under user env.files", "[env]\nfiles = ['{{ws}}/b.env']\n", `[{"B": "inline"}, "a.env"]`},
		{"under user env.sources", "[env]\nsources = ['{{ws}}/b.env', { set = { D = 'user' } }]\n", `["a.env", {"D": null}]`},
		{"under user env.set and unset", "[env]\nset = { C = 'user' }\nunset = ['A']\n", `["a.env", {"C": "inline", "D": null}]`},
		{"a null over user lists", "[env]\nsources = ['{{ws}}/b.env', { set = { D = 'user' } }]\nset = { C = 'user' }\n", `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, wsState := inheritHome(t, "")
			o := inheritOptions(t)
			for name, body := range map[string]string{"a.env": "A=a\nB=a\nC=a\n", "b.env": "B=b\nD=b\n"} {
				if err := os.WriteFile(filepath.Join(o.Workspace, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			userTOML := "packs = ['claude']\n" + strings.ReplaceAll(tc.userTOML, "{{ws}}", o.Workspace)
			if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml"), []byte(userTOML), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(o.Workspace, "yolo-jail.jsonc"), []byte(`{"env_sources": `+tc.sources+"}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			effective, _, err := o.effectiveConfigForInherit()
			if err != nil {
				t.Fatal(err)
			}
			want := resolvedEnv(t, o.Workspace, effective)

			args, err := o.userConfigMountArgsForHome("podman", wsState, nil)
			if err != nil {
				t.Fatalf("the launch refused a mixed TOML/JSONC config: %v", err)
			}
			body, source, ok := mountedBody(t, args, "/home/agent/.config/yolo-jail/inherited-launch.toml")
			if !ok || filepath.Ext(source) != ".toml" {
				t.Fatalf("the nested scope was not delivered as TOML; argv=%v", args)
			}
			doc, err := configdoc.Parse([]byte(body), configdoc.TOML)
			if err != nil {
				t.Fatalf("delivered nested scope is not TOML: %v\n%s", err, body)
			}
			if _, present := doc.Values.Get("env_sources"); present {
				t.Errorf("env_sources crossed in its legacy spelling:\n%s", body)
			}
			if _, present := doc.Values.Get("packs"); !present {
				t.Errorf("the user packs did not cross:\n%s", body)
			}
			if got := resolvedEnv(t, o.Workspace, doc.Values); !maps.Equal(got, want) {
				t.Errorf("the delivered scope resolves a different env:\n  host %v\n  jail %v\n%s", want, got, body)
			}
		})
	}
}

// A legacy null that drops what a lower layer wrote has a TOML spelling where the readable schema
// gives one (an empty list, or "off"), so under a TOML user config with packs the nested scope
// still crosses as TOML, through the launch's own caller, carrying that spelling. It fails if the
// delivery stops lowering nulls (config.LowerLegacyNulls), which refused these launches.
func TestMixedFormatNullsCrossInTheirTOMLRemovalSpelling(t *testing.T) {
	_, wsState := inheritHome(t, "")
	o := inheritOptions(t)
	userTOML := "packs = ['claude']\n[install]\nnix = ['hello']\n[mcp_servers.foo]\ncommand = ['foo-mcp']\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml"), []byte(userTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Workspace, "yolo-jail.jsonc"), []byte(`{"packages": null, "mcp_servers": {"foo": null}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := o.userConfigMountArgsForHome("podman", wsState, nil)
	if err != nil {
		t.Fatalf("the launch refused a mixed TOML/JSONC config: %v", err)
	}
	body, source, ok := mountedBody(t, args, "/home/agent/.config/yolo-jail/inherited-launch.toml")
	if !ok || filepath.Ext(source) != ".toml" {
		t.Fatalf("the nested scope was not delivered as TOML; argv=%v", args)
	}
	doc, err := configdoc.Parse([]byte(body), configdoc.TOML)
	if err != nil {
		t.Fatalf("delivered nested scope is not TOML: %v\n%s", err, body)
	}
	install, _ := doc.Values.Get("install")
	nix, _ := install.(*jsonx.OrderedMap).Get("nix")
	if list, ok := nix.([]any); !ok || len(list) != 0 {
		t.Errorf("the packages null crossed as %v, want install.nix = []:\n%s", nix, body)
	}
	servers, _ := doc.Values.Get("mcp_servers")
	if foo, _ := servers.(*jsonx.OrderedMap).Get("foo"); foo != "off" {
		t.Errorf("the mcp_servers null crossed as %v, want \"off\":\n%s", foo, body)
	}
}

// A null with no TOML removal spelling yet (RC-D50's open gaps) cannot cross in either format
// under a TOML user config with packs. The refusal names both sides, the legacy file's own line,
// and `yolo config migrate`, which converts that file where it can and names what it cannot.
func TestMixedFormatGapNamesTheLegacySettingAndMigrate(t *testing.T) {
	_, wsState := inheritHome(t, "")
	o := inheritOptions(t)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml"), []byte("packs = ['claude']\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wsFile := filepath.Join(o.Workspace, "yolo-jail.jsonc")
	if err := os.WriteFile(wsFile, []byte(`{"lsp_servers": {"foo": null}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := o.userConfigMountArgsForHome("podman", wsState, nil)
	if err == nil {
		t.Fatal("a null with no TOML spelling crossed")
	}
	for _, want := range []string{"config.packs[0]", "config.lsp_servers.foo", wsFile + ":1:", "yolo config migrate", "cannot yet write in TOML"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
}
