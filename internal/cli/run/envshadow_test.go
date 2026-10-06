package run

// envshadow_test.go pins OQ-NC12's DISCLOSURE (ruled 2026-10-05) at every JAIL vehicle: a launch
// names each variable for which one of yolo's own sources beat another that set it otherwise,
// one line per name, the winner and every loser, never a value, and prints nothing when nothing
// is shadowed. Each test drives an arm and reads what it printed, so deleting that arm's
// noteCredentialScope call, or the noteShadowedEnv call inside it, fails it: the fresh podman
// launch through Run, the fresh Apple Container launch through Run, the attach's delivery on
// both runtimes, and the macos-user launch through Run. The wording is packload's
// (envshadow_test.go there); the host's pins are internal/cli's.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// The fixture's values, none of which a launch may print.
const (
	shadowPackValue = "pack-value-7f3a"
	shadowESValue   = "es-value-9c1d"
	shadowBaseES    = "es-base-2b8e"
	shadowShape     = "shape-base-4e6f"
	shadowKey       = "key-value-5a0c"
)

// shadowFixtureValues is every value the fixture composes.
var shadowFixtureValues = []string{shadowPackValue, shadowESValue, shadowBaseES, shadowShape, shadowKey}

// The lines the fixture's launch must print: env_sources over the local pack's value for every
// process, and the fxp profile's value over env_sources for fxa alone.
const (
	wantPackShadow    = "Shadowed SHADOW_K: your env_sources value wins over the local pack's value"
	wantProfileShadow = "Shadowed FXP_BASE: the fxp profile's value wins over your env_sources value, for fxa"
)

// shadowPackManifest is the conventional local pack: it installs fxa, ships the provider fxp
// claiming FXP_KEY and a profile over it, and sets SHADOW_K for every process; its derive sets
// FXP_BASE for fxa.
const shadowPackManifest = `{"name":"local","contributes":[` +
	`{"kind":"program","bin":"fxa","via":"npm","package":"@acme/fxa"},` +
	`{"kind":"provider","name":"fxp","api_key_env_name":"FXP_KEY"},` +
	`{"kind":"profile","name":"fxp","provider":"fxp"},` +
	`{"kind":"env","vars":{"SHADOW_K":"` + shadowPackValue + `"}}]}`

const shadowPackDerive = `yolo.env("fxa", function(ctx)
  return {FXP_BASE = "` + shadowShape + `"}
end)
`

// writeShadowFixture writes the local pack under home and a user config selecting fxp for fxa,
// whose env_sources sets SHADOW_K and FXP_BASE (each shadowed) and FXP_KEY (fxp's key). shadowed
// false keeps the pack and the profile and moves env_sources off both names, so nothing is.
func writeShadowFixture(t *testing.T, home string, shadowed bool) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(shadowPackManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"), []byte(shadowPackDerive), 0o644); err != nil {
		t.Fatal(err)
	}
	k, base := "SHADOW_K", "FXP_BASE"
	if !shadowed {
		k, base = "OTHER_K", "OTHER_BASE"
	}
	writeUserConfigJSON(t, home, `{"packs": [], "profile": {"fxa": "fxp"}, "env_sources": [{`+
		`"`+k+`": "`+shadowESValue+`", "`+base+`": "`+shadowBaseES+`", "FXP_KEY": "`+shadowKey+`"}]}`)
}

// requireShadowLines fails unless out carries both fixture lines, each once (or, unshadowed, no
// shadow line at all), and fails for a value: the user's and the profile's anywhere, and the
// pack's own in a shadow line (the pack environment disclosure names a pack's declared value by
// design, docs/reference/report-tiers.md).
func requireShadowLines(t *testing.T, vehicle, out string, shadowed bool) {
	t.Helper()
	if shadowed {
		for _, want := range []string{wantPackShadow, wantProfileShadow} {
			if n := strings.Count(out, want); n != 1 {
				t.Errorf("%s printed %q %d times, want once:\n%s", vehicle, want, n, out)
			}
		}
	} else if strings.Contains(out, "Shadowed ") {
		t.Errorf("%s: nothing is shadowed, yet the launch printed a shadow line:\n%s", vehicle, out)
	}
	for _, line := range strings.Split(out, "\n") {
		for _, v := range shadowFixtureValues {
			if strings.Contains(line, v) && (v != shadowPackValue || strings.Contains(line, "Shadowed ")) {
				t.Errorf("%s printed the value %q: %s", vehicle, v, line)
			}
		}
	}
}

