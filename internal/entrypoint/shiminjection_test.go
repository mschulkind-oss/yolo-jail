package entrypoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE SHIM BODY IS A /bin/sh SCRIPT BUILT FROM AGENT-EDITABLE TEXT. Every value
// GenerateShims splices into it — `message`, `suggestion`, `block_flags`,
// `allow_flags` — arrives from YOLO_BLOCK_CONFIG, whose workspace half is editable by
// the agent running in the jail (the same reason `name` already has a writer-side
// guard, TestGenerateShimsSkipsPathyToolNames).
//
// Until 2026-09-05 all four were spliced RAW, and the docstring on ShimContent
// declared the raw splice deliberate. Demonstrated the same day, at the call site:
// a message of `oops"; touch <path>; echo "done` emitted
//
//	echo "oops"; touch <path>; echo "done" >&2
//
// and the `touch` RAN. The flag lists were the same defect one line down — a
// block_flags entry of `-q) touch <path>;; -z` closed the case arm and opened its own.
//
// WHY IT MATTERS DESPITE THE JAIL. In-jail (boot.go) the privilege gain is nil: an
// agent that can edit the workspace config already has a shell in that jail, and
// `yolo check`'s probe generates into a temp dir it never executes. The context that
// LEAVES a container is the macos-user backend (darwin.go / RunDarwinBootstrap),
// which generates these same shims into the `_yolojail` HOST account's home to be run
// as that account. So this is a host-side hole wearing in-jail clothing, and every
// test below therefore EXECUTES the shim rather than inspecting its bytes.
//
// THESE TESTS PIN THE CALL SITE, NOT THE CALLEE. They drive GenerateShims from a
// YOLO_BLOCK_CONFIG string and run the file it wrote, so deleting the escaping from
// ShimContent, or deleting GenerateShims' call to it, fails them both.

// TestShimRefusesInjectionThroughMessageAndSuggestion is the regression test for the
// demonstrated defect: the payload must reach stderr as TEXT and never as script.
//
// The second assertion is the load-bearing one. Dropping the metacharacters would
// also stop the `touch`, and would be wrong: the message is the whole point of a
// blocker, so the fix has to be quoting rather than filtering. Asserting the payload
// arrives on stderr byte-for-byte is what tells those two apart.
func TestShimRefusesInjectionThroughMessageAndSuggestion(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	for _, field := range []string{"message", "suggestion"} {
		for _, arm := range []struct {
			name  string
			extra map[string]any
			argv  []string
		}{
			// The unconditional-block arm (shims.go's else branch).
			{"unconditional", nil, []string{"http://x"}},
			// Both echo pairs of the argv-filter arm: a long-option exact match and a
			// short pattern. Each splices msg/sug again, and a fix applied to one echo
			// and not the others would pass a test that only exercised one.
			{"filter_long", map[string]any{"block_flags": []string{"--recursive", "-r"}}, []string{"--recursive", "x"}},
			{"filter_short", map[string]any{"block_flags": []string{"--recursive", "-r"}}, []string{"-r", "x"}},
		} {
			t.Run(field+"_"+arm.name, func(t *testing.T) {
				mark := filepath.Join(t.TempDir(), "pwned")
				payload := `oops"; touch ` + mark + `; echo "done`

				// grep, not curl: only grep and find get a realBin, and without one the
				// argv-filter arm is never generated at all.
				entry := map[string]any{"name": "grep", "message": "benign", "suggestion": "benign"}
				for k, v := range arm.extra {
					entry[k] = v
				}
				entry[field] = payload
				raw, err := json.Marshal([]any{entry})
				if err != nil {
					t.Fatal(err)
				}
				e := NewEnv(map[string]string{
					"JAIL_HOME":         t.TempDir(),
					"YOLO_BLOCK_CONFIG": string(raw),
				})
				if err := GenerateShims(e); err != nil {
					t.Fatal(err)
				}
				shim := filepath.Join(e.BlockDir(), "grep")

				rc, _, stderr := runShim(t, shim, arm.argv, "")

				if _, err := os.Stat(mark); err == nil {
					body, _ := os.ReadFile(shim)
					t.Fatalf("SHELL INJECTION through %q: the payload ran and created %s\n"+
						"the generated shim was:\n%s", field, mark, body)
				}
				if rc != 127 {
					t.Errorf("rc=%d, want 127 — the blocker must still refuse", rc)
				}
				want := payload
				if field == "suggestion" {
					want = "Suggestion: " + payload
				}
				if !strings.Contains(stderr, want) {
					t.Errorf("the %s text did not reach stderr verbatim — quoting it must not\n"+
						"mangle or strip it, or the blocker stops explaining itself.\n got: %q\nwant substring: %q",
						field, stderr, want)
				}
			})
		}
	}
}

