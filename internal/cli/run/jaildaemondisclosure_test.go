package run

// jaildaemondisclosure_test.go pins the JAIL_DAEMON half of trust-paths.md OQ-TP10: a loophole
// that declares a `jail_daemon` runs a supervised process inside the jail, and until this
// half was built a loophole declaring ONLY that produced no claim, no footprint line and no
// launch line (trust-paths.md §3.2, inventory row 19).
//
// Every test reads the launch's OWN answer to "will it run": the payload jailDaemonsFor
// composes (the value the container argv serializes and the macos-user guest's supervisor
// receives), split by loopholes.JailDaemonsRunIn. A disclosure that walked the manifests
// itself would announce a disabled or declined daemon, which is the overclaim the ruling
// names, so the controls below are as load-bearing as the positive case.

import (
	"bytes"
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jailOnlyLoophole is a loophole manifest declaring a jail daemon and nothing else: no host
// daemon, no doctor, no intercept, no bind. The shape moduleClaims emits no claim for.
func jailOnlyLoophole(name string, cmd string) string {
	return `{"name": "` + name + `", "description": "a relay inside the jail",
		"default_enabled": true, "transport": "none",
		"jail_daemon": {"cmd": ` + cmd + `}}`
}

// jailDaemonBoundary runs the spawn boundary for rt over one jail-only loophole pack, with the
// payload composed by the production composer, and returns the payload's names and what the
// launch printed.
func jailDaemonBoundary(t *testing.T, rt string, cfg *jsonx.OrderedMap) (map[string]bool, string) {
	t.Helper()
	return jailDaemonBoundaryFor(t, rt, cfg, func() *packload.Pack {
		return writeRealLoopholePack(t, "acme", "acme-relay",
			jailOnlyLoophole("acme-relay", `["yolo-jaild", "acme-relay"]`))
	})
}

// jailDaemonBoundaryFor is jailDaemonBoundary over the pack mkPack writes, which it calls once
// HOME points at a scratch directory.
func jailDaemonBoundaryFor(t *testing.T, rt string, cfg *jsonx.OrderedMap,
	mkPack func() *packload.Pack) (map[string]bool, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	packs := []*packload.Pack{mkPack()}
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules(packs))

	cname := "yolo-jaildaemon-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := goldenOptions(t.TempDir(), home)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	if rt == "macos-user" {
		t.Cleanup(func() { o.endServicesSession(nil) })
	}
	payload := o.jailDaemonsFor(cfg, rt, packs)
	names := map[string]bool{}
	for _, s := range payload {
		names[s.Name] = true
	}
	handles, _ := o.startLoopholesDisclosed(cname, rt, cfg, packs, payload)
	for _, h := range handles {
		if h.stop != nil {
			h.stop()
		}
	}
	return names, errBuf.String()
}

// THE LINE PRINTS, AT THE REAL BOUNDARY, for a daemon the launch really runs.
//
// Driven through startLoopholesDisclosed with the payload jailDaemonsFor composed, so the
// test fails if the boundary stops disclosing jail code, if the disclosure stops reading the
// payload, or if the payload stops carrying the daemon (the precondition below says which).
func TestALoopholeJailDaemonIsDisclosedAtTheSpawnBoundary(t *testing.T) {
	names, got := jailDaemonBoundary(t, "podman", newConfig())
	if !names["acme-relay"] {
		t.Fatalf("the payload does not carry the fixture's jail daemon (%v), so this test could "+
			"not tell a missing line from a daemon that never runs:\n%s", names, got)
	}
	want := "acme: 1 jail daemon runs in the jail — acme-relay"
	if !strings.Contains(got, want) {
		t.Fatalf("a loophole whose only declaration is a jail_daemon reached the jail's "+
			"supervisor with no launch line naming it (trust-paths.md §3.2). OQ-TP9 deleted the "+
			"approval gate and kept the disclosure as the whole boundary, so a daemon nobody is "+
			"told about is outside it. Want %q in:\n%s", want, got)
	}
	if !strings.Contains(got, "runs inside the jail") {
		t.Errorf("the daemon is named outside the jail-code block, whose heading is what says "+
			"where it runs:\n%s", got)
	}
	if strings.Contains(got, "runs pack code on your machine") {
		t.Errorf("a jail daemon is announced in the HOST execution block. It runs nothing on "+
			"the user's machine; that block's value is that every line in it does:\n%s", got)
	}
	// The header points at the footprint only for plugins: a jail daemon has no claim, so
	// `yolo pack footprint` lists nothing for it, and a pointer there sends the reader to a
	// report that does not mention what the line named.
	if strings.Contains(got, "yolo pack footprint") {
		t.Errorf("a block naming only a jail daemon points at `yolo pack footprint`, which "+
			"lists nothing for one:\n%s", got)
	}
}