// containerLaunchOut drives one fresh launch on rt ("podman" or Apple Container's "container")
// through Run() to the runtime's `run`, with a fake runtime first on PATH, and returns everything
// it printed. Apple Container takes the macOS arm of every probe, as on a Mac.
func containerLaunchOut(t *testing.T, rt string) string {
	t.Helper()
	ws := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, rt), []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, rt, &stdout, &stderr, nil)
	if rt == "container" {
		o.IsMacOS, o.IsLinux = true, false
	}
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool { return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch strings.Join(argv, " ") {
		case "container --version":
			return ExecResult{Ran: true, RC: 0, Stdout: "container CLI version 1.1.0 (build: release, commit: 0000000)"}
		case "container system status":
			return ExecResult{Ran: true, RC: 0, Stdout: "apiserver is running"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.Getenv = func(k string) string {
		switch k {
		case "YOLO_RUNTIME":
			return rt
		case "YOLO_NO_AUTO_IMAGE_REAP":
			return "1"
		}
		return ""
	}
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(yoloruntime.FromWorkspace(ws), false)) })
	Run(*o)
	return stdout.String() + stderr.String()
}

// THE FRESH CONTAINER LAUNCH, podman and Apple Container: runContainer's noteCredentialScope.
func TestAFreshContainerLaunchNamesWhatItShadowed(t *testing.T) {
	hostLauncher(t)
	for _, rt := range []string{"podman", "container"} {
		for _, shadowed := range []bool{true, false} {
			home := packHome(t)
			writeShadowFixture(t, home, shadowed)
			out := containerLaunchOut(t, rt)
			if !strings.Contains(out, `Profile fxp: declared by local`) || !strings.Contains(out, " | "+rt+"\n") {
				t.Fatalf("%s: the launch never reached its disclosures on that runtime:\n%s", rt, out)
			}
			requireShadowLines(t, rt+" fresh launch", out, shadowed)
		}
	}
}

// THE ATTACH, on both container runtimes: deliverChannelOnAttach's noteCredentialScope, the
// delivery an attach performs into a running jail.
func TestAnAttachNamesWhatItShadowed(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		for _, shadowed := range []bool{true, false} {
			pack := inlinePack(t, "local", shadowPackManifest)
			if err := os.WriteFile(filepath.Join(pack.Root, "derive.lua"), []byte(shadowPackDerive), 0o644); err != nil {
				t.Fatal(err)
			}
			k, base := "SHADOW_K", "FXP_BASE"
			if !shadowed {
				k, base = "OTHER_K", "OTHER_BASE"
			}
			userEnv := jsonx.NewOrderedMap()
			userEnv.Set(k, shadowESValue)
			userEnv.Set(base, shadowBaseES)
			userEnv.Set("FXP_KEY", shadowKey)
			packs := []*packload.Pack{pack}
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, packs, userEnv,
				func(_ *Options, cfg *jsonx.OrderedMap) { configSelects(cfg, "fxa", "fxp") })
			if rc := o.deliverChannelOnAttach("yolo-ws-abcd1234", rt, cfg,
				stagedPacks{root: "/ctx/packs", packs: packs}, channel); rc != 0 {
				t.Fatalf("%s: the attach refused: rc=%d\n%s", rt, rc, stderr.String())
			}
			requireShadowLines(t, rt+" attach", stderr.String(), shadowed)
		}
	}
}

// THE macos-user LAUNCH: the arm's noteCredentialScope, beside its session env.
func TestAMacosUserLaunchNamesWhatItShadowed(t *testing.T) {
	hostLauncher(t)
	for _, shadowed := range []bool{true, false} {
		home := packHome(t)
		writeShadowFixture(t, home, shadowed)
		got := macosUserLaunch(t, t.TempDir())
		if got.rc != 0 || got.env == nil {
			t.Fatalf("the macos-user launch never reached its handler: rc=%d\n%s", got.rc, got.out)
		}
		requireShadowLines(t, "macos-user launch", got.out, shadowed)
	}
}
