package cli

// hostprofilepair_test.go pins the host -p value read in the run path's grammar
// (docs/design/credential-sources-separation.md ES-D27). `yolo host -p claude=codex -- claude`
// used to refuse as an undeclared profile named "claude=codex", so the jail's spelling with
// `host` added looked like a typo rather than meeting the host's own answer for that profile.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The grammar's arms: a bare name, a pair naming the agent, a pair naming another CLI, a pair
// with no name.
func TestHostProfileForReadsTheRunGrammar(t *testing.T) {
	for _, tc := range []struct {
		v, want, refusal string
	}{
		{"bedrock", "bedrock", ""},
		{"claude=bedrock", "bedrock", ""},
		{"claude=zai,claude=bedrock", "bedrock", ""},
		{"", "", ""},
		{"pi=codex", "", `selects a profile for "pi", but this composes the environment of "claude" alone`},
		{"claude=bedrock,pi=codex", "", `selects a profile for "pi"`},
		{"claude=", "", `names no profile for "claude"`},
	} {
		got, err := hostProfileFor(tc.v, "claude", false)
		if tc.refusal == "" {
			if err != nil || got != tc.want {
				t.Errorf("hostProfileFor(%q) = %q, %v; want %q", tc.v, got, err, tc.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.refusal) {
			t.Errorf("hostProfileFor(%q) = %q, %v; want a refusal saying %q", tc.v, got, err, tc.refusal)
		}
	}
}

// Through hostMain: a pair naming the launched command composes exactly what the bare name does.
func TestHostPairNamingTheCommandIsTheBareName(t *testing.T) {
	bare, _ := hostGateLaunchWith(t, claudeAlone, nil, []string{"-p", "bedrock"}, "claude")
	pair, _ := hostGateLaunchWith(t, claudeAlone, nil, []string{"-p", "claude=bedrock"}, "claude")
	for k := range pair {
		if _, ok := bare[k]; !ok {
			t.Errorf("the pair composed %s, which the bare name did not", k)
		}
	}
	if pair["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Fatalf("-p claude=bedrock -- claude did not compose bedrock: %v", pair)
	}
	composed := func(k string) bool {
		for _, p := range []string{"CLAUDE_", "ANTHROPIC_", "AWS_", "YOLO_"} {
			if strings.HasPrefix(k, p) {
				return true
			}
		}
		return false
	}
	for k, v := range bare {
		if composed(k) && pair[k] != v {
			t.Errorf("%s: bare -p composed %q, the pair %q", k, v, pair[k])
		}
	}
}

// A pair naming another CLI refuses by name, before any exec, and grants nothing.
func TestHostPairNamingAnotherCLIRefuses(t *testing.T) {
	rc, env, errs := hostGateRun(t, claudeAlone, nil, []string{"-p", "pi=codex"}, "claude")
	if rc != 2 || env != nil {
		t.Fatalf("yolo host -p pi=codex -- claude: rc = %d, env = %v, want a refusal (2) before the exec\n%s",
			rc, env, errs)
	}
	for _, want := range []string{`"pi"`, `"claude" alone`, "`yolo host -p codex -- pi`", "--with-credentials <provider>"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}

	hostGateHome(t, claudeAlone, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "-p", "pi=codex"}, &out, &errw, false, nil); rc != 2 || out.Len() != 0 {
		t.Fatalf("yolo host env -p pi=codex: rc = %d, stdout = %q, want a refusal with nothing to eval", rc, out.String())
	}
	if !strings.Contains(errw.String(), "`yolo host env --agent pi -p codex`") {
		t.Errorf("host env's refusal must spell its own verb:\n%s", errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "claude=bedrock", "--format", "json"},
		&out, &errw, false, nil); rc != 0 || !strings.Contains(out.String(), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("yolo host env -p claude=bedrock: rc = %d\n%s\n%s", rc, out.String(), errw.String())
	}
}

// BOTH ORDERS, through cli.Main in a child with a fake claude on PATH: `yolo host -p
// claude=codex -- claude` and `yolo -p claude=codex host -- claude` reach the host's answer for
// claude's codex profile (ES-D18, and ES-D25 when openai-auth is not listed), never the
// undeclared-profile refusal, and never run claude.
func TestHostPairReachesTheCodexRefusalInBothOrders(t *testing.T) {
	const helper = "YOLO_TEST_HOST_PAIR_SPELLING"
	if spelling := os.Getenv(helper); spelling != "" {
		os.Exit(Main(append([]string{"yolo"}, strings.Fields(spelling)...)))
	}
	for _, cfg := range []string{claudeAlone, `{"packs": ["claude", "openai-auth"]}`} {
		for _, spelling := range []string{"host -p claude=codex -- claude", "-p claude=codex host -- claude",
			"host --profile=claude=codex -- claude"} {
			t.Run(cfg+" "+spelling, func(t *testing.T) {
				home := t.TempDir()
				userCfg(t, home, cfg)
				bin := t.TempDir()
				fake := "#!/bin/sh\necho FAKE-CLAUDE-RAN\n"
				if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestHostPairReachesTheCodexRefusalInBothOrders$")
				cmd.Dir = t.TempDir()
				cmd.Env = append(envWithoutYolo(), helper+"="+spelling, "HOME="+home,
					"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "NO_COLOR=1")
				out, err := cmd.CombinedOutput()
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
					t.Fatalf("yolo %s: exit = %v, want 1 (a refusal)\n%s", spelling, err, out)
				}
				s := string(out)
				if strings.Contains(s, "FAKE-CLAUDE-RAN") || strings.Contains(s, `"claude=codex"`) {
					t.Fatalf("yolo %s ran claude or read the pair as a profile name:\n%s", spelling, s)
				}
				for _, want := range []string{"http://127.0.0.1:8215", "No host process serves it",
					"which is a jail launch, not a `yolo host` one"} {
					if !strings.Contains(s, want) {
						t.Errorf("yolo %s: the refusal must say %q:\n%s", spelling, want, s)
					}
				}
			})
		}
	}
}
