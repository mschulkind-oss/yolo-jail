package entrypoint

// agentenv_test.go pins the JAIL HALF of the per-agent env file
// (docs/design/provider-credential-scope.md, OQ-CN6): every carrier a bare agent name can
// resolve to in ~/.yolo/bin/launch — the npm launcher, the native launcher and the launch-flag
// wrapper — sources $HOME/.config/yolo-agent-env/<bin>.sh before it hands over to the
// program, and only its own. Each cell RUNS the generated script against a fake program that
// reports what it received, so deleting the splice from a template fails here rather than in a
// jail. No real agent runs.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// writeAgentEnvFile puts one agent's env file where the host launcher's bind lands it.
func writeAgentEnvFile(t *testing.T, home, agent, body string) {
	t.Helper()
	p := AgentEnvFile(home, agent)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// envReporter writes an executable at path that records $SCOPED and $OTHER into logPath.
func envReporter(t *testing.T, path, logPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf 'SCOPED=%s\\nOTHER=%s\\n' \"${SCOPED:-}\" \"${OTHER:-}\" > " +
		"'" + logPath + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Both installer templates carry the source: an agent's own file reaches its program, and
// another agent's file never does.
func TestTheInstallerLaunchersSourceTheAgentsOwnEnvFile(t *testing.T) {
	for _, kind := range []string{"npm", "native"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			logPath := filepath.Join(home, "seen.log")
			var body, realBin string
			inst := &packdecl.Install{Kind: kind, Bin: "probe"}
			switch kind {
			case "npm":
				inst.Package = "probe-pkg"
				realBin = filepath.Join(home, ".npm-global", "bin", "probe")
				body = npmAgentLauncher(inst, filepath.Join(home, "stamps"),
					filepath.Join(home, "receipts"), false, launcherServers{}, nil)
			case "native":
				inst.InstallerURL = "http://127.0.0.1:9/install.sh" // never fetched: the bin exists
				realBin = filepath.Join(home, ".local", "bin", "probe")
				body = nativeAgentLauncher(inst, filepath.Join(home, "stamps"),
					filepath.Join(home, "receipts"), "", false, launcherServers{}, nil)
			}
			envReporter(t, realBin, logPath)
			launcher := filepath.Join(home, "launch-probe")
			if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			writeAgentEnvFile(t, home, "probe", "export SCOPED='mine'\n")
			writeAgentEnvFile(t, home, "someone-else", "export OTHER='theirs'\n")

			out, rc := runScript(t, launcher, nil,
				[]string{"HOME=" + home, "PATH=/usr/bin:/bin"})
			if rc != 0 {
				t.Fatalf("the %s launcher exited %d:\n%s", kind, rc, out)
			}
			got := strings.Join(logLines(t, logPath), " ")
			if got != "SCOPED=mine OTHER=" {
				t.Errorf("the %s launcher handed its program %q, want its own file's value and "+
					"nothing from another agent's\n%s", kind, got, out)
			}
		})
	}
}

// The launch-flag wrapper — the carrier a bare name resolves to when the image or a mise
// tool provides the program — sources the file too, so an agent whose launcher collision
// check wrote no installer still receives what the gate scoped to it.
func TestTheLaunchWrapperSourcesTheAgentsOwnEnvFile(t *testing.T) {
	home := t.TempDir()
	e := NewEnv(map[string]string{
		"JAIL_HOME":      home,
		"YOLO_PACK_ROOT": flaggedPackRoot(t, nil, map[string][]string{"wrapped": {"--flag"}}),
	})
	runLaunchDirPasses(t, e)
	target := filepath.Join(home, "target")
	logPath := filepath.Join(home, "seen.log")
	envReporter(t, filepath.Join(target, "wrapped"), logPath)
	writeAgentEnvFile(t, home, "wrapped", "export SCOPED='wrapped-own'\n")

	path := strings.Join([]string{e.LaunchDir(), target, "/usr/bin", "/bin"}, ":")
	out, rc := runScript(t, filepath.Join(e.LaunchDir(), "wrapped"), nil,
		[]string{"HOME=" + home, "PATH=" + path})
	if rc != 0 {
		t.Fatalf("the wrapper exited %d:\n%s", rc, out)
	}
	if got := strings.Join(logLines(t, logPath), " "); got != "SCOPED=wrapped-own OTHER=" {
		t.Errorf("the wrapper handed its program %q, want its own env file's value\n%s", got, out)
	}
}

// The file is sourced AHEAD OF the pre-launch authentication step, because that step reads
// its own switches from the environment and a profile-gated switch is exactly what the
// gate now puts in the agent's file: pi's YOLO_AUTH_PRELAUNCH_PI_FLAG is gated on the
// `codex` profile, so it no longer rides the shared file. With the switch ONLY in pi's own
// file the launcher must still ask the broker for a token before pi starts.
func TestTheAuthPrelaunchReadsItsSwitchFromTheAgentsOwnFile(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "fake-bin")
	calls := filepath.Join(home, "calls")
	fakeYolo := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\n"
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(fakeYolo), 0o755); err != nil {
		t.Fatal(err)
	}
	inst := &packdecl.Install{Kind: "native", Bin: "probe", InstallerURL: "http://127.0.0.1:9/i.sh"}
	logPath := filepath.Join(home, "seen.log")
	envReporter(t, filepath.Join(home, ".local", "bin", "probe"), logPath)
	body := nativeAgentLauncher(inst, filepath.Join(home, "stamps"), filepath.Join(home, "receipts"),
		"", false, launcherServers{}, nil)
	launcher := filepath.Join(home, "launch-probe")
	if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAgentEnvFile(t, home, "probe", "export YOLO_AUTH_PRELAUNCH_PROBE_FLAG='--probe-auth'\n"+
		"export YOLO_AUTH_PRELAUNCH_PROBE_PATH='.probe/auth.json'\n")

	out, rc := runScript(t, launcher, nil,
		[]string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin"})
	if rc != 0 {
		t.Fatalf("the launcher exited %d:\n%s", rc, out)
	}
	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the pre-launch authentication step never ran — its switch, in the agent's "+
			"own file, was not sourced before it: %v\n%s", err, out)
	}
	if want := "internal openai-auth-client token --probe-auth=" + filepath.Join(home, ".probe/auth.json"); !strings.Contains(string(log), want) {
		t.Errorf("auth prelaunch calls = %q, want %q", log, want)
	}
}
