package run

// acinspect_test.go pins the attach's read of an Apple Container jail's environment. AC's
// `container inspect` takes no --format and answers JSON (internal/cli/ps.go and
// internal/cli/check/probes.go read it that way already), so the podman template the attach used
// for every runtime read nothing there: the contract gate then treated every AC jail as current,
// and an older one silently received a scoped delivery its launchers never source. A remedy an AC
// attach names is `yolo stop`, as on every other runtime (stopRemedy). It named `container stop`
// while `yolo stop` read the same template and said "No jail running" there (G11,
// docs/plans/setup-support-gaps.md), which JL-D79 ended.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// acInspectPayload is AC's inspect answer for a jail frozen with env, in the measured shape
// (setup-support-gaps.md §5.1 row 5; internal/runtime's inspectenv_test.go carries the whole
// document): a top-level array, the environment at configuration.initProcess.environment.
func acInspectPayload(t *testing.T, env string) string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(env, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	doc := []map[string]any{{
		"id":     "yolo-ws-abcd1234",
		"status": map[string]any{"state": "running"},
		"configuration": map[string]any{
			"initProcess": map[string]any{"environment": lines},
		},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// acRuntime answers the way Apple Container does: inspect with a Go template fails, inspect
// without one prints the JSON document.
func acRuntime(t *testing.T, env string) func([]string, string, []string, time.Duration) ExecResult {
	payload := acInspectPayload(t, env)
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[0] == "container" && argv[1] == "inspect" {
			for _, a := range argv {
				if a == "--format" {
					return ExecResult{Ran: true, RC: 1, Stderr: "Error: Unknown option '--format'"}
				}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: payload}
		}
		return ExecResult{Ran: false}
	}
}

// TestInspectContainerEnvReadsAppleContainersJSON: the environment an AC jail was launched with
// comes back as the same env lines the podman template yields.
func TestInspectContainerEnvReadsAppleContainersJSON(t *testing.T) {
	o := &Options{Exec: acRuntime(t, "YOLO_VERSION=0.10.0\nYOLO_HOST_DIR=/ws\n")}
	got := o.inspectContainerEnv("container", "yolo-ws-abcd1234")
	if envLineValue(got, "YOLO_VERSION") != "0.10.0" || envLineValue(got, "YOLO_HOST_DIR") != "/ws" {
		t.Errorf("inspectContainerEnv on Apple Container = %q, want the jail's environment", got)
	}
}

// acAttach is one attach into an Apple Container jail frozen with env, with a typed zai
// selection, which scopes values to claude and so needs agent-env-files. A fake `container` on
// PATH stands in for the exec.
func acAttach(t *testing.T, env string) (rc int, execed bool, stderr string, envFile string, before []byte) {
	t.Helper()
	packs := zaiSelected(t)
	o, cfg, channel, errBuf := attachFixture(t, env, packs, hydratedKey(),
		func(o *Options, _ *jsonx.OrderedMap) { o.ProfileName = "zai" })
	o.Exec = acRuntime(t, env)
	o.Stdout = &bytes.Buffer{}
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	envFile, before = seedLiveChannelFile(t, o)
	bin, marker := t.TempDir(), filepath.Join(t.TempDir(), "execed")
	if err := os.WriteFile(filepath.Join(bin, "container"), []byte("#!/bin/sh\n: > '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	rc, _ = o.attachExisting("yolo-ws-abcd1234", "container", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil)
	_, err := os.Stat(marker)
	return rc, err == nil, errBuf.String(), envFile, before
}

// TestTheContractGateReadsAnAppleContainerJail: an AC jail launched before the per-agent env
// files is refused a scoped delivery, and the refusal names `yolo stop`, which ends an AC jail as
// it ends any other since JL-D79, and not the bare `container stop`, which bypasses the jail's
// keeper.
func TestTheContractGateReadsAnAppleContainerJail(t *testing.T) {
	rc, execed, stderr, envFile, before := acAttach(t, preGateEnv)
	if rc != 1 || execed {
		t.Fatalf("an older Apple Container jail was handed a scoped delivery: rc=%d execed=%v\n%s", rc, execed, stderr)
	}
	for _, want := range []string{"Refusing to attach", "agent-env-files", "'yolo stop' from this workspace"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the refusal must name %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "container stop") {
		t.Errorf("an Apple Container refusal names 'container stop', where 'yolo stop' ends the jail as on every "+
			"other runtime:\n%s", stderr)
	}
	assertLiveChannelFileUnchanged(t, envFile, before)
}

// TestTheContractGatePassesACurrentAppleContainerJail: the same attach into an AC jail this build
// launched, whose environment carries the tags, goes ahead and delivers.
func TestTheContractGatePassesACurrentAppleContainerJail(t *testing.T) {
	rc, execed, stderr, envFile, _ := acAttach(t, "YOLO_VERSION=9.9.9-test\n"+entrypointContractTagsLine()+"\n")
	if rc != 0 || !execed {
		t.Fatalf("a current Apple Container jail was refused: rc=%d execed=%v\n%s", rc, execed, stderr)
	}
	body, err := os.ReadFile(envFile)
	if err != nil || !strings.Contains(string(body), "zai") {
		t.Errorf("the attach did not deliver the selection into the live channel file (%v):\n%s", err, body)
	}
}

// TestAnAppleContainerAttachDoesNotTakeTheSharedTreeForTheBootedOne: a jail Apple Container
// launched before per-launch pack trees copied the shared tree into its home at that launch, and
// every attach since re-staged the shared tree, so it holds what the config said at the last
// entry. Read as the jail's packs it would describe the wrong set with no warning; the attach
// instead says it cannot find the tree the jail booted with.
func TestAnAppleContainerAttachDoesNotTakeTheSharedTreeForTheBootedOne(t *testing.T) {
	packs := zaiSelected(t)
	o, cfg, channel, errBuf := attachFixture(t, "", packs, hydratedKey(), nil)
	current := "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"
	o.Exec = acRuntime(t, current)
	o.Stdout = &bytes.Buffer{}
	legacy := paths.LegacyPackStagingDir("yolo-ws-abcd1234")
	if err := os.MkdirAll(filepath.Join(legacy, officialStagingDir, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, filepath.Join(legacy, officialStagingDir, "claude"), `{"name":"claude"}`)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "container"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if rc, _ := o.attachExisting("yolo-ws-abcd1234", "container", "true", cfg,
		stagedPacks{root: "/ctx/packs", packs: packs}, channel, false, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errBuf)
	}
	out := errBuf.String()
	if !strings.Contains(out, "could not find the pack tree this jail booted with") ||
		!strings.Contains(out, "Apple Container") {
		t.Errorf("the attach took the shared tree for the one an Apple Container jail booted with:\n%s", out)
	}
	if strings.Contains(out, "configured packs differ") {
		t.Errorf("the attach compared against the shared tree:\n%s", out)
	}
}
