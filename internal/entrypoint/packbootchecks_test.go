package entrypoint

// packbootchecks_test.go pins the boot_check hook (docs/reference/mcp-configuration.md, OQ-MP9):
// the chrome-devtools wrapper's --boot-check, the boot step that runs a declared check and prints
// what it says, and the rule that a check which fails, hangs or is missing never fails the boot.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// --boot-check is the browser half of --check alone: silent with a browser, one line naming the
// next step without one, and it never asks for the server, which the boot runs before installing.
func TestTheWrapperBootCheckReportsOnlyAMissingBrowser(t *testing.T) {
	w := newWrapperWorld(t)
	path := resolvedDir(t)
	env := []string{"HOME=" + w.home, "PATH=" + path}

	out, errs, rc := w.run(t, env, "--boot-check")
	lines := nonEmptyLines(out)
	if rc != 1 || len(lines) != 1 || errs != "" {
		t.Fatalf("no browser: rc=%d, want 1 and exactly one line on stdout:\nstdout=%q\nstderr=%q", rc, out, errs)
	}
	for _, want := range []string{"browser: none found", "install Chrome or Chromium", `"chromium"`, "next start"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the line does not say %q:\n%s", want, lines[0])
		}
	}

	exe(t, filepath.Join(path, "chromium"))
	if out, errs, rc := w.run(t, env, "--boot-check"); rc != 0 || out != "" || errs != "" {
		t.Errorf("a browser on PATH, no server installed: rc=%d stdout=%q stderr=%q, want 0 and silence", rc, out, errs)
	}
	if len(logLines(t, w.log)) != 0 {
		t.Error("--boot-check started the server")
	}

	// The standard install paths count too: it is the search a server start runs.
	w2 := newWrapperWorld(t)
	exe(t, filepath.Join(w2.root, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"))
	if out, _, rc := w2.run(t, []string{"HOME=" + w2.home, "PATH=" + resolvedDir(t)}, "--boot-check"); rc != 0 || out != "" {
		t.Errorf("the Mac app bundle: rc=%d stdout=%q, want 0 and silence", rc, out)
	}
}

// bootCheckStep is the boot table's own run_pack_boot_checks step, so a test driving it fails
// when the table stops calling the checks.
func bootCheckStep(t *testing.T) bootStep {
	t.Helper()
	for _, s := range bootSteps() {
		if s.name == "run_pack_boot_checks" {
			if s.run == nil {
				t.Fatal("run_pack_boot_checks is not a run step")
			}
			if s.notContainer != "" || s.notDarwin != "" {
				t.Fatalf("run_pack_boot_checks is excluded from a boot: %q / %q", s.notContainer, s.notDarwin)
			}
			return s
		}
	}
	t.Fatal("the boot table has no run_pack_boot_checks step")
	return bootStep{}
}

// bootCheckEnv is an Env whose terminal and boot log are captured, with home and the agent's
// PATH (agentPath, through the macos-user login PATH it honors first) of the test's own.
func bootCheckEnv(t *testing.T) (*Env, *strings.Builder, *strings.Builder, string) {
	t.Helper()
	home, path := resolvedDir(t), resolvedDir(t)
	e := NewEnv(map[string]string{"JAIL_HOME": home, DarwinLoginPathEnv: path})
	var term, log strings.Builder
	e.Stderr, e.LogOnly = &term, &log
	return e, &term, &log, path
}

// runBootCheckStep runs the table's step over packs, for both boots.
func runBootCheckStep(t *testing.T, e *Env, target bootTarget, packs []*packload.Pack) {
	t.Helper()
	b := &bootRun{e: e, target: target, packsLoaded: true, packs: packs}
	bootCheckStep(t).run(b)
}

// checkPack is a pack declaring one boot_check of script body (written into home) with args.
func checkPack(t *testing.T, e *Env, name, body string, args ...string) *packload.Pack {
	t.Helper()
	rel := ".local/share/" + name + "/check"
	if body != "" {
		p := filepath.Join(e.Home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	flags := ""
	if len(args) > 0 {
		flags = `,"flags":["` + strings.Join(args, `","`) + `"]`
	}
	decl, problems := packdecl.Decode([]byte(`{"name":"` + name + `","contributes":[
		{"kind":"hook","hook":"boot_check","from":"` + rel + `"` + flags + `}]}`))
	if len(problems) != 0 {
		t.Fatalf("decoding the fixture: %v", problems)
	}
	return &packload.Pack{Name: name, Decl: decl}
}

// THE BOOT RUNS THE SHIPPED PACK'S CHECK AND PRINTS ITS LINE: the chrome-devtools pack as staged,
// its wrapper where its files contribution puts it, and no browser on the agent's PATH or at a
// standard path. With one, the launch says nothing.
func TestTheBootPrintsTheChromeDevtoolsPacksMissingBrowserLine(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	for _, target := range []bootTarget{bootContainer} {
		t.Run(target.String(), func(t *testing.T) {
			t.Setenv("YOLO_CHROME_DEVTOOLS_ROOT", resolvedDir(t))
			e, term, _, path := bootCheckEnv(t)
			w := newWrapperWorld(t)
			body, err := os.ReadFile(w.wrapper)
			if err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(e.Home, chromeWrapperRel)
			if err := os.MkdirAll(filepath.Dir(wrapper), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(wrapper, body, 0o444); err != nil {
				t.Fatal(err)
			}
			packs := []*packload.Pack{materializedPack(t, "chrome-devtools")}

			runBootCheckStep(t, e, target, packs)
			got := nonEmptyLines(term.String())
			if len(got) != 1 || !strings.HasPrefix(got[0], "  ⚠ chrome-devtools: browser: none found") ||
				!strings.Contains(got[0], "install Chrome or Chromium") {
				t.Fatalf("want the pack's one line, prefixed with its name; the terminal got:\n%s", term.String())
			}
			if err := genFailuresError(e); err != nil {
				t.Errorf("a missing browser failed the boot: %v", err)
			}

			term.Reset()
			exe(t, filepath.Join(path, "google-chrome"))
			runBootCheckStep(t, e, target, packs)
			if term.Len() != 0 {
				t.Errorf("with a browser on the agent's PATH the launch printed:\n%s", term.String())
			}
		})
	}
}

// A sealed build jail runs no agent, so it runs no check; a container session's pass skips the
// step, so an attach does not repeat the warning.
func TestTheBootCheckRunsOncePerBoot(t *testing.T) {
	e, term, _, _ := bootCheckEnv(t)
	packs := []*packload.Pack{checkPack(t, e, "noisy", "echo said\n")}
	b := &bootRun{e: e, target: bootContainer, packsLoaded: true, packs: packs, sealed: true}
	bootCheckStep(t).run(b)
	if term.Len() != 0 {
		t.Errorf("a sealed build ran the check:\n%s", term.String())
	}
	if bootCheckStep(t).notSessionPass == "" {
		t.Error("a container session's pass would run the check again on every attach")
	}
	runBootCheckStep(t, e, bootContainer, packs)
	if got := term.String(); got != "  ⚠ noisy: said\n" {
		t.Errorf("the boot printed %q, want the check's line once", got)
	}
}

// The declared flags reach the script, and HOME is the jail's home.
func TestTheBootCheckGetsItsFlagsAndHome(t *testing.T) {
	e, term, _, _ := bootCheckEnv(t)
	packs := []*packload.Pack{checkPack(t, e, "argy", `printf '%s|%s|%s\n' "$1" "$2" "$HOME"`+"\n", "--boot-check", "two words")}
	runBootCheckStep(t, e, bootContainer, packs)
	if want := "  ⚠ argy: --boot-check|two words|" + e.Home + "\n"; term.String() != want {
		t.Errorf("got %q, want %q", term.String(), want)
	}
}

// A CHECK THAT FAILS, HANGS, FLOODS OR IS MISSING NEVER FAILS THE BOOT. Each costs one line that
// names the command showing the check's own answer, and the step returns within the bound.
func TestABrokenBootCheckNeverFailsTheBoot(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on PATH")
	}
	saved := bootCheckTimeout
	bootCheckTimeout = 300 * time.Millisecond
	t.Cleanup(func() { bootCheckTimeout = saved })

	cases := []struct {
		name, body string
		want       []string
	}{
		{"silent-failure", "exit 3\n", []string{"exited 3 without saying why", "/bin/sh ~/.local/share/silent-failure/check"}},
		{"hangs", sleep + " 30\n", []string{"did not finish within 300ms", "the launch goes on", "/bin/sh ~/.local/share/hangs/check"}},
		// A grandchild holding the output open must not hold the boot either.
		{"grandchild", "/bin/sh -c '" + sleep + " 30; :'; :\n", []string{"did not finish within 300ms"}},
		{"floods", "for i in 1 2 3 4 5 6 7; do echo line $i; done; exit 1\n", []string{"line 4", "(3 more lines", "/bin/sh ~/.local/share/floods/check"}},
		{"missing", "", []string{"~/.local/share/missing/check is not in this home", "yolo check"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, term, _, _ := bootCheckEnv(t)
			packs := []*packload.Pack{checkPack(t, e, c.name, c.body)}
			start := time.Now()
			runBootCheckStep(t, e, bootContainer, packs)
			if took := time.Since(start); took > 5*time.Second {
				t.Errorf("the step took %s; a hung check held the boot", took)
			}
			for _, w := range c.want {
				if !strings.Contains(term.String(), w) {
					t.Errorf("the terminal does not say %q:\n%s", w, term.String())
				}
			}
			if strings.Contains(term.String(), "line 5") {
				t.Errorf("more than %d lines of one check reached the terminal:\n%s", bootCheckMaxLines, term.String())
			}
			if err := genFailuresError(e); err != nil {
				t.Errorf("the boot failed: %v", err)
			}
		})
	}
}

// A MACOS-USER BOOT RUNS NO PACK'S CHECK. Its bootstrap is outside the agent's Seatbelt profile
// (the account mode's is unconfined), and P4 lets it run only yolo's own code; a check is a
// pack's script in an agent-writable home. So the script never executes there, the terminal gets
// nothing, and the boot log names the skip.
func TestAMacosUserBootNeverRunsABootCheck(t *testing.T) {
	e, term, log, _ := bootCheckEnv(t)
	marker := filepath.Join(e.Home, "ran")
	packs := []*packload.Pack{checkPack(t, e, "planted", "echo said; : > "+shquote.Quote(marker)+"\n")}

	runBootCheckStep(t, e, bootDarwin, packs)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a macos-user boot executed a pack's boot check outside the agent's sandbox")
	}
	if term.Len() != 0 {
		t.Errorf("a macos-user boot printed:\n%s", term.String())
	}
	if !strings.Contains(log.String(), "boot check planted (~/.local/share/planted/check): skipped on macos-user") {
		t.Errorf("the boot log does not record the skip:\n%s", log.String())
	}

	// The same pack on a container boot does run, so the marker's absence above is the skip.
	runBootCheckStep(t, e, bootContainer, packs)
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the container boot did not run the check: %v", err)
	}
}

// A TIMED-OUT CHECK TAKES ITS WHOLE PROCESS GROUP WITH IT: a background child it started does not
// outlive the bound to act later.
func TestATimedOutBootCheckLeavesNoGrandchild(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on PATH")
	}
	saved := bootCheckTimeout
	bootCheckTimeout = 300 * time.Millisecond
	t.Cleanup(func() { bootCheckTimeout = saved })

	e, _, _, _ := bootCheckEnv(t)
	late := filepath.Join(e.Home, "late")
	body := "( " + sleep + " 1; : > " + shquote.Quote(late) + " ) </dev/null >/dev/null 2>&1 &\n" + sleep + " 30\n"
	runBootCheckStep(t, e, bootContainer, []*packload.Pack{checkPack(t, e, "spawner", body)})
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(late); err == nil {
		t.Error("a grandchild of a timed-out check survived and acted after the boot moved on")
	}
}

