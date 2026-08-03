package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// describe prints the resolved confinement + packs + a description hash; --json is the
// canonical config, --hash is the (unsealed-marked) pin.
func TestDescribeVerb(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail","resources":{"pids_limit":4096}}`)

	var out, errw bytes.Buffer
	if rc := describeMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("describe rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "confinement") || !strings.Contains(out.String(), "jail") {
		t.Errorf("describe should name the confinement notch:\n%s", out.String())
	}

	// --json is the canonical computed config.
	out.Reset()
	if rc := describeMain([]string{"--json"}, &out, &errw, false); rc != 0 {
		t.Fatalf("describe --json rc=%d", rc)
	}
	if !strings.Contains(out.String(), `"pids_limit": 4096`) {
		t.Errorf("describe --json must print the effective config:\n%s", out.String())
	}

	// --hash is a sha256, marked unsealed (not yet authoritative).
	out.Reset()
	if rc := describeMain([]string{"--hash"}, &out, &errw, false); rc != 0 {
		t.Fatalf("describe --hash rc=%d", rc)
	}
	if !strings.Contains(out.String(), "sha256:") || !strings.Contains(out.String(), "UNSEALED") {
		t.Errorf("describe --hash must print a sha256 marked unsealed:\n%s", out.String())
	}
}

// apply routes by notch; the not-yet-built notches fail closed (rc!=0) with an honest
// message rather than silently doing nothing, and a bogus --at is a usage error.
func TestApplyVerbRouting(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"confinement":"jail"}`)

	var out, errw bytes.Buffer
	// jail: reports + describes, rc 0.
	if rc := applyMain(nil, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply (jail) rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "jail") {
		t.Errorf("apply (jail) should say so:\n%s", out.String())
	}

	// --host and --sealed are real now (Phases 4/5) with their own tests; their outcome
	// depends on packs/workspace, so this routing test does not assert them.

	// A bogus notch is a usage error (rc 2), not a silent default.
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--at", "bogus"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("apply --at bogus should be a usage error (rc 2), got %d", rc)
	}
}

// apply --sealed refuses when an UNDECLARED input is present (yolo-jail.local.jsonc)
// and passes when the workspace is clean of them. Runs in a scratch workspace so
// workspaceRoot() resolves there (not this repo's /workspace, which has real sidecars).
func TestApplySealedClosure(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	// A .yolo dir so workspaceRoot() anchors on this repo.
	writeFile(t, filepath.Join(repo, ".yolo", "keep"), "x")

	// Clean: no local.jsonc, no capture sidecars → sealed (rc 0).
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("clean workspace should seal (rc 0), got %d: %s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "sealed") {
		t.Errorf("clean seal should say 'sealed':\n%s", out.String())
	}

	// With yolo-jail.local.jsonc present → refused (rc 1), naming it.
	writeFile(t, filepath.Join(repo, "yolo-jail.local.jsonc"), `{"packages":["ripgrep"]}`)
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("local.jsonc present should refuse (rc 1), got %d", rc)
	}
	if !strings.Contains(out.String(), "yolo-jail.local.jsonc") || !strings.Contains(out.String(), "refused") {
		t.Errorf("refusal should name the undeclared input:\n%s", out.String())
	}
}

// apply --host renders config surfaces into a real home as PURE RMW (OQ-4): observe
// writes nothing, assert regenerates only the pack's managed keys and preserves the
// user's own, and non-config kinds are refused by name. Uses a scratch HOME.
func TestApplyHostRMW(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	packDir := t.TempDir()
	writeFile(t, filepath.Join(packDir, "pack.json"), `{"name":"hp","contributes":[
	  {"kind":"config","config":[{"agent":"hp","name":"settings","codec":"json","path":"~/.hp/settings.json","mode":"rmw","managed":{"telemetry":false}}]},
	  {"kind":"mount","host":"refs","into":"refs"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["file://`+packDir+`"],"confinement":"host"}`)

	// A pre-existing user key that RMW must preserve.
	writeFile(t, filepath.Join(home, ".hp", "settings.json"), `{"myOwnKey":"keep","telemetry":true}`)

	// Observe: writes nothing (the file keeps the user's telemetry:true).
	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--host"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --host observe rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "refused") || !strings.Contains(out.String(), "mount") {
		t.Errorf("observe should refuse the mount kind by name:\n%s", out.String())
	}
	data, _ := os.ReadFile(filepath.Join(home, ".hp", "settings.json"))
	if !strings.Contains(string(data), `"telemetry": true`) && !strings.Contains(string(data), `"telemetry":true`) {
		t.Errorf("observe must not write — telemetry should still be the user's true:\n%s", data)
	}

	// Assert: managed key regenerated, user key preserved.
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--host", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("apply --host --assert rc=%d: %s", rc, errw.String())
	}
	data, _ = os.ReadFile(filepath.Join(home, ".hp", "settings.json"))
	if !strings.Contains(string(data), "keep") {
		t.Errorf("RMW must preserve the user's own key:\n%s", data)
	}
	if !strings.Contains(string(data), "false") {
		t.Errorf("RMW must regenerate yolo's managed key to its declared value:\n%s", data)
	}
}