// TestShimRefusesInjectionThroughFlagPatterns is the sibling vector found while
// fixing the one above: block_flags and allow_flags are spliced as `case` PATTERNS,
// so a pattern carrying `)` and `;;` closes the arm the generator opened and the rest
// of it is script.
//
// These cannot be shquote'd — a case pattern is a glob, and quoting `-*[rR]*` would
// make it literal and break the shipped grep rule (TestShimKeepsShippedGlobPatterns
// below is the guard for exactly that). They are validated instead: a pattern outside
// the glob vocabulary is dropped, which is the same writer-side shape the `name`
// field already has.
func TestShimRefusesInjectionThroughFlagPatterns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	for _, key := range []string{"block_flags", "allow_flags"} {
		t.Run(key, func(t *testing.T) {
			mark := filepath.Join(t.TempDir(), "pwned")
			entry := map[string]any{
				"name":        "grep",
				"message":     "m",
				"block_flags": []string{"-r"},
			}
			entry[key] = []string{"-q) touch " + mark + ";; -z"}
			raw, err := json.Marshal([]any{entry})
			if err != nil {
				t.Fatal(err)
			}
			var warnings strings.Builder
			e := NewEnv(map[string]string{
				"JAIL_HOME":         t.TempDir(),
				"YOLO_BLOCK_CONFIG": string(raw),
			})
			e.Stderr = &warnings
			if err := GenerateShims(e); err != nil {
				t.Fatal(err)
			}
			shim := filepath.Join(e.BlockDir(), "grep")

			runShim(t, shim, []string{"-q"}, "")
			if _, err := os.Stat(mark); err == nil {
				body, _ := os.ReadFile(shim)
				t.Fatalf("SHELL INJECTION through %s: the payload ran and created %s\n"+
					"the generated shim was:\n%s", key, mark, body)
			}

			// Dropping a pattern silently would be its own defect: the user asked for a
			// rule and got none.
			if !strings.Contains(warnings.String(), key) {
				t.Errorf("dropped a malformed %s pattern without saying so:\n%s", key, warnings.String())
			}
			// And the well-formed rule in the same entry still works — the drop is
			// per-pattern, not per-entry.
			if rc, _, _ := runShim(t, shim, []string{"-r", "x"}, ""); rc != 127 {
				t.Errorf("-r rc=%d, want 127 — a bad sibling pattern must not disarm the good one", rc)
			}
		})
	}
}

