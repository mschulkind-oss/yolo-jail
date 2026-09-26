package entrypoint

// agentenv_test.go pins the JAIL HALF of the per-agent env file
// (docs/design/provider-credential-scope.md, OQ-CN6): every carrier a bare agent name can
// resolve to in ~/.yolo/bin/launch — the npm launcher, the native launcher and the wrapper,
// with launch flags or, for an agent the image provides, with none — sources
// $HOME/.config/yolo-agent-env/<bin>.sh before it hands over to the
// program, and only its own. Each cell RUNS the generated script against a fake program that
// reports what it received, so deleting the splice from a template fails here rather than in a
// jail. No real agent runs.

import (
	"os"
	"os/exec"
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

// The launch-flag wrapper — one carrier a bare name resolves to when the image or a mise
// tool provides the program — sources the file too, so an agent whose launcher collision
// check wrote no installer still receives what the gate scoped to it. The flagless case is
// TestAShadowedAgentWithoutLaunchFlagsStillSourcesItsOwnFile.
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

// AN AGENT THE IMAGE PROVIDES STILL RECEIVES ITS FILE. pi and opencode declare no launch
// flags, so when the image (or the store farm, or a declared mise tool) provides them the
// installer pass writes no launcher and the flag pass no wrapper — and nothing sourced
// ~/.config/yolo-agent-env/<bin>.sh, so the agent that selected a profile started without the
// credentials the gate scoped to it, which the shared file used to hand it. Over the SHIPPED
// packs, through all three launch-dir generators, the bare name must resolve to a carrier
// that sources the agent's own file and runs the image's program.
func TestAShadowedAgentWithoutLaunchFlagsStillSourcesItsOwnFile(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	image := t.TempDir()
	orig := imageProbeBase
	imageProbeBase = image
	t.Cleanup(func() { imageProbeBase = orig })
	home := t.TempDir()
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)})
	for _, agent := range []string{"pi", "opencode"} {
		envReporter(t, filepath.Join(image, agent), filepath.Join(home, agent+".log"))
	}

	// An entry that scoped something to pi alone: pi gets a carrier, and opencode — provided
	// by the image too, with nothing of its own — gets nothing standing in front of it.
	writeAgentEnvFile(t, home, "pi", "export SCOPED='pi-own'\n")
	runLaunchDirPasses(t, e)
	if _, err := os.Stat(filepath.Join(e.LaunchDir(), "opencode")); !os.IsNotExist(err) {
		t.Errorf("opencode has no env file this entry, so no wrapper may stand in front of the "+
			"image's copy (err=%v)", err)
	}

	// The next entry scopes something to opencode too; the boot re-runs the pass.
	writeAgentEnvFile(t, home, "opencode", "export SCOPED='opencode-own'\n")
	runLaunchDirPasses(t, e)
	path := strings.Join([]string{e.LaunchDir(), image, "/usr/bin", "/bin"}, ":")
	for _, agent := range []string{"pi", "opencode"} {
		carrier := filepath.Join(e.LaunchDir(), agent)
		body, err := os.ReadFile(carrier)
		if err != nil {
			t.Errorf("%s: the image provides it and it has an env file, but the launch dir has "+
				"no carrier, so nothing sources its file: %v", agent, err)
			continue
		}
		for _, forbidden := range []string{"npm install", "_do_install"} {
			if strings.Contains(string(body), forbidden) {
				t.Errorf("%s: the carrier for an image-provided agent must install nothing (%q)", agent, forbidden)
			}
		}
		out, rc := runScript(t, carrier, nil, []string{"HOME=" + home, "PATH=" + path})
		if rc != 0 {
			t.Fatalf("%s's carrier exited %d:\n%s", agent, rc, out)
		}
		if got := strings.Join(logLines(t, filepath.Join(home, agent+".log")), " "); got != "SCOPED="+agent+"-own OTHER=" {
			t.Errorf("%s ran with %q, want its own file's value\n%s", agent, got, out)
		}
	}
}

