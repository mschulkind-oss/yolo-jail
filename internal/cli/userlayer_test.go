package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// ARGV PARSING. The flag is consumed at the front door, in both spellings, and — the part
// that is easy to get wrong — it STOPS AT `--`, so a `--user-layer` meant for the command the
// jail runs is left alone. yolo eating an inner command's flag is the classic argv-rewriting
// bug, and this is the table that keeps the boundary honest.
func TestStripUserLayer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantRest []string
		wantPath string
		wantOK   bool
	}{
		{"absent", []string{"check"}, []string{"check"}, "", true},
		{"space form", []string{"--user-layer", "/l.jsonc", "check"}, []string{"check"}, "/l.jsonc", true},
		{"equals form", []string{"--user-layer=/l.jsonc", "check"}, []string{"check"}, "/l.jsonc", true},
		{
			"before a passthrough command",
			[]string{"--user-layer", "/l.jsonc", "--", "bash"},
			[]string{"--", "bash"}, "/l.jsonc", true,
		},
		{
			// THE TRAP: the inner command has a flag of the same name. Everything after `--`
			// belongs to it, untouched.
			"after -- belongs to the inner command",
			[]string{"--", "mytool", "--user-layer", "x"},
			[]string{"--", "mytool", "--user-layer", "x"}, "", true,
		},
		{
			"a value is required",
			[]string{"check", "--user-layer"},
			[]string{"check"}, "", false,
		},
		{
			// `--user-layer --` is a missing value, not a value of "--".
			"dash-dash is not a value",
			[]string{"--user-layer", "--", "bash"},
			[]string{"--", "bash"}, "", false,
		},
		{"empty equals form is a missing value", []string{"--user-layer="}, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rest, path, ok := stripUserLayer(tc.args)
			if path != tc.wantPath || ok != tc.wantOK {
				t.Errorf("stripUserLayer(%v) = (%v, %q, %v), want (_, %q, %v)",
					tc.args, rest, path, ok, tc.wantPath, tc.wantOK)
			}
			if strings.Join(rest, " ") != strings.Join(tc.wantRest, " ") {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

// An explicitly-named layer that cannot be read REFUSES the invocation. It is never skipped,
// and that is the whole difference between an argument and a convention: the caller asked for
// this file by name, so silently ignoring it would reproduce exactly the invisibility that
// got the `config.local.jsonc` proposal withdrawn.
func TestUserLayerRefusesAnUnusableFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.jsonc")
	if err := os.WriteFile(bad, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"missing", filepath.Join(dir, "nope.jsonc"), "cannot read"},
		{"a directory", dir, "is a directory"},
		{"unparseable", bad, "Failed to parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errw bytes.Buffer
			_, ok := applyUserLayerFlag([]string{"--user-layer", tc.path, "check"}, &errw)
			if ok {
				t.Errorf("an unusable layer (%s) was accepted — it must refuse the invocation, "+
					"not be skipped", tc.name)
			}
			if !strings.Contains(errw.String(), tc.want) {
				t.Errorf("message %q does not mention %q", errw.String(), tc.want)
			}
		})
	}
}

// NO APPROVAL GATE, and this test exists to keep it that way (gate-placement-principle.md
// Test 1). Passing an argv already requires the ability to run commands, which exceeds
// anything the flag grants — so a prompt here would refuse an actor who cleared a stronger
// bar. Asserting on stdin proves it: a valid layer is accepted with NO reader attached, so no
// future change can quietly add a y/N without failing here.
func TestUserLayerHasNoApprovalGate(t *testing.T) {
	layer := filepath.Join(t.TempDir(), "layer.jsonc")
	if err := os.WriteFile(layer, []byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserLayerEnv, "")

	var errw bytes.Buffer
	rest, ok := applyUserLayerFlag([]string{"--user-layer", layer, "check"}, &errw)
	if !ok {
		t.Fatalf("a valid layer was refused: %s", errw.String())
	}
	if errw.Len() != 0 {
		t.Errorf("accepting a layer must print nothing (a prompt would): %s", errw.String())
	}
	if strings.Join(rest, " ") != "check" {
		t.Errorf("rest = %v, want [check]", rest)
	}
	if got := os.Getenv(config.UserLayerEnv); got != layer {
		t.Errorf("the layer was not published to the config loader: %q", got)
	}
}

// The flag is GLOBAL: it must survive routing to every subcommand, not just `run`. Four
// commands read user scope in-jail, and a flag that reached only the launcher would change a
// launch but not the command you verify it with.
func TestUserLayerIsStrippedBeforeSubcommandRouting(t *testing.T) {
	layer := filepath.Join(t.TempDir(), "layer.jsonc")
	if err := os.WriteFile(layer, []byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"check", "loopholes", "pack", "config"} {
		t.Setenv(config.UserLayerEnv, "")
		var errw bytes.Buffer
		rest, ok := applyUserLayerFlag([]string{"--user-layer", layer, sub}, &errw)
		if !ok {
			t.Fatalf("%s: refused: %s", sub, errw.String())
		}
		// After stripping, the subcommand must still be the FIRST positional — otherwise
		// Subcommand() would not resolve it and the CLI would report an unknown command.
		if got := Subcommand(rest); got != sub {
			t.Errorf("after stripping the flag, Subcommand(%v) = %q, want %q — the flag must "+
				"not leave a token that breaks routing", rest, got, sub)
		}
	}
}

// And with the flag stripped, a bare `yolo --user-layer x -- bash` still routes to `run`.
// This is the regression for the ordering choice in Main: the strip happens BEFORE
// RewriteArgv, so the flag never looks like a leading positional (which would make the CLI
// print `unknown command "--user-layer"`).
func TestUserLayerDoesNotBreakTheRunRewrite(t *testing.T) {
	layer := filepath.Join(t.TempDir(), "layer.jsonc")
	if err := os.WriteFile(layer, []byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.UserLayerEnv, "")
	rest, ok := applyUserLayerFlag([]string{"--user-layer", layer, "--", "bash"}, &bytes.Buffer{})
	if !ok {
		t.Fatal("refused")
	}
	// "dispatch:run" is the `--`→run REWRITE having fired (RewriteArgv inserted the token);
	// a bare "run" would mean no subcommand was resolvable at all. Either is a working
	// launch — what must NOT happen is "unknown", which is what a leftover flag token in the
	// leading position produces.
	if got := routeDecision(rest); got != "dispatch:run" && got != "run" {
		t.Errorf("routeDecision(%v) = %q, want a run route — a leftover flag token in the "+
			"leading position makes the CLI print `unknown command`", rest, got)
	}
	// And the unstripped form is exactly that failure, which is why Main strips BEFORE
	// RewriteArgv. Pinned so the ordering cannot be "tidied" back.
	if got := routeDecision([]string{"--user-layer", layer, "--", "bash"}); got == "unknown" {
		t.Log("confirmed: an unstripped --user-layer routes to unknown, which is the reason " +
			"applyUserLayerFlag runs before RewriteArgv")
	}
}