// writePluginAndJailDaemonPack writes one pack that ships a wrapped plugin declaring a hook
// and a loophole declaring only a jail daemon, loaded through packload like the fixtures above.
func writePluginAndJailDaemonPack(t *testing.T) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	plugin := filepath.Join(root, "skills", "acme-tools", ".claude-plugin")
	mod := filepath.Join(root, "loopholes", "acme-relay")
	for _, d := range []string{plugin, mod} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(plugin, "plugin.json"): `{"name":"acme-tools","skills":["./"],` +
			`"hooks":{"PreToolUse":[]}}`,
		filepath.Join(mod, "manifest.jsonc"): jailOnlyLoophole("acme-relay", `["yolo-jaild", "acme-relay"]`),
		filepath.Join(root, "pack.json"): `{"contributes":[` +
			`{"kind":"skills","from":"skills","into":".claude/skills"},` +
			`{"kind":"loophole","from":"loopholes/acme-relay"}]}`,
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("the plugin-and-loophole pack fixture does not load: %v", probs)
	}
	return p
}

// A PACK WITH BOTH IS STILL ONE LINE (report-tiers.md P1): the plugin counts, then the jail
// daemons, on the pack's one line, and the header keeps its footprint pointer because that
// line counts a plugin. Through the real boundary, like the test above.
func TestAPacksPluginCodeAndJailDaemonShareOneLine(t *testing.T) {
	names, got := jailDaemonBoundaryFor(t, "podman", newConfig(), func() *packload.Pack {
		return writePluginAndJailDaemonPack(t)
	})
	if !names["acme-relay"] {
		t.Fatalf("the payload does not carry the fixture's jail daemon (%v):\n%s", names, got)
	}
	var lines []string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "acme:") {
			lines = append(lines, l)
		}
	}
	want := "acme: 1 wrapped plugin runs code in the jail — hooks (1); " +
		"1 jail daemon runs in the jail — acme-relay"
	if len(lines) != 1 || !strings.Contains(lines[0], want) {
		t.Fatalf("a pack shipping a plugin hook and a jail daemon did not get one line with "+
			"both, plugin counts first. Want one line containing %q; the launch said:\n%s", want, got)
	}
	if !strings.Contains(got, "(`yolo pack footprint` names each plugin)") {
		t.Errorf("the block counts a plugin and its header no longer points at the report "+
			"that itemizes it:\n%s", got)
	}
}

// A DAEMON THE LAUNCH DOES NOT RUN IS NOT ANNOUNCED. The user's own switch turns the loophole
// off, the payload drops it, and the line must drop it too: announcing it would be the
// overclaim OQ-TP10 names ("a declared daemon whose loophole is disabled … starts nothing").
// A disclosure written over the manifests rather than over the payload prints it here.
func TestADisabledLoopholesJailDaemonIsNotDisclosed(t *testing.T) {
	off := jsonx.NewOrderedMap()
	off.Set("enabled", false)
	lp := jsonx.NewOrderedMap()
	lp.Set("acme-relay", off)
	names, got := jailDaemonBoundary(t, "podman", newConfig("loopholes", lp))
	if names["acme-relay"] {
		t.Fatalf("the payload still carries a loophole the config switched off (%v); the "+
			"control below would measure nothing", names)
	}
	if strings.Contains(got, "acme-relay") {
		t.Errorf("the launch named a jail daemon it does not run:\n%s", got)
	}
}