// probeLauncher writes the npm or native launcher for a probe program whose real binary
// reports $SCOPED and $OTHER into logPath, and returns the launcher's path.
func probeLauncher(t *testing.T, home, kind, logPath string, servers launcherServers) string {
	t.Helper()
	inst := &packdecl.Install{Kind: kind, Bin: "probe"}
	var body, realBin string
	switch kind {
	case "npm":
		inst.Package = "probe-pkg"
		realBin = filepath.Join(home, ".npm-global", "bin", "probe")
		body = npmAgentLauncher(inst, filepath.Join(home, "stamps"), filepath.Join(home, "receipts"),
			false, servers, nil)
	case "native":
		inst.InstallerURL = "http://127.0.0.1:9/i.sh" // never fetched: the bin exists
		realBin = filepath.Join(home, ".local", "bin", "probe")
		body = nativeAgentLauncher(inst, filepath.Join(home, "stamps"), filepath.Join(home, "receipts"),
			"", false, servers, nil)
	}
	envReporter(t, realBin, logPath)
	launcher := filepath.Join(home, "launch-probe")
	if err := os.WriteFile(launcher, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return launcher
}

// fakeYoloRecording puts a `yolo` on binDir that appends its argv and the $SCOPED it was
// handed to callsPath, one line per call.
func fakeYoloRecording(t *testing.T, binDir, callsPath string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s SCOPED=%s\\n' \"$*\" \"${SCOPED:-}\" >> '" + callsPath + "'\n"
	if err := os.WriteFile(filepath.Join(binDir, "yolo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The file is sourced AHEAD OF the pre-launch authentication step, because that step reads
// its own switches from the environment and a profile-gated switch is exactly what the
// gate now puts in the agent's file: pi's YOLO_AUTH_PRELAUNCH_PI_FLAG is gated on the
// `codex` profile, so it no longer rides the shared file. With the switch ONLY in the
// agent's own file the launcher must still ask the broker for a token before the program
// starts — on BOTH templates, since pi's launcher is the npm one and it is the case CN-D10
// names (swapping the two fragments in that template alone used to leave the suite green).
func TestTheAuthPrelaunchReadsItsSwitchFromTheAgentsOwnFile(t *testing.T) {
	for _, kind := range []string{"npm", "native"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, "fake-bin")
			calls := filepath.Join(home, "calls")
			fakeYoloRecording(t, binDir, calls)
			launcher := probeLauncher(t, home, kind, filepath.Join(home, "seen.log"), launcherServers{})
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
		})
	}
}

// THE REFRESH STEPS RUN WITHOUT THE AGENT'S CREDENTIALS. The MCP server refresh installs npm
// packages, whose lifecycle scripts run, and the pre-launch refresh updates pi's extensions;
// neither needs a provider credential, so neither may run holding one. The file is sourced
// after both, immediately before the authentication step and the exec — and the program
// still receives it.
func TestTheRefreshStepsRunWithoutTheAgentsCredentials(t *testing.T) {
	for _, kind := range []string{"npm", "native"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, "fake-bin")
			calls := filepath.Join(home, "calls")
			fakeYoloRecording(t, binDir, calls)
			seen := filepath.Join(home, "seen.log")
			launcher := probeLauncher(t, home, kind, seen, launcherServers{npm: "some-mcp-server"})
			writeAgentEnvFile(t, home, "probe", "export SCOPED='the-agents-key'\n")

			out, rc := runScript(t, launcher, nil,
				[]string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin"})
			if rc != 0 {
				t.Fatalf("the launcher exited %d:\n%s", rc, out)
			}
			log, err := os.ReadFile(calls)
			if err != nil || !strings.Contains(string(log), "internal refresh-servers") {
				t.Fatalf("the MCP server refresh never ran, so this proves nothing: %v %q\n%s", err, log, out)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
				if strings.Contains(line, "refresh-servers") && !strings.HasSuffix(line, "SCOPED=") {
					t.Errorf("the MCP server refresh ran holding the agent's credential: %q", line)
				}
			}
			if got := strings.Join(logLines(t, seen), " "); got != "SCOPED=the-agents-key OTHER=" {
				t.Errorf("the program must still receive its own file: got %q\n%s", got, out)
			}
		})
	}
}
