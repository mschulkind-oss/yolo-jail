package cli

// hostwiretables_test.go pins docs/design/agent-footer.md FT-D2 (OQ-FT15): `yolo host --` exports
// YOLO_PROVIDERS, YOLO_PROFILES and YOLO_USE_PROFILES as it composed them for its one agent, so the
// footer's "env first" read (OQ-FT6) names a one-launch `-p` instead of the config's selection.
// Each cell runs the real front door (hostMain) to the exec, with the exec replaced, and reads the
// environment the agent would have been handed; the footer cell then runs the statusLine command
// `yolo host apply` filled, through /bin/sh, in exactly that environment. Deleting the vars step
// in composeHostVarsWith fails every cell here.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// hostExports reports whether a `yolo host env` script exports name, reading the `export K=` lines
// alone. A plain substring test cannot answer it any more: YOLO_PROVIDERS names every provider's
// credential variable as data (`api_key_env_name`), so the name appears in a script that exports
// no such variable.
func hostExports(script, name string) bool {
	return slices.Contains(hostExportKeys(script), name)
}

// hostExportsPrefix is hostExports for every name with prefix: the first exported one, or "".
func hostExportsPrefix(script, prefix string) string {
	for _, k := range hostExportKeys(script) {
		if strings.HasPrefix(k, prefix) {
			return k
		}
	}
	return ""
}

// wireTable decodes one wire table the exec was handed, failing the test when it is absent or is
// not a JSON object: FT-D2 has every launch set all three.
func wireTable(t *testing.T, env map[string]string, name string) map[string]any {
	t.Helper()
	raw, ok := env[name]
	if !ok {
		t.Fatalf("the launch exported no %s (FT-D2: every `yolo host --` sets all three)", name)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("%s=%q is not a JSON object: %v", name, raw, err)
	}
	return m
}

// zaiOverBedrock is the measured case of OQ-FT15: the config selects bedrock for claude, and the
// launch types `-p zai`.
const zaiOverBedrock = `{"packs": ["claude", "zai"], "profile": {"claude": "bedrock"}, ` +
	`"env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`

// TestHostLaunchExportsTheWireTablesItComposed: `yolo host -p zai -- claude` hands claude the three
// tables, the selection holding claude's one-launch entry and nothing of the config's, the profile
// table resolving it, and the provider table carrying its provider.
func TestHostLaunchExportsTheWireTablesItComposed(t *testing.T) {
	env, errs := hostGateLaunchWith(t, zaiOverBedrock, nil, []string{"-p", "zai"}, "claude")
	use := wireTable(t, env, entrypoint.UseProfilesWireEnv)
	if len(use) != 1 || use["claude"] != "zai" {
		t.Errorf("YOLO_USE_PROFILES = %q, want claude's one-launch entry alone, {\"claude\": \"zai\"}\n%s",
			env[entrypoint.UseProfilesWireEnv], errs)
	}
	profiles := wireTable(t, env, entrypoint.ProfilesWireEnv)
	zai, _ := profiles["zai"].(map[string]any)
	if zai == nil || zai["provider"] != "zai" {
		t.Errorf("YOLO_PROFILES must resolve the selected profile to its provider: %q",
			env[entrypoint.ProfilesWireEnv])
	}
	if _, ok := wireTable(t, env, entrypoint.ProvidersWireEnv)["zai"]; !ok {
		t.Errorf("YOLO_PROVIDERS must carry the selected profile's provider: %q",
			env[entrypoint.ProvidersWireEnv])
	}
	// The tables carry names and addresses, never a credential's value.
	for _, k := range entrypoint.WireTables() {
		if strings.Contains(env[k], "tok-zai") {
			t.Errorf("%s carries the zai key's value: %q", k, env[k])
		}
	}
}