// TestDarwinBootstrapWritesNonInjectableShims is the test for the call site that
// actually decides this defect's severity, and it is deliberately a SECOND test of
// the same payload rather than a case in the one above.
//
// Every other generator of these shims runs inside a container, where an agent that
// can write the payload already has a shell. RunDarwinBootstrap does not: the
// macos-user backend runs it against the `_yolojail` HOST account's home, and the
// shim it leaves there is later executed as that account. If someone deletes
// GenerateShims from RunDarwinBootstrap's genStep list, or gives this backend a
// generator of its own, the tests above stay green while the only host-side path
// stops being covered. This one goes red.
func TestDarwinBootstrapWritesNonInjectableShims(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	home := t.TempDir()
	mark := filepath.Join(t.TempDir(), "pwned")
	payload := `oops"; touch ` + mark + `; echo "done`

	raw, err := json.Marshal([]any{map[string]any{
		"name": "curl", "message": payload, "suggestion": payload,
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"HOME": home, "YOLO_BLOCK_CONFIG": string(raw)})
	e.Workspace = filepath.Join(t.TempDir(), "proj")
	e.ShimBinDir = "/usr/bin"

	RunDarwinBootstrap(e, DarwinBootstrapOptions{
		LoginPath:     filepath.Join(home, ".yolo/bin/block") + ":/usr/bin",
		YoloLogScript: "#!/bin/sh\nexec /usr/bin/log \"$@\"\n",
	})

	shim := filepath.Join(home, ".yolo/bin/block", "curl")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("the darwin bootstrap generated no shim at all (%v) — this backend's "+
			"blockers come from RunDarwinBootstrap's generate_shims step", err)
	}
	rc, _, stderr := runShim(t, shim, []string{"http://x"}, "")
	if _, err := os.Stat(mark); err == nil {
		body, _ := os.ReadFile(shim)
		t.Fatalf("SHELL INJECTION in the macos-user sandbox home: the payload ran as the "+
			"host account and created %s\nthe generated shim was:\n%s", mark, body)
	}
	if rc != 127 {
		t.Errorf("rc=%d, want 127", rc)
	}
	if !strings.Contains(stderr, payload) {
		t.Errorf("the message did not reach stderr verbatim: %q", stderr)
	}
}

// TestShimStderrIsExactlyTheConfiguredText pins the half of the frozen contract the
// escaping had to leave alone. Quoting changed the LITERAL's delimiter in the emitted
// script (`echo "msg"` became `echo 'msg'`); it must not have changed one byte of what
// the shim prints, which is what a user of the contract actually observes.
//
// The shipped guardrails text is used deliberately: it carries an apostrophe
// ("grep's", which shquote has to break out of its own single quotes), parentheses,
// and the angle/square brackets of "Try: rg <pattern> [path]" — the characters most
// likely to be mangled by a quoting change or eaten by a filtering one.
func TestShimStderrIsExactlyTheConfiguredText(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found")
	}
	const msg = "grep's recursive mode is blocked. Use ripgrep (rg) for recursive " +
		"searches; pipe filters and single-file greps pass through."
	const sug = "Try: rg <pattern> [path]"

	raw, err := json.Marshal([]any{map[string]any{
		"name": "grep", "message": msg, "suggestion": sug,
		"block_flags": []string{"--recursive", "-r", "-R", "-*[rR]*"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_BLOCK_CONFIG": string(raw)})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	rc, _, stderr := runShim(t, filepath.Join(e.BlockDir(), "grep"), []string{"-rn", "x"}, "")
	if rc != 127 {
		t.Errorf("rc=%d, want 127", rc)
	}
	if want := msg + "\nSuggestion: " + sug + "\n"; stderr != want {
		t.Errorf("the shim's output changed.\n got: %q\nwant: %q", stderr, want)
	}
}

// TestShimKeepsShippedGlobPatterns is the counterweight to the validation above: the
// shipped guardrails rule is `-*[rR]*`, a GLOB, and it must survive verbatim into the
// case arm. An allowlist tight enough to reject `*`, `[` or `]` would silently unblock
// `grep -rn` — the exact rule the pack exists to enforce.
func TestShimKeepsShippedGlobPatterns(t *testing.T) {
	e := NewEnv(map[string]string{
		"JAIL_HOME": t.TempDir(),
		"YOLO_BLOCK_CONFIG": `[{"name":"grep","message":"m","suggestion":"s",` +
			`"block_flags":["--recursive","-r","-R","-*[rR]*"]}]`,
	})
	if err := GenerateShims(e); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(e.BlockDir(), "grep"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pat := range []string{"--recursive", "-r", "-R", "-*[rR]*"} {
		if !strings.Contains(string(body), pat) {
			t.Errorf("the shipped pattern %q did not survive into the shim:\n%s", pat, body)
		}
	}
}