// One check's output is held to bootCheckMaxBytes, however much it writes.
func TestABootChecksOutputIsCapped(t *testing.T) {
	var c cappedBuffer
	chunk := strings.Repeat("x", 1000)
	for range 2 * bootCheckMaxBytes / len(chunk) {
		if n, err := c.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write = %d, %v; a capped write must still report success", n, err)
		}
	}
	if got := len(c.String()); got != bootCheckMaxBytes {
		t.Errorf("held %d bytes, want %d", got, bootCheckMaxBytes)
	}
}

// The host refuses `flags` on every hook but boot_check, where nothing would read them, and an
// empty argument on boot_check.
func TestHookFlagsAreBootChecksAlone(t *testing.T) {
	_, problems := packdecl.Decode([]byte(`{"name":"x","contributes":[
		{"kind":"hook","hook":"per_jail_history","from":".x/history","flags":["--y"]}]}`))
	if !strings.Contains(strings.Join(problems, "\n"), `does not take "flags"`) {
		t.Errorf("flags on per_jail_history were accepted: %v", problems)
	}
	_, problems = packdecl.Decode([]byte(`{"name":"x","contributes":[
		{"kind":"hook","hook":"boot_check","from":".x/check","flags":[""]}]}`))
	if !strings.Contains(strings.Join(problems, "\n"), "is not an argument") {
		t.Errorf("an empty boot_check argument was accepted: %v", problems)
	}
	_, problems = packdecl.Decode([]byte(`{"name":"x","contributes":[
		{"kind":"hook","hook":"boot_check","flags":["--boot-check"]}]}`))
	if !strings.Contains(strings.Join(problems, "\n"), `needs "from"`) {
		t.Errorf("a boot_check with no script was accepted: %v", problems)
	}
}
