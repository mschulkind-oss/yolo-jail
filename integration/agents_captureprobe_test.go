package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestPiPackConfigCheckReadsPublishedSessionHome(t *testing.T) {
	var tc packCase
	for _, candidate := range packMatrix {
		if candidate.pack == "pi" {
			tc = candidate
			break
		}
	}
	if tc.pack == "" {
		t.Fatal("pi is missing from packMatrix")
	}

	home := t.TempDir()
	agentDir := filepath.Join(t.TempDir(), "pi session home")
	configPath := filepath.Join(agentDir, tc.configRel)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(tc.marker), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(home, ".pi", "agent", tc.configRel)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy home must not satisfy this probe"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(agent string) ([]byte, error) {
		cmd := exec.Command("/bin/bash", "-c", packConfigCheck(tc))
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
		if agent != "" {
			cmd.Env = append(cmd.Env, tc.configDirEnv+"="+agent)
		}
		return cmd.CombinedOutput()
	}
	out, err := run(agentDir)
	if err != nil {
		t.Fatalf("Pi config probe did not read the published session home's settings.json: %v\n%s", err, out)
	}
	if out, err := run(""); err == nil {
		t.Errorf("Pi config probe silently accepted the legacy home without PI_CODING_AGENT_DIR: %s", out)
	}
}

func TestPackInstallProbePreparedCaptureEvidence(t *testing.T) {
	const bin = "chrome-devtools-mcp"
	tc := packCase{pack: "chrome-devtools", binary: bin, versionArg: "--version",
		configRel: ".yolo-test/config", marker: "prepared-config"}
	program := shippedProgram(t, tc.pack, bin)
	name, version := packdecl.SplitNpmSpec(program.Package)
	wantSpec := packdecl.NpmInstallSpec(name, version)
	identity := strings.Repeat("a", 64)
	vector, err := json.Marshal(map[string]entrypoint.ProgramCaptureDelivery{bin: {
		Declared: program.Package, Compatibility: identity,
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name           string
		vector         string
		marker         string
		spec           string
		payload        bool
		wantDiagnostic string
	}{
		{name: "prepared exact identity without a legacy stamp", vector: string(vector), marker: identity,
			spec: wantSpec, payload: true},
		{name: "prepared marker mismatch", vector: string(vector), marker: strings.Repeat("b", 64),
			spec: wantSpec, payload: true, wantDiagnostic: "prepared capture marker differs from launch identity"},
		{name: "prepared payload missing", vector: string(vector), marker: identity,
			spec: wantSpec, wantDiagnostic: "version command failed"},
		{name: "legacy installer leaves the program on PATH"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			binDir := t.TempDir()
			stampDir := filepath.Join(home, ".cache", "yolo-agent-stamps")
			if err := os.MkdirAll(stampDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.vector != "" {
				markerPath := filepath.Join(home, ".cache", "yolo-program-captures", bin)
				if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(markerPath, []byte(tt.marker), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stampDir, bin+".spec"), []byte(tt.spec), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.payload {
				if err := os.WriteFile(filepath.Join(home, "prepared-payload"), []byte("payload"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fakeProgram := "#!/bin/sh\nif [ -n \"${YOLO_PROGRAM_CAPTURES:-}\" ] && [ ! -f \"$HOME/prepared-payload\" ]; then echo 'capture payload missing' >&2; exit 42; fi\necho 1.0.0\n"
			if err := os.WriteFile(filepath.Join(binDir, bin), []byte(fakeProgram), 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, tc.configRel)
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(tc.marker), 0o600); err != nil {
				t.Fatal(err)
			}

			probe := packInstallProbe(t, tc)
			cmd := exec.Command("/bin/bash", "-c", probe)
			cmd.Env = append(envWithout(entrypoint.ProgramCapturesEnv), "HOME="+home, "PATH="+binDir+":"+os.Getenv("PATH"))
			if tt.vector != "" {
				cmd.Env = append(cmd.Env, entrypoint.ProgramCapturesEnv+"="+tt.vector)
			}
			out, err := cmd.CombinedOutput()
			if tt.wantDiagnostic != "" {
				if err == nil || !strings.Contains(string(out), tt.wantDiagnostic) {
					t.Fatalf("probe result err=%v output=%s, want diagnostic %q", err, out, tt.wantDiagnostic)
				}
				return
			}
			if err != nil {
				t.Fatalf("probe: %v\n%s", err, out)
			}
			if tt.vector != "" {
				if _, err := os.Stat(filepath.Join(stampDir, bin+".stamp")); !os.IsNotExist(err) {
					t.Fatalf("prepared route unexpectedly requires or creates legacy stamp: stat err=%v", err)
				}
			}
		})
	}
}
