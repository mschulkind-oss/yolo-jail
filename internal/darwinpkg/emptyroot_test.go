package darwinpkg

import (
	"bytes"
	"strings"
	"testing"
)

// Materialize sets cmd.Dir = repoRoot on every nix invocation, and an empty Dir
// inherits the CALLER's working directory rather than meaning "none" — so an
// empty root does not fail, it silently evaluates whatever flake the user is
// standing in. Refusing it here is the floor under run.Run's gate: the caller's
// check can be deleted, this one cannot be reached around.
//
// It must refuse BEFORE spawning nix, so the assertion is on the message rather
// than on any nix output: a machine with no nix on PATH must produce this error
// and not "nix command not found".
func TestMaterializeRefusesEmptyRepoRoot(t *testing.T) {
	var stderr bytes.Buffer
	got, err := Materialize("", []any{"fzf"}, "aarch64-darwin", &stderr)
	if err == nil {
		t.Fatalf("Materialize(\"\") = %+v, nil — want an error; nix would have run in the caller's cwd", got)
	}
	if !strings.Contains(err.Error(), "repo root") {
		t.Errorf("error does not name the missing repo root: %v", err)
	}
	if stderr.Len() != 0 {
		t.Errorf("refusal streamed nix stderr (%q) — it must refuse before spawning nix", stderr.String())
	}
}
