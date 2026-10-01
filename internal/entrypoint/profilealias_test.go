package entrypoint

// profilealias_test.go pins the SHELL half of a pack's launch flags against the fold, and
// pins the CALL SITE that does the folding.
//
// Two MECHANISMS deliver one pack's launch flags, and they must agree because shell.go says
// so: packAliases exists so "an interactive shell gets the same flags a `yolo -- <bin>`
// invocation does". They now share a PRODUCER — both are packload.InjectLaunchFlags, one
// over the user's argv and one over the bare `<bin>` — so the agreement below is close to
// tautological, and that is the point: it is what fails if the alias ever goes back to
// assembling its own argv beside the injector. Before OQ-PT8 they ALSO read a profile
// table, because a kind:profile body could carry launch flags; that body is gone.
//
// The fixture is a real pack on disk read by LoadJailPacks, not a hand-built struct:
// the alias derivation starts from the loaded set, and a test that bypassed the loader
// would pin the fold while leaving the load unpinned (the unpinned-callee shape AGENTS.md
// names).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// profileLaunchPack writes a pack that installs `acme` and gives its autonomous posture one
// launch flag, plus a profile declaration to make the point the shrink makes: a declared
// profile contributes no flag at all, because the launch body it used to carry has nowhere
// to live.
// Returned is the staging root the pack was written into, ready to hand to YOLO_PACK_ROOT.
func profileLaunchPack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[` +
		`{"kind":"program","bin":"acme","via":"npm","package":"@acme/acme"},` +
		`{"kind":"autonomy","autonomous":{"launch":[{"bin":"acme","flags":["--static"]}]}},` +
		`{"kind":"profile","name":"bedrock","provider":"bedrock"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// aliasEnv is an Env whose pack root holds profileLaunchPack and whose profile table is
// the JSON given — the whole input the alias derivation reads in a real jail.
func aliasEnv(t *testing.T, root, profiles string) *Env {
	t.Helper()
	return NewEnv(map[string]string{
		"JAIL_HOME":           t.TempDir(),
		"YOLO_PACK_ROOT":      root,
		"YOLO_USE_PROFILES":   profiles,
		"YOLO_PACK_PROVIDERS": "",
	})
}

// The alias is derived from the pack's declared flags THROUGH packAliases — not from a
// hand-built map — so deleting the injector call from packAliases fails this test
// rather than leaving the alias silently empty while the fold stays green.
func TestPackAliasesFoldTheLaunchContributions(t *testing.T) {
	root := profileLaunchPack(t)

	got := packAliases(aliasEnv(t, root, ``))
	if !strings.Contains(got, "--static") {
		t.Errorf("the pack's launch flag must reach the alias, got %q", got)
	}

	// A declared profile adds nothing to the same alias, whatever the jail's table says:
	// the flag list has one source, and it is not the profile.
	for _, table := range []string{``, `{"acme":"bedrock"}`, `{"claude":"bedrock"}`} {
		got := packAliases(aliasEnv(t, root, table))
		if !strings.Contains(got, "--static") {
			t.Errorf("table %q: the static flags must stand, got %q", table, got)
		}
		if strings.Contains(got, "--bedrock") {
			t.Errorf("table %q: a profile contributes no launch flag since the shrink, got %q",
				table, got)
		}
	}
}

// THE PARITY: both mechanisms must produce the same flags. The direct invocation is
// represented by packload's injection — the same call the host CLI makes — and the alias by
// packAliases, which now makes that very call over the bare `acme`. So this asserts the
// rendering around it: the alias must be the injected argv, shell-quoted, with the user's
// own arguments belonging to the direct path alone. It fails the moment either side
// re-derives the flag list instead of taking the injector's.
func TestAliasAndDirectInvocationAgree(t *testing.T) {
	root := profileLaunchPack(t)
	e := aliasEnv(t, root, `{"acme":"bedrock"}`)

	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("LoadJailPacks: %v", err)
	}

	// The direct path: what the host CLI injects into `yolo -- acme user-arg`.
	direct, _ := packload.InjectLaunchFlags(packs, true, []string{"acme", "user-arg"})
	if len(direct) != 3 || direct[0] != "acme" || direct[len(direct)-1] != "user-arg" {
		t.Fatalf("injected argv %v — the direct path must keep the user's own arguments", direct)
	}
	injected := direct[1 : len(direct)-1]

	// The alias path: the rendered alias line, rebuilt from what the direct path injected.
	want := "alias acme=" + shquote.Quote(shquote.Join(append([]string{"acme"}, injected...)))
	if got := packAliases(e); got != want {
		t.Errorf("alias and direct invocation disagree:\n alias:  %s\n direct: %s", got, want)
	}
}

// A flag carrying a quote must not break the generated .bashrc. The alias line is shell
// source, so a pack-declared flag is code the jail sources on every interactive shell —
// the quoted form has to survive both a syntax check and the expansion itself, and the
// expansion has to deliver the flag to the binary as ONE argv element, exactly what a
// `yolo -- quotepack "--opt='a b'"` invocation would pass.
func TestAliasWithAQuotedFlagStaysValidShell(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "quotepack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"quotepack","contributes":[` +
		`{"kind":"program","bin":"quotepack","via":"npm","package":"@acme/quotepack"},` +
		`{"kind":"autonomy","autonomous":{"launch":[` +
		`{"bin":"quotepack","flags":["--opt='a b'"]}]}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	e := aliasEnv(t, root, ``)
	got := packAliases(e)
	if !strings.Contains(got, `'`) {
		t.Fatalf("a quoted flag must be quoted in the alias, got %q", got)
	}
	if err := GenerateBashrc(e); err != nil {
		t.Fatalf("GenerateBashrc: %v", err)
	}
	bashrc := filepath.Join(e.Home, ".bashrc")

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not found")
	}
	if out, err := exec.Command(bash, "-n", bashrc).CombinedOutput(); err != nil {
		t.Fatalf("bash -n rejected the generated .bashrc: %v\n%s\n%s", err, out, readFileString(t, bashrc))
	}

	// And the expansion. A stub on the rcfile's PATH records the argv it was handed, so
	// this asserts the words the agent actually receives rather than the text of the line.
	argvFile := filepath.Join(e.Home, "argv")
	stubDir := filepath.Join(e.Home, ".local", "bin")
	if err := os.MkdirAll(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The path is spliced in quoted and bare: inside double quotes as well, a quoted path
	// is a file name with the single quotes in it.
	stub := "#!/bin/bash\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> " +
		shellSingleQuote(argvFile) + "; done\n"
	if err := os.WriteFile(filepath.Join(stubDir, "quotepack"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bash, "--rcfile", bashrc, "-i")
	cmd.Env = append(os.Environ(), "HOME="+e.Home, "TERM=dumb")
	cmd.Stdin = strings.NewReader("quotepack\nexit\n")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("interactive bash over the generated .bashrc: %v\n%s", err, out.String())
	}
	argv := readFileString(t, argvFile)
	if argv != "--opt='a b'\n" {
		t.Errorf("the alias must hand the binary the flag as ONE word, got %q\n%s", argv, out.String())
	}
}
