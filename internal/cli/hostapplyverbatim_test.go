package cli

// hostapplyverbatim_test.go pins that `yolo host apply` prints every install command it names
// byte for byte, as the pack wrote it: the remedy and its package-manager alternative in the
// report's remedy groups (printRemedyGroups), and the command the dependency gate runs
// (gateHostDeps). Each was printed through the rich-markup printer unescaped, so a bracketed word
// the printer reads as a style (`acme[red]`) vanished from the command, which then named another
// package, and an unclosed `[` ran on into the closing tag after it. Escaping it would keep an
// invisible U+2060 after the `[`, which a pasted command carries into the install. Each test runs
// the verb itself, so deleting the verbatim print at either call site fails here.

import (
	"bytes"
	"strings"
	"testing"
)

// bracketedDep is a program whose npm package and apt hint both carry a bracket the markup
// printer would read: a style word that closes (`[red]`), and in the hint's step, the one part
// of a hint that may hold a bracket, an opening that never does (`[dim`).
const bracketedDep = `{"kind":"program","bin":"gatebr","via":"npm","package":"acme[red]",` +
	`"install_hints":{"apt":"acme-tools && acme-init [dim"}}`

// assertVerbatim fails when report lacks any of want, carries a U+2060, or prints a closing
// tag as text (a command whose unclosed `[` ran on into the markup after it).
func assertVerbatim(t *testing.T, label, raw string, want ...string) {
	t.Helper()
	if strings.Contains(raw, "\u2060") {
		t.Errorf("%s: the output carries an invisible U+2060, which a pasted command keeps:\n%q", label, raw)
	}
	report := stripANSI(raw)
	for _, w := range want {
		if !strings.Contains(report, w) {
			t.Errorf("%s: the output does not print %q as written:\n%s", label, w, report)
		}
	}
	for _, tag := range []string{"[/cyan]", "[/dim]"} {
		if strings.Contains(report, tag) {
			t.Errorf("%s: %s printed as text, so a command ran on into it:\n%s", label, tag, report)
		}
	}
}

// The dry run's remedy group: the command and its alternative, on a terminal and piped.
func TestHostApplyPrintsABracketedRemedyAsWritten(t *testing.T) {
	depGateFixture(t, bracketedDep)
	for _, color := range []bool{false, true} {
		var out, errw bytes.Buffer
		if rc := applyHost(&out, &errw, color, false, nil); rc != 0 {
			t.Fatalf("color=%v: dry run rc=%d:\n%s%s", color, rc, out.String(), errw.String())
		}
		assertVerbatim(t, map[bool]string{false: "piped", true: "terminal"}[color],
			out.String()+errw.String(),
			"    → npm install -g acme[red]\n",
			"      or via apt: sudo apt install -y acme-tools && acme-init [dim\n")
	}
}

// NO_COLOR on a terminal, through the verb's own entry point and its own color decision.
func TestHostApplyPrintsABracketedRemedyAsWrittenUnderNoColor(t *testing.T) {
	depGateFixture(t, bracketedDep)
	standInTerminal(t)
	t.Setenv("NO_COLOR", "1")
	var rc int
	out := captureStdout(t, func() { rc = runHost([]string{"host", "apply"}) })
	if rc != 0 {
		t.Fatalf("dry run rc=%d:\n%s", rc, out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("NO_COLOR=1 still colored the report, so this is not the case it names:\n%s", out)
	}
	assertVerbatim(t, "NO_COLOR", out,
		"    → npm install -g acme[red]\n",
		"      or via apt: sudo apt install -y acme-tools && acme-init [dim\n")
}

// The --assert gate: the command it is about to run is printed as it runs it.
func TestHostApplyGatePrintsTheCommandItRunsAsWritten(t *testing.T) {
	depGateFixture(t, bracketedDep)
	for _, color := range []bool{false, true} {
		ran := watchInstalls(t, nil)
		var out, errw bytes.Buffer
		applyHost(&out, &errw, color, true, strings.NewReader("y\n"))
		raw := out.String() + errw.String()
		if len(*ran) != 1 || (*ran)[0] != "npm install -g acme[red]" {
			t.Fatalf("color=%v: the gate ran %q, want the pack's command once:\n%s", color, *ran, raw)
		}
		// The gate's own line, after the prompt (which a piped answer leaves unterminated) and
		// before the re-probe's refusal: the groups above print the same command indented deeper.
		assertVerbatim(t, map[bool]string{false: "piped", true: "terminal"}[color], raw,
			"  → npm install -g acme[red]\nhost apply: refused")
	}
}
