package run

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestAssembleNoticesGoToStderr pins the STREAM, not the sentence: every notice
// assembleRunCmd prints while composing the argv must land on stderr, because
// stdout belongs to the jailed command. warnIfNoPacks states the rule
// (run.go, "Stderr, like every other launch notice"): a launch is usually
// `yolo -- cmd`, and the user redirects or pipes the COMMAND's stdout — a
// notice there is swallowed by the redirect or corrupts the piped payload.
//
// This is not hypothetical. The host-loopback note printed to stdout from
// 2026-09-04 to 2026-09-06, and on GitHub's ubuntu runners (podman 4.9.3:
// rootless, `podman info` predates rootlessNetworkCmd, slirp4netns fallback
// confirmed) it fired on EVERY bridge launch — so every integration test that
// asserts the jailed command's exact stdout grew a nine-line note prepended to
// it. TestHostComposedBriefingIsNotDeliveredTwice read the count "1" back as
// "Note: this host's podman (4.9.3)… 1"; TestProvidersRenderInTheAgentsOwn-
// Vocabulary saw a correct provider env declared wrong for the same reason
// (CI runs 34008441165/34009682846/34009751413, both architectures).
//
// A unit test is the only local proof of that stream. The in-jail suite cannot
// reproduce it: a nested launch forces --net=host, so the host-loopback
// decision never runs and the note never fires (the reachability carve-out,
// AGENTS.md "Nested-jail verification"). The integration tests that broke on
// CI pass in-jail for exactly that reason — green here meant nothing about the
// stream, which is how three days of red CI shipped from a green desk.
//
// Each row pins one print site: delete the call and its row fails on the
// stderr half; move it back to stdout and every row fails on the stdout half.
func TestAssembleNoticesGoToStderr(t *testing.T) {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	// netSec builds a network section the config readers will actually see —
	// cfgMap/mapGet type-switch on *jsonx.OrderedMap, so a plain Go map here
	// reads as no section at all and the row silently stops exercising its branch.
	netSec := func(pairs ...any) *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		for i := 0; i+1 < len(pairs); i += 2 {
			m.Set(pairs[i].(string), pairs[i+1])
		}
		return m
	}

	// The podman-info fixture of a GitHub ubuntu runner: rootless, old enough
	// that `podman info --format json` has NO rootlessNetworkCmd key, with
	// podman's own slirp4netns on tap — the exact facts whose note broke CI.
	ghPodmanInfo := `{"host":{"security":{"rootless":true},` +
		`"slirp4netns":{"executable":"/usr/libexec/podman/slirp4netns","version":"1.2.0"}},` +
		`"version":{"Version":"4.9.3"}}`

	cases := []struct {
		name       string
		rt         string
		cfg        *jsonx.OrderedMap
		lookPath   func(string) (string, bool)
		exec       func([]string, string, []string, time.Duration) ExecResult
		pathExists func(string) bool
		wantNotice string
	}{
		{
			// The CI breaker itself: the unnamed-backend note (hostloopback.go
			// unnamedBackendNotice) on a podman too old to name its rootless stack.
			name: "host-loopback note for an old podman",
			rt:   "podman",
			cfg: newConfig(
				"agents", []any{"claude"},
				"security", sec,
			),
			lookPath: func(b string) (string, bool) {
				if b == "podman" {
					return "/usr/bin/podman", true
				}
				return "", false
			},
			exec: func(args []string, _ string, _ []string, _ time.Duration) ExecResult {
				switch {
				case strings.Join(args, " ") == "/usr/bin/podman info --format json":
					return ExecResult{Ran: true, RC: 0, Stdout: ghPodmanInfo}
				case strings.Join(args, " ") == "/usr/libexec/podman/slirp4netns --help":
					// slirp4netnsHostLoopbackFlag is the literal probed for.
					return ExecResult{Ran: true, RC: 0, Stdout: "  --disable-host-loopback\n"}
				}
				return ExecResult{Ran: false}
			},
			wantNotice: "Note: this host's podman",
		},
		{
			name: "missing mount path",
			rt:   "podman",
			cfg: newConfig(
				"agents", []any{"claude"},
				"security", sec,
				"mounts", []any{"/no/such/host/path:/mnt/target"},
			),
			wantNotice: "Warning: mount path does not exist",
		},
		{
			// The nested row needs ports declared — the warning fires only when
			// nesting drops something the user asked for (assemble.go, OQ-BP-3).
			name: "nested launch drops declared ports",
			rt:   "podman",
			cfg: newConfig(
				"agents", []any{"claude"},
				"security", sec,
				"network", netSec("ports", []any{"8080:80"}),
			),
			pathExists: func(p string) bool { return p == "/run/.containerenv" },
			wantNotice: "Warning: nested launch",
		},
		{
			name: "explicit host mode on Apple Container",
			rt:   "container",
			cfg: newConfig(
				"agents", []any{"claude"},
				"security", sec,
				"network", netSec("mode", "host"),
			),
			wantNotice: "NOT honored on Apple Container",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			emptyLoopholeDirs(t)
			o := goldenOptions("/ws", home)

			// Row-specific seams override the golden's deterministic stubs.
			if tc.lookPath != nil {
				o.LookPath = tc.lookPath
			}
			if tc.exec != nil {
				o.Exec = tc.exec
			}
			if tc.pathExists != nil {
				o.PathExists = tc.pathExists
			}

			// goldenOptions → fillDefaults wired the real os.Stdout/os.Stderr;
			// the buffers must replace BOTH, or the assertion reads the process's
			// own streams instead of the launch's.
			var stdout, stderr bytes.Buffer
			o.Stdout = &stdout
			o.Stderr = &stderr

			in := &assembleInput{
				cfg:           tc.cfg,
				rt:            tc.rt,
				cname:         "yolo-ws-abcd1234",
				imageRef:      goldenImageRef,
				packs:         claudePackFixture(t),
				agentsPath:    "/agents/yolo-ws-abcd1234",
				wsState:       "/ws/.yolo/home",
				miseStore:     "/mise-store",
				yoloVersion:   "9.9.9-test",
				mountTargets:  map[string]struct{}{},
				lspNPMInstall: "",
				lspGoInstall:  "",
			}

			got := o.assembleRunCmd(in)
			if len(got) == 0 {
				t.Fatal("assembleRunCmd returned an empty argv — the row never reached a launch shape")
			}
			if !strings.Contains(stderr.String(), tc.wantNotice) {
				t.Errorf("notice %q missing from stderr — the launcher must say it there:\nstderr: %s",
					tc.wantNotice, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout is not empty — the jailed command owns that stream, and any "+
					"byte of launcher chatter corrupts what the user pipes or redirects:\n%s",
					stdout.String())
			}
		})
	}
}