// TestHostLaunchReplacesInheritedWireTables: a launch started inside another launch (the tables
// already in its shell) sets all three to its own composition, empty selection included, rather
// than keeping what it inherited.
func TestHostLaunchReplacesInheritedWireTables(t *testing.T) {
	shell := map[string]string{
		entrypoint.UseProfilesWireEnv: `{"pi": "bedrock"}`,
		entrypoint.ProfilesWireEnv:    `{"inherited": {"provider": "nowhere"}}`,
		entrypoint.ProvidersWireEnv:   `{"nowhere": {}}`,
	}
	env, errs := hostGateLaunchWith(t, `{"packs": ["claude"]}`, shell, nil, "claude")
	if got := env[entrypoint.UseProfilesWireEnv]; got != "{}" {
		t.Errorf("YOLO_USE_PROFILES = %q, want {} for a launch that selects no profile\n%s", got, errs)
	}
	if _, kept := wireTable(t, env, entrypoint.ProfilesWireEnv)["inherited"]; kept {
		t.Errorf("YOLO_PROFILES kept the inherited table: %q", env[entrypoint.ProfilesWireEnv])
	}
	if _, kept := wireTable(t, env, entrypoint.ProvidersWireEnv)["nowhere"]; kept {
		t.Errorf("YOLO_PROVIDERS kept the inherited table: %q", env[entrypoint.ProvidersWireEnv])
	}
}

// TestHostEnvPrintsTheWireTables: `yolo host env` is the same composition, so its script carries
// the same three lines (FT-D2's build note), and an eval'd shell's footer names the same profile.
func TestHostEnvPrintsTheWireTables(t *testing.T) {
	hostGateHome(t, zaiOverBedrock, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "-p", "zai"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env -p zai: rc = %d\n%s", rc, errw.String())
	}
	for _, k := range entrypoint.WireTables() {
		if !hostExports(out.String(), k) {
			t.Errorf("yolo host env -p zai printed no %s:\n%s", k, out.String())
		}
	}
	if !strings.Contains(out.String(), `export YOLO_USE_PROFILES='{"claude": "zai"}'`) {
		t.Errorf("yolo host env -p zai must select zai for claude alone:\n%s", out.String())
	}
}

// TestHostLaunchFooterNamesAOneLaunchProfile is OQ-FT15's measured case, closed: with the config
// selecting bedrock for claude, `yolo host -p zai -- claude`'s footer names zai, because the
// renderer reads the tables the launch exported before it composes the config's. The footer
// command is the one `yolo host apply` filled into the scratch home's Claude settings, run
// through /bin/sh as Claude runs it, in the environment the exec was handed.
func TestHostLaunchFooterNamesAOneLaunchProfile(t *testing.T) {
	home, command := hostFooterHome(t, zaiOverBedrock)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("AWS_REGION", hostGateRegion)
	blankHostGateShell(t)
	for _, k := range entrypoint.WireTables() {
		t.Setenv(k, "")
	}
	t.Chdir(t.TempDir())
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	rc, _, env, errs := runRemedy(t, []string{"-p", "zai", "--", "claude"})
	if rc != 0 || env == nil {
		t.Fatalf("yolo host -p zai -- claude: rc = %d, reached exec = %v\n%s", rc, env != nil, errs)
	}
	line := hostFooterRunIn(t, home, command, env)
	if want := "Opus · yolo: zai · host"; line != want {
		t.Errorf("the one-launch footer = %q, want %q (the config selects bedrock; this launch runs zai)", line, want)
	}
}

// hostFooterRunIn is hostFooterRun in a given environment: command runs through /bin/sh with a
// `yolo` on PATH that records its argv, and that argv then reaches this package's front door with
// the launch's wire tables and provider switches set as the agent would inherit them.
func hostFooterRunIn(t *testing.T, home, command string, launchEnv map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "argv")
	writeFile(t, filepath.Join(bin, "yolo"), "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$YOLO_FOOTER_ARGV\"\n")
	if err := os.Chmod(filepath.Join(bin, "yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh := exec.Command("/bin/sh", "-c", command)
	sh.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "YOLO_FOOTER_ARGV=" + argsFile}
	if out, err := sh.CombinedOutput(); err != nil {
		t.Fatalf("status command failed under sh: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("the status command never ran yolo: %v", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	for _, k := range append(entrypoint.WireTables(), "YOLO_VERSION", "CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX") {
		t.Setenv(k, launchEnv[k])
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inW.WriteString(hostClaudeStatus); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	savedIn, savedOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	code := Main(append([]string{"yolo"}, argv...))
	os.Stdin, os.Stdout = savedIn, savedOut
	outW.Close()
	out, _ := io.ReadAll(outR)
	if code != 0 {
		t.Errorf("yolo %q exited %d, want 0", argv, code)
	}
	return strings.TrimRight(string(out), "\n")
}