// writeLocalLoopholes is writeLocalLoopholePack for several modules in the one local pack.
func writeLocalLoopholes(t *testing.T, home string, manifests map[string]string) {
	t.Helper()
	local := filepath.Join(home, ".config", "yolo-jail", "local")
	var contributes []string
	for name, manifest := range manifests {
		mod := filepath.Join(local, "loopholes", name)
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		contributes = append(contributes, `{"kind":"loophole","from":"loopholes/`+name+`"}`)
	}
	body := `{"contributes":[` + strings.Join(contributes, ",") + `]}`
	if err := os.WriteFile(filepath.Join(local, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// THROUGH Run(), on the backend that runs some jail daemons and declines others: the line
// names exactly what the guest's supervisor is handed. A daemon whose program is a Linux
// executable in its module directory is declined there (loopholes.JailDaemonsRunIn) and said so
// by the decline report, so naming it as running in the jail would contradict that report on
// the same screen.
func TestMacosUserLaunchDisclosesTheJailDaemonsItsGuestRuns(t *testing.T) {
	home := packHome(t)
	ws := t.TempDir()
	writeLocalLoopholes(t, home, map[string]string{
		"acme-relay":   jailOnlyLoophole("acme-relay", `["yolo-jaild", "acme-relay"]`),
		"acme-mounted": jailOnlyLoophole("acme-mounted", `["{jail_loophole_dir}/bin/relay"]`),
	})
	writeLocalModuleFile(t, home, "acme-mounted", "bin/relay", linuxProgram)
	writeUserConfigJSON(t, home, `{"packs": []}`)

	got := macosUserLaunch(t, ws)
	if got.rc != 0 {
		t.Fatalf("Run() = %d, want 0\n%s", got.rc, got.out)
	}
	guest := map[string]bool{}
	for _, n := range got.jailDaemons.Names() {
		guest[n] = true
	}
	if !guest["acme-relay"] || guest["acme-mounted"] {
		t.Fatalf("the guest's supervisor was handed %v; want acme-relay and not acme-mounted, "+
			"or the assertions below measure a different split:\n%s", got.jailDaemons.Names(), got.out)
	}
	var line string
	for _, l := range strings.Split(got.out, "\n") {
		if strings.Contains(l, "jail daemon") && strings.Contains(l, "runs in the jail") {
			line = l
		}
	}
	if !strings.Contains(line, "acme-relay") {
		t.Fatalf("the macos-user launch handed its guest a jail daemon and named it nowhere:\n%s", got.out)
	}
	if strings.Contains(line, "acme-mounted") {
		t.Errorf("the line names a daemon this backend declines as running in the jail: %q", line)
	}
}

// THE CONTAINER ARM HANDS THE BOUNDARY ITS PAYLOAD. runContainer cannot be driven by a unit
// test (it starts a real container), so its call is pinned in the source, as
// TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec pins its order: the disclosure
// it prints before the keeper's spawn must read jailDaemons, the value its argv serializes.
// Passing nil there would silence every jail daemon on podman while every test above passed.
func TestTheContainerArmDisclosesTheJailDaemonsItsArgvCarries(t *testing.T) {
	var calls []*ast.CallExpr
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && skelCallee(call) == "discloseLoopholes" {
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 1 {
		t.Fatalf("runContainer calls discloseLoopholes %d times, want 1", len(calls))
	}
	args := calls[0].Args
	if len(args) == 0 || skelIdent(args[len(args)-1]) != "jailDaemons" {
		t.Errorf("runContainer's disclosure is not handed jailDaemons, the payload its argv " +
			"serializes, so a podman launch names none of the jail daemons it runs")
	}
}
