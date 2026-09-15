package entrypoint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// This follows the production hot path the live failure missed: the selected profile's
// env reaches Pi's npm launcher, the launcher materializes auth.json, and only then does
// Pi start and resolve its configured default. The fake Pi parses both files at exec time;
// no vendor agent or network request runs.
func TestPiCodexProfileSeedsFreshAuthBeforeNpmLauncherExec(t *testing.T) {
	pi := shippedPiPack(t)
	profiles := map[string]string{"pi": "codex"}
	env := packload.EnvVarsFor([]*packload.Pack{pi}, profiles)
	for key, want := range map[string]string{
		"YOLO_AUTH_PRELAUNCH_BIN":  "pi",
		"YOLO_AUTH_PRELAUNCH_FLAG": "--pi-auth",
		"YOLO_AUTH_PRELAUNCH_PATH": ".pi/agent/auth.json",
	} {
		if env[key] != want {
			t.Fatalf("profile env %s = %q, want %q", key, env[key], want)
		}
	}
	if got := packload.EnvVarsFor([]*packload.Pack{pi}, nil)["YOLO_AUTH_PRELAUNCH_BIN"]; got != "" {
		t.Fatalf("unprofiled Pi unexpectedly enables auth prelaunch: %q", got)
	}

	providers, err := packload.ComposeProviders(nil, []*packload.Pack{pi})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles([]*packload.Pack{pi}, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, `{}`)
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"codex"}`)
	home := r.e.Home
	authPath := filepath.Join(home, ".pi", "agent", "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"openai-codex":{"type":"oauth","access":"expired","refresh":"yolo-broker:1","expires":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(home, "fake-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(home, "calls")
	fakeYolo := `#!/bin/bash
printf '%s\n' "$*" >> "$CALLS"
auth_path="${4#--pi-auth=}"
cat > "$auth_path" <<'JSON'
{"openai-codex":{"type":"oauth","access":"fresh-access","refresh":"yolo-broker:2","expires":4102444800000,"accountId":"acct-1"}}
JSON
`
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fakeYolo), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(home, ".npm-global")
	realPi := filepath.Join(prefix, "bin", "pi")
	if err := os.MkdirAll(filepath.Dir(realPi), 0o755); err != nil {
		t.Fatal(err)
	}
	fakePi := `#!/bin/bash
node -e '
const fs = require("fs");
const auth = JSON.parse(fs.readFileSync(process.env.AUTH_PATH));
const settings = JSON.parse(fs.readFileSync(process.env.SETTINGS_PATH));
const c = auth["openai-codex"];
if (!c || c.type !== "oauth" || c.access !== "fresh-access" || c.refresh !== "yolo-broker:2" || c.expires <= Date.now()) process.exit(11);
if (settings.defaultProvider !== "openai-codex" || settings.defaultModel !== "gpt-5.4") process.exit(12);
'
echo PI_READY
`
	if err := os.WriteFile(realPi, []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	var install *packdecl.Install
	installs, _ := pi.HonoredInstalls()
	for i := range installs {
		if installs[i].Bin == "pi" {
			install = &installs[i]
		}
	}
	if install == nil {
		t.Fatal("shipped Pi pack has no pi program")
	}
	launcher := npmAgentLauncher(install, filepath.Join(home, "stamps"), filepath.Join(home, "receipts"), false, launcherServers{}, nil)
	launcherPath := filepath.Join(home, "launch-pi")
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcherPath)
	cmd.Env = append(os.Environ(),
		"HOME="+home, "NPM_CONFIG_PREFIX="+prefix,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLS="+calls, "AUTH_PATH="+authPath,
		"SETTINGS_PATH="+filepath.Join(home, ".pi", "agent", "settings.json"),
	)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "PI_READY") {
		t.Fatalf("Pi launcher did not seed auth before exec: %v\n%s", err, output)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	wantCall := "internal openai-auth-client token --pi-auth=" + authPath + "\n"
	if string(log) != wantCall {
		t.Fatalf("prelaunch calls = %q, want one refresh token call %q", log, wantCall)
	}
}
