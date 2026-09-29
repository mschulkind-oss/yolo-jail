package cli

// hostprofilepair_test.go pins the host -p value read in the run path's grammar
// (docs/design/credential-sources-separation.md ES-D27). `yolo host -p claude=codex -- claude`
// used to refuse as an undeclared profile named "claude=codex", so the jail's spelling with
// `host` added looked like a typo rather than meeting the host's own answer for that profile.

import (
	"bytes"
	"io"
	"os"
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

// A `-p claude=codex` pair reaches the bridge in both orders, and through `--profile=`: the pair is
// read in the run path's grammar (ES-D27), so the host starts the wire bridge for claude rather than
// reading "claude=codex" as a profile name, whether openai-auth is listed or joins through claude's
// `needs`.
func TestHostPairStartsTheBridgeInBothOrders(t *testing.T) {
	const helper = "YOLO_TEST_HOST_PAIR_SPELLING"
	if spelling := os.Getenv(helper); spelling != "" {
		prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
		os.Exit(Main(append([]string{"yolo"}, strings.Fields(spelling)...)))
	}
	for _, cfg := range []string{claudeAlone, `{"packs": ["claude", "openai-auth"]}`} {
		for _, spelling := range []string{"host -p claude=codex -- claude", "-p claude=codex host -- claude",
			"host --profile=claude=codex -- claude"} {
			t.Run(cfg+" "+spelling, func(t *testing.T) {
				out := runHostSpelling(t, "^TestHostPairStartsTheBridgeInBothOrders$", helper, cfg, spelling)
				if strings.Contains(out, `"claude=codex"`) {
					t.Fatalf("yolo %s read the pair as a profile name:\n%s", spelling, out)
				}
				assertBridgedClaude(t, spelling, out)
			})
		}
	}
}
