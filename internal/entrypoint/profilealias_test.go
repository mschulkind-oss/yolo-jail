package entrypoint

// profilealias_test.go pins the SHELL half of a pack's launch flags against the
// profiles-as-pack-variants.md §3.4 fold, and pins the CALL SITE that does the folding.
//
// Two consumers read one pack's launch list, and they must agree because shell.go says so:
// packAliases exists so "an interactive shell gets the same flags a `yolo -- <bin>`
// invocation does". The alias folds LaunchFlagsFor over the jail's YOLO_USE_PROFILES
// table; the direct invocation is injected by the host CLI. When only the alias half
// learned about profiles, a pack's variant flags appeared on the alias and vanished from
// `yolo -- <bin>` — the divergence the parity test below exists to catch again.
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

// profileLaunchPack writes a pack that installs `acme`, gives it a static launch flag,
// and declares one `bedrock` variant whose launch replaces the static one for the same
// bin — the shape §3.4's later-wins rule exists to resolve. Returned is the staging root
// the pack was written into, ready to hand to YOLO_PACK_ROOT.
func profileLaunchPack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"acme","contributes":[` +
		`{"kind":"program","bin":"acme","via":"npm","package":"@acme/acme"},` +
		`{"kind":"launch","bin":"acme","flags":["--static"]},` +
		`{"kind":"profile","name":"bedrock",` +
		`"launch":[{"bin":"acme","flags":["--bedrock"]}]}]}`
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

// A variant named in the jail's table must reach the alias. The fold lives inside
// packAliases, so a test on LaunchFlagsFor alone would stay green if the alias stopped
// handing the table over — and a real jail's alias would silently lose the variant's
// flags while every unit test passed.
func TestPackAliasesFoldTheSelectedProfile(t *testing.T) {
	root := profileLaunchPack(t)

	got := packAliases(aliasEnv(t, root, `{"acme":"bedrock"}`))
	if !strings.Contains(got, "--bedrock") {
		t.Errorf("the selected variant's flag must reach the alias, got %q", got)
	}
	if strings.Contains(got, "--static") {
		t.Errorf("the variant replaces the static flags for the same bin (§3.4 OQ-8), got %q", got)
	}

	// No selection (or a name this pack does not declare): the static baseline stands.
	for _, table := range []string{``, `{"acme":"nobody"}`, `{"claude":"bedrock"}`} {
		got := packAliases(aliasEnv(t, root, table))
		if !strings.Contains(got, "--static") || strings.Contains(got, "--bedrock") {
			t.Errorf("table %q: the static flags must stand and no variant may fold, got %q",
				table, got)
		}
	}
}

// THE PARITY: both consumers of a pack's launch list, run over the SAME table, must
// produce the same flags. The direct invocation is represented by packload's injection —
// the same call the host CLI makes — and the alias by packAliases. Either half that stops
// folding the table breaks the equality, which is the property shell.go's doc claims.
func TestAliasAndDirectInvocationAgreeOnAProfile(t *testing.T) {
	root := profileLaunchPack(t)
	e := aliasEnv(t, root, `{"acme":"bedrock"}`)

	packs, err := LoadJailPacks(e)
	if err != nil {
		t.Fatalf("LoadJailPacks: %v", err)
	}
	table := packload.ProfileTable(e.LoadUseProfiles())

	// The direct path: what the host CLI injects into `yolo -- acme user-arg`.
	direct := packload.InjectLaunchFlags(packs, table, []string{"acme", "user-arg"})
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
		`{"kind":"launch","bin":"quotepack","flags":["--opt='a b'"]}]}`
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
	stub := "#!/bin/bash\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$YoloStubOut\"; done\n"
	stub = strings.ReplaceAll(stub, "$YoloStubOut", shquote.Quote(argvFile))
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
