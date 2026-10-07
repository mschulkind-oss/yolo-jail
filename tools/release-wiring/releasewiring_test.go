package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"
const testTagObject = "abcdef0123456789abcdef0123456789abcdef01"

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
}

type workflowJob struct {
	Permissions map[string]string `yaml:"permissions"`
	Needs       yaml.Node         `yaml:"needs"`
	If          string            `yaml:"if"`
	Environment string            `yaml:"environment"`
	Steps       []workflowStep    `yaml:"steps"`
}

type releaseWorkflow struct {
	Name        string                 `yaml:"name"`
	RunName     string                 `yaml:"run-name"`
	Permissions map[string]string      `yaml:"permissions"`
	On          map[string]yaml.Node   `yaml:"on"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

func readWorkflow(t *testing.T, relative string) releaseWorkflow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), relative))
	if err != nil {
		t.Fatal(err)
	}
	var workflow releaseWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("%s: invalid YAML: %v", relative, err)
	}
	return workflow
}

func needsList(t *testing.T, node yaml.Node) []string {
	t.Helper()
	if node.Kind == 0 {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		var values []string
		if err := node.Decode(&values); err != nil {
			t.Fatal(err)
		}
		return values
	}
	var value string
	if err := node.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return []string{value}
}

func TestReleaseWorkflowsAreMainDispatchedAndKeepWritesOffTargetSource(t *testing.T) {
	requestCaller := readWorkflow(t, ".github/workflows/release-request.yml")
	if !contains(needsList(t, requestCaller.Jobs["prepare-target"].Needs), "eligibility") || !contains(needsList(t, requestCaller.Jobs["create-tag-and-dispatch"].Needs), "prepare-target") || !contains(needsList(t, requestCaller.Jobs["create-tag-and-dispatch"].Needs), "eligibility") {
		t.Fatal("tag mutation is not ordered after eligibility and target build")
	}
	if step := namedStep(t, requestCaller, "prepare-target", "Prepare exact target source without publication credentials"); step.Run != "tools/release-wiring/prepare.sh" {
		t.Fatalf("request target preparation call removed/replaced: %q", step.Run)
	}
	request := readWorkflow(t, ".github/workflows/release-request.yml")
	release := readWorkflow(t, ".github/workflows/release.yml")
	publish := readWorkflow(t, ".github/workflows/publish.yml")
	for rel, wf := range map[string]releaseWorkflow{
		"release-request.yml": request,
		"release.yml":         release,
		"publish.yml":         publish,
	} {
		if len(wf.On) != 1 {
			t.Errorf("%s triggers = %v; only an explicit main-sourced workflow_dispatch is allowed", rel, wf.On)
		}
		if _, ok := wf.On["workflow_dispatch"]; !ok {
			t.Errorf("%s does not use workflow_dispatch", rel)
		}
		for jobName, job := range wf.Jobs {
			for i, step := range job.Steps {
				if strings.TrimSpace(step.Run) != "" {
					cmd := exec.Command("bash", "-n", "-c", step.Run)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Errorf("%s job %s step %d has invalid shell: %v: %s", rel, jobName, i, err, output)
					}
				}
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					ref := step.With["ref"]
					if hasAnyWritePermission(job.Permissions) && (strings.Contains(ref, "inputs.sha") || strings.Contains(ref, "outputs.sha")) {
						t.Errorf("%s write-capable job %s checks out target-controlled ref %q", rel, jobName, ref)
					}
				}
			}
		}
	}

	for rel, wf := range map[string]releaseWorkflow{"release-request.yml": request, "release.yml": release, "publish.yml": publish} {
		if wf.Permissions == nil {
			t.Errorf("%s must explicitly start with no workflow permissions", rel)
		}
	}
	for jobName, job := range request.Jobs {
		if jobName == "create-tag-and-dispatch" {
			if job.Permissions["contents"] != "write" || job.Permissions["actions"] != "write" {
				t.Errorf("release request mutation job permissions = %v", job.Permissions)
			}
			continue
		}
		if hasAnyWritePermission(job.Permissions) {
			t.Errorf("release-request preflight/target job %s unexpectedly has write scope %v", jobName, job.Permissions)
		}
	}
	for jobName, job := range release.Jobs {
		if jobName == "publish-release" {
			if job.Permissions["contents"] != "write" {
				t.Errorf("release publisher permissions = %v", job.Permissions)
			}
			continue
		}
		if hasAnyWritePermission(job.Permissions) {
			t.Errorf("release eligibility/target job %s unexpectedly has write scope %v", jobName, job.Permissions)
		}
	}
	for jobName, job := range publish.Jobs {
		if jobName == "claim-publication" {
			if job.Permissions["contents"] != "write" {
				t.Errorf("claim job permissions = %v", job.Permissions)
			}
			continue
		}
		if jobName == "publish-wheels" {
			if job.Permissions["id-token"] != "write" || len(job.Permissions) != 2 || job.Permissions["contents"] != "read" {
				t.Errorf("PyPI identity job permissions = %v; direct OIDC is restricted to read + id-token", job.Permissions)
			}
			continue
		}
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "go run ./tools/build-wheels") || strings.Contains(step.Run, "nix build") {
				if hasAnyWritePermission(job.Permissions) {
					t.Errorf("publisher build job %s has write or OIDC authority %v", jobName, job.Permissions)
				}
			}
		}
	}

	for _, id := range []string{"build-wheels", "build-image-cache", "build-builder-image"} {
		if !contains(needsList(t, publish.Jobs[id].Needs), "claim-publication") {
			t.Errorf("%s can begin target build before the create-only publication claim", id)
		}
	}
	if !contains(needsList(t, publish.Jobs["publish-wheels"].Needs), "build-wheels") {
		t.Errorf("PyPI upload is not ordered after wheel preparation: %v", publish.Jobs["publish-wheels"].Needs)
	}
	if !strings.Contains(release.RunName, "Release v") || !strings.Contains(release.RunName, "inputs.sha") || !strings.Contains(release.RunName, "inputs.request_run_id") || !strings.Contains(release.RunName, "Homebrew-only v") {
		t.Errorf("release run-name does not transport anchored version/SHA/request data and explicitly label backfills: %q", release.RunName)
	}
	if !strings.Contains(publish.RunName, "inputs.version") || !strings.Contains(publish.RunName, "inputs.sha") || !strings.Contains(publish.RunName, "inputs.release_run_id") {
		t.Errorf("publisher run-name does not bind exact version/SHA/original Release run: %q", publish.RunName)
	}
	for _, step := range publish.Jobs["publish-wheels"].Steps {
		if strings.Contains(step.Run, "uv publish") && !strings.Contains(step.Run, "--trusted-publishing always") {
			t.Error("direct uv publisher lost the established Trusted Publishing call")
		}
	}
	if !strings.Contains(string(mustRead(t, ".github/workflows/publish.yml")), "environment: pypi") {
		t.Error("PyPI environment identity was removed")
	}
	if !strings.Contains(string(mustRead(t, ".github/workflows/release.yml")), "--skip=publish") {
		t.Error("GoReleaser preparation must be the documented read-only --skip=publish mode")
	}
}

func hasAnyWritePermission(permissions map[string]string) bool {
	for _, value := range permissions {
		if value == "write" {
			return true
		}
	}
	return false
}

func mustRead(t *testing.T, relative string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), relative))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fakeCommands(t *testing.T) (string, string) {
	t.Helper()
	bin := t.TempDir()
	trace := filepath.Join(t.TempDir(), "trace.log")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("git", `echo "git $*" >> "$TRACE"
if [ "$1" = "-C" ] && [ "$3" = "rev-parse" ] && [ "$4" = "HEAD" ]; then echo "${TARGET_SHA:-$TEST_SHA}"; exit 0; fi
case "$*" in
  "rev-parse HEAD") echo "$TEST_SHA" ;;
  "rev-parse $TEST_SHA^{commit}") echo "$TEST_SHA" ;;
  "fetch --quiet origin main") exit 0 ;;
  "merge-base --is-ancestor $TEST_SHA FETCH_HEAD") exit 0 ;;
  "ls-remote origin refs/tags/v9.8.7 refs/tags/v9.8.7^{}") if [ "${FAIL_TAG_LOOKUP:-}" = 1 ]; then exit 1; fi ;;
  "-C */target rev-parse HEAD") echo "${TARGET_SHA:-$TEST_SHA}" ;;
esac
`)
	write("go", `echo "go $* token=${GITHUB_TOKEN:-} gh=${GH_TOKEN:-} tap=${HOMEBREW_TAP_TOKEN:-} cachix=${CACHIX_AUTH_TOKEN:-}" >> "$TRACE"
case "$*" in
  *tools/release-gate*)
    if [ "${FAIL_GATE:-}" = 1 ]; then echo "fake CI refusal" >&2; exit 1; fi
    echo '{"sha":"'"$TEST_SHA"'"}'
    ;;
  *tools/release-wiring\ check-version-order*) exit 0 ;;
  *tools/pack-binaries\ check*) if [ "${FAIL_PINS:-}" = 1 ]; then exit 1; fi ;;
  *tools/build-wheels*)
    if [ "${FAIL_PREPARE:-}" = 1 ]; then exit 1; fi
    while [ "$#" -gt 0 ]; do
      if [ "$1" = --output-dir ]; then mkdir -p "$2"; : > "$2/fake.whl"; shift 2; else shift; fi
    done
    ;;
esac
`)
	write("uv", `echo "registry: uv $*" >> "$TRACE"
exit 0
`)
	write("sh", `echo "sh $*" >> "$TRACE"
exit 0
`)
	write("sleep", `exit 0
`)
	write("gh", `echo "gh $*" >> "$TRACE"
case "$*" in
  *git/tags*) echo "$TEST_TAG_OBJECT" ;;
  *git/refs*) [ "${FAIL_REF:-}" != 1 ] || exit 1 ;;
  *"workflow run release.yml"*) [ "${FAIL_RELEASE_DISPATCH:-}" != 1 ] || exit 1 ;;
  *actions/workflows/release.yml*) echo 55 ;;
  *"/runs?per_page=100"*)
    status=${FAKE_RELEASE_STATUS:-completed}
    conclusion=${FAKE_RELEASE_CONCLUSION:-success}
    printf '202\t%s\t%s\t1\n' "$status" "$conclusion"
    if [ "${FAKE_DUPLICATE_RELEASE_RUNS:-}" = 1 ]; then printf '203\t%s\t%s\t1\n' "$status" "$conclusion"; fi
    ;;
  *"workflow run publish.yml"*)
    if [ "${FAIL_PUBLISH_DISPATCH:-}" = 1 ]; then exit 1; fi
    echo "fake-publish-dispatch-accepted" >> "$TRACE"
    ;;
  *) exit 0 ;;
esac
`)
	return bin, trace
}

func commandEnv(bin, trace string, extra ...string) []string {
	overridden := map[string]bool{"PATH": true, "TRACE": true, "TEST_SHA": true, "TEST_TAG_OBJECT": true,
		"GITHUB_REPOSITORY": true, "GITHUB_REF_TYPE": true, "GITHUB_REF_NAME": true, "GITHUB_EVENT_NAME": true,
		"GITHUB_RUN_ATTEMPT": true}
	for _, item := range extra {
		if key, _, ok := strings.Cut(item, "="); ok {
			overridden[key] = true
		}
	}
	env := make([]string, 0, len(os.Environ())+16)
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if !overridden[key] {
			env = append(env, item)
		}
	}
	env = append(env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TRACE="+trace,
		"TEST_SHA="+testSHA,
		"TEST_TAG_OBJECT="+testTagObject,
		"GITHUB_REPOSITORY=owner/repo",
		"GITHUB_REF_TYPE=branch",
		"GITHUB_REF_NAME=main",
		"GITHUB_EVENT_NAME=workflow_dispatch",
		"GITHUB_RUN_ATTEMPT=1",
	)
	return append(env, extra...)
}

func runScript(t *testing.T, root, script string, bin, trace string, extra ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = commandEnv(bin, trace, extra...)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	return output.String(), err
}

func readTrace(t *testing.T, trace string) []string {
	t.Helper()
	b, err := os.ReadFile(trace)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func traceIndex(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func traceLastIndex(lines []string, needle string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], needle) {
			return i
		}
	}
	return -1
}

func TestRequestMainCallerWaitsForExactOriginalReleaseSuccessBeforePublishDispatch(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "tools", "release-wiring", "request.sh")
	for _, tc := range []struct {
		name        string
		extra       []string
		wantError   string
		wantPublish bool
	}{
		{name: "successful exact original Release run", wantPublish: true},
		{name: "CI failure before any tag write", extra: []string{"FAIL_GATE=1"}, wantError: "No tag or publisher write was made"},
		{name: "failed original Release run", extra: []string{"FAKE_RELEASE_CONCLUSION=failure"}, wantError: "exact original GoReleaser/Release run concluded"},
		{name: "still-running original Release run is not success", extra: []string{"FAKE_RELEASE_STATUS=in_progress", "RELEASE_WAIT_SECONDS=1"}, wantError: "did not complete within"},
		{name: "duplicate matching original runs are ambiguous", extra: []string{"FAKE_DUPLICATE_RELEASE_RUNS=1"}, wantError: "Multiple Release runs match"},
		{name: "ref conflict does not dispatch", extra: []string{"FAIL_REF=1"}, wantError: "Could not create v9.8.7"},
		{name: "release dispatch refusal does not create publisher run", extra: []string{"FAIL_RELEASE_DISPATCH=1"}, wantError: "Release dispatch failed"},
		{name: "publisher dispatch refusal is not retried", extra: []string{"FAIL_PUBLISH_DISPATCH=1"}, wantError: "Publish dispatch failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, trace := fakeCommands(t)
			out, err := runScript(t, root, script, bin, trace,
				append([]string{"RELEASE_VERSION=9.8.7", "RELEASE_SHA=" + testSHA, "GITHUB_RUN_ID=101", "RELEASE_WAIT_SECONDS=1"}, tc.extra...)...)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(out, tc.wantError) {
					t.Fatalf("request did not refuse safely: err=%v output=%s", err, out)
				}
			} else if err != nil {
				t.Fatalf("request failed: %v output=%s", err, out)
			}
			lines := readTrace(t, trace)
			gate, tag, releaseRun, publishRun := traceIndex(lines, "tools/release-gate"), traceIndex(lines, "git/tags"), traceIndex(lines, "workflow run release.yml"), traceIndex(lines, "workflow run publish.yml")
			if tc.wantError == "No tag or publisher write was made" {
				if tag >= 0 || traceIndex(lines, "gh ") >= 0 {
					t.Fatalf("failed CI crossed into a tag/dispatch API call: %v", lines)
				}
				return
			}
			if gate < 0 {
				t.Fatalf("actual request script did not call exact CI gate: %v", lines)
			}
			if tc.wantPublish {
				if !(tag >= 0 && releaseRun > tag && publishRun > releaseRun) || traceIndex(lines, "fake-publish-dispatch-accepted") < 0 {
					t.Fatalf("request did not sequence tag -> Release dispatch/wait -> accepted publisher: %v", lines)
				}
				if !strings.Contains(lines[tag], "object="+testSHA) || !strings.Contains(lines[releaseRun], "sha="+testSHA) || !strings.Contains(lines[publishRun], "sha="+testSHA) {
					t.Fatalf("frozen target SHA was not bound through writes: %v", lines)
				}
				return
			}
			if publishRun >= 0 && tc.wantError != "Publish dispatch failed" {
				t.Fatalf("request dispatched publisher despite refusal: %v", lines)
			}
			if tc.wantError == "Publish dispatch failed" && (publishRun < 0 || traceIndex(lines, "fake-publish-dispatch-accepted") >= 0) {
				t.Fatalf("publisher dispatch refusal was not retained as a failed attempt: %v", lines)
			}
			if tc.wantError != "" && tc.wantError != "No tag or publisher write was made" && tag >= 0 && !(strings.Contains(strings.ToLower(out), "read-only") && strings.Contains(strings.ToLower(out), "inspect")) {
				t.Fatalf("partial tagged state did not name safe read-only inspection guidance: %s", out)
			}
		})
	}
}

func TestRequestCallerRejectsWrongRefAndRerunBeforeAnyWrite(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "tools", "release-wiring", "request.sh")
	for _, extra := range [][]string{{"GITHUB_REF_TYPE=tag"}, {"GITHUB_RUN_ATTEMPT=2"}} {
		bin, trace := fakeCommands(t)
		out, err := runScript(t, root, script, bin, trace, append([]string{"RELEASE_VERSION=9.8.7", "RELEASE_SHA=" + testSHA, "GITHUB_RUN_ID=101"}, extra...)...)
		if err == nil {
			t.Fatalf("unsafe request context unexpectedly passed: %s", out)
		}
		if writes := readTrace(t, trace); traceIndex(writes, "gh ") >= 0 {
			t.Fatalf("wrong-ref/rerun refusal made a GitHub API call: %v", writes)
		}
	}
}

func TestPrepareTargetRunsWithoutWriteCredentialsAndChecksExactHead(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	for _, dir := range []string{"tools/release-wiring", "target/scripts"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), "tools/release-wiring/prepare.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "tools/release-wiring/prepare.sh")
	if err := os.WriteFile(script, data, 0o755); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(root, "trace")
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"git": `echo "git $*" >> "$TRACE"; if [ "$1" = "-C" ] && [ "$3" = "rev-parse" ] && [ "$4" = "HEAD" ]; then echo "${TARGET_SHA:-$TEST_SHA}"; fi`,
		"go":  `echo "go $* token=${GITHUB_TOKEN:-} gh=${GH_TOKEN:-} tap=${HOMEBREW_TAP_TOKEN:-} cachix=${CACHIX_AUTH_TOKEN:-}" >> "$TRACE"; case "$*" in *pack-binaries*) exit 0;; *build-wheels*) [ "${FAIL_PREPARE:-}" != 1 ] || exit 1; while [ "$#" -gt 0 ]; do if [ "$1" = --output-dir ]; then mkdir -p "$2"; : > "$2/wheel.whl"; shift 2; else shift; fi; done;; esac`,
		"sh":  `echo "sh $* token=${GITHUB_TOKEN:-} gh=${GH_TOKEN:-} tap=${HOMEBREW_TAP_TOKEN:-} cachix=${CACHIX_AUTH_TOKEN:-}" >> "$TRACE"; exit 0`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(target, "scripts/stage-source-bundle.sh")
	// Execute hostile target shell source, not a stub for the shell interpreter.
	if err := os.Remove(filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "scripts/changelog-section.sh"), []byte("#!/bin/sh\nprintf 'TARGET token=%s gh=%s tap=%s cachix=%s\\n' \"${GITHUB_TOKEN:-}\" \"${GH_TOKEN:-}\" \"${HOMEBREW_TAP_TOKEN:-}\" \"${CACHIX_AUTH_TOKEN:-}\" >> \"$TRACE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("#!/bin/sh\nmkdir -p \"$1\"; : > \"$1/flake.nix\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = commandEnv(bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+testSHA, "TARGET_ROOT="+target, "RUNNER_TEMP="+root,
		"GITHUB_TOKEN=write-secret", "GH_TOKEN=write-secret", "HOMEBREW_TAP_TOKEN=write-secret", "CACHIX_AUTH_TOKEN=write-secret")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("read-only preparation failed: %v output=%s trace=%s", err, output.String(), readFile(t, trace))
	}
	traceText := readFile(t, trace)
	if strings.Contains(traceText, "write-secret") || strings.Contains(traceText, "token= gh= tap= cachix=") == false {
		t.Fatalf("target preparation received a publication credential or trace omitted empty credentials: %s", traceText)
	}

	bad := exec.Command("bash", script)
	bad.Dir = root
	bad.Env = commandEnv(bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+testSHA, "TARGET_ROOT="+target,
		"TARGET_SHA="+strings.Repeat("f", 40), "RUNNER_TEMP="+root)
	if err := bad.Run(); err == nil {
		t.Fatal("target preparation accepted a checkout whose HEAD differed from the frozen SHA")
	}
	if lines := readTrace(t, trace); traceIndex(lines, "gh ") >= 0 {
		t.Fatalf("target source called publication API: %v", lines)
	}
	// A target-controlled build refusal occurs before the write job becomes eligible.
	failed := exec.Command("bash", script)
	failed.Dir = root
	failed.Env = commandEnv(bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+testSHA, "TARGET_ROOT="+target, "RUNNER_TEMP="+root, "FAIL_PREPARE=1")
	if err := failed.Run(); err == nil {
		t.Fatal("target build refusal was accepted")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHostJustReleaseSubmitsOnlyFrozenSHAAfterLocalPinCheck(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just is not installed")
	}
	bin, trace := fakeCommands(t)
	cmd := exec.Command("just", "--justfile", "Justfile", "release", "9.8.7")
	cmd.Dir = repositoryRoot(t)
	cmd.Env = commandEnv(bin, trace)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("just release failed: %v output=%s trace=%v", err, output.String(), readTrace(t, trace))
	}
	lines := readTrace(t, trace)
	check, request := traceIndex(lines, "tools/pack-binaries check"), traceIndex(lines, "workflow run release-request.yml")
	if check < 0 || request <= check || !strings.Contains(lines[request], "sha="+testSHA) || !strings.Contains(lines[request], "--ref main") {
		t.Fatalf("host recipe did not pin-check then dispatch frozen SHA on main: %v", lines)
	}
	if traceIndex(lines, "git tag") >= 0 || traceIndex(lines, "git push") >= 0 {
		t.Fatalf("host recipe wrote a tag instead of submitting a request: %v", lines)
	}
}

func TestHomebrewBackfillIsExplicitAndRequiresThePublishedImmutableTag(t *testing.T) {
	workflow := readWorkflow(t, ".github/workflows/release.yml")
	if !strings.Contains(workflow.RunName, "Homebrew-only v") || !strings.Contains(workflow.RunName, "inputs.mode") {
		t.Fatalf("legacy mode is not explicitly named in workflow_run display_title: %q", workflow.RunName)
	}
	body := string(mustRead(t, ".github/workflows/release.yml"))
	if !strings.Contains(body, "default: homebrew-only") || !strings.Contains(body, "tools/release-wiring/resolve-tag.sh") || !strings.Contains(body, "ensure-release-present.sh") {
		t.Fatal("Homebrew-only backfill lost its narrow immutable-tag/published-release guard")
	}
	if !strings.Contains(string(mustRead(t, "tools/tap-install-check/judge.go")), "Homebrew-only v") ||
		!strings.Contains(string(mustRead(t, "tools/tap-install-check/judge.go")), "mainReleaseTitle") {
		t.Fatal("the production tap caller no longer distinguishes normal main releases from explicit backfills")
	}
}

func TestHomebrewReleasePresenceGateRefusesDraftOrUnknown(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "tools", "release-wiring", "ensure-release-present.sh")
	for _, tc := range []struct {
		name, output string
		wantOK       bool
	}{
		{name: "published", output: "false\n", wantOK: true},
		{name: "draft", output: "true\n"},
		{name: "unknown API result", output: "", wantOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			body := "printf '%s' '" + tc.output + "'\n"
			if tc.name == "unknown API result" {
				body += "exit 1\n"
			}
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "GITHUB_REPOSITORY=owner/repo", "RELEASE_TAG=v9.8.7")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			err := cmd.Run()
			if (err == nil) != tc.wantOK {
				t.Fatalf("unexpected result: err=%v output=%s", err, output.String())
			}
		})
	}
}

func TestClaimEnvironmentChecksTrustedContextAndCreateOnlyCaller(t *testing.T) {
	var assets []byte
	var posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/git/ref/tags/v9.8.7":
			writeJSON(w, map[string]any{"object": map[string]any{"type": "commit", "sha": testSHA}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/releases/tags/v9.8.7":
			writeJSON(w, map[string]any{"id": 77, "tag_name": "v9.8.7", "draft": false, "upload_url": serverURL(r) + "/repos/owner/repo/releases/77/assets{?name,label}"})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/releases/77/assets":
			posts++
			if r.URL.Query().Get("name") != publicationClaimAsset {
				http.Error(w, "unexpected asset", http.StatusBadRequest)
				return
			}
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read failed", http.StatusBadRequest)
				return
			}
			assets = data
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for name, value := range map[string]string{
		"GITHUB_REPOSITORY": "owner/repo", "RELEASE_VERSION": "9.8.7", "RELEASE_SHA": testSHA,
		"REQUEST_RUN_ID": "101", "RELEASE_RUN_ID": "202", "GITHUB_RUN_ID": "303", "GITHUB_RUN_ATTEMPT": "1",
		"GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "main", "GITHUB_EVENT_NAME": "workflow_dispatch",
		"GH_TOKEN": "offline-test-token", "GITHUB_API_URL": server.URL, "GITHUB_UPLOADS_URL": server.URL,
	} {
		t.Setenv(name, value)
	}
	var output strings.Builder
	if err := claimFromEnvWithHTTP(context.Background(), &output, server.Client()); err != nil {
		t.Fatalf("actual claim caller refused: %v", err)
	}
	if posts != 1 {
		t.Fatalf("asset create calls = %d, want exactly one", posts)
	}
	var claim publicationClaim
	if err := json.Unmarshal(assets, &claim); err != nil {
		t.Fatal(err)
	}
	if claim != validTestClaim() {
		t.Fatalf("actual claim caller wrote %+v, want %+v", claim, validTestClaim())
	}

	t.Setenv("GITHUB_EVENT_NAME", "push")
	if err := claimFromEnv(context.Background(), &output); err == nil || !strings.Contains(err.Error(), "trusted-main workflow_dispatch") {
		t.Fatalf("wrong event claim = %v", err)
	}
	if posts != 1 {
		t.Fatalf("wrong-event refusal still called asset API: %d", posts)
	}
}

func TestClaimPublicationDependencyAndPyPIOIDCSourceCaller(t *testing.T) {
	publish := readWorkflow(t, ".github/workflows/publish.yml")
	for _, id := range []string{"build-wheels", "build-image-cache", "build-builder-image"} {
		if !contains(needsList(t, publish.Jobs[id].Needs), "claim-publication") {
			t.Errorf("%s is not gated by publication claim", id)
		}
	}
	if !strings.Contains(string(mustRead(t, ".github/workflows/publish.yml")), "uv publish dist/*.whl --check-url https://pypi.org/simple/ --trusted-publishing always") {
		t.Fatal("direct PyPI publication invocation or OIDC mode changed")
	}
	if publish.Jobs["publish-wheels"].Permissions["id-token"] != "write" {
		t.Fatal("PyPI job no longer retains id-token:write")
	}
}

func TestShellHelpersFailClosedAndAllWorkflowCommandsParse(t *testing.T) {
	for _, rel := range []string{".github/workflows/release-request.yml", ".github/workflows/release.yml", ".github/workflows/publish.yml"} {
		wf := readWorkflow(t, rel)
		for jobName, job := range wf.Jobs {
			for index, step := range job.Steps {
				if strings.TrimSpace(step.Run) == "" {
					continue
				}
				cmd := exec.Command("bash", "-n", "-c", step.Run)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("%s job %s step %d: %v: %s", rel, jobName, index, err, out)
				}
			}
		}
	}
	for _, helper := range []string{"preflight.sh", "prepare.sh", "request.sh", "publish-release.sh", "update-homebrew.sh", "resolve-tag.sh", "validate-target.sh", "ensure-release-absent.sh", "ensure-release-present.sh"} {
		cmd := exec.Command("bash", "-n", filepath.Join(repositoryRoot(t), "tools/release-wiring", helper))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v: %s", helper, err, out)
		}
	}
}

func TestGoReleaserPackagingScriptAcceptsOnlyItsDeclaredInertOutputSet(t *testing.T) {
	workflow := readWorkflow(t, ".github/workflows/release.yml")
	var command string
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Name == "Package only the supported inert GoReleaser output contract" {
				command = step.Run
			}
		}
	}
	if command == "" {
		t.Fatal("release workflow has no actual GoReleaser packaging caller")
	}
	root := makeGoReleaserPackagingFixture(t, "1.2.3")
	cmd := exec.Command("bash", "-e", "-c", command)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "RELEASE_VERSION=1.2.3")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("actual workflow packaging script rejected its declared output set: %v\n%s", err, output.String())
	}
	entries, err := os.ReadDir(filepath.Join(root, "release-assets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 {
		t.Fatalf("packaged release asset count = %d, want 4 archives + pack binary + checksums", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, "release-assets", "checksums.txt")); err != nil {
		t.Fatalf("checksum asset was omitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "release-assets", "fixture_1.2.3_linux_amd64")); err != nil {
		t.Fatalf("pack-binary asset was omitted: %v", err)
	}

	bad := makeGoReleaserPackagingFixture(t, "1.2.3")
	if err := os.WriteFile(filepath.Join(bad, "target/dist/unapproved.whl"), []byte("not a GoReleaser asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	refuse := exec.Command("bash", "-e", "-c", command)
	refuse.Dir = bad
	refuse.Env = append(os.Environ(), "RELEASE_VERSION=1.2.3")
	output.Reset()
	refuse.Stdout, refuse.Stderr = &output, &output
	if err := refuse.Run(); err == nil || !strings.Contains(output.String(), "unexpected GoReleaser dist paths") {
		t.Fatalf("actual workflow packaging script accepted an unknown dist path: err=%v output=%s", err, output.String())
	}
}

func makeGoReleaserPackagingFixture(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	dist := filepath.Join(root, "target/dist")
	packDir := filepath.Join(root, "target/bundle/pack-binaries")
	for _, directory := range []string{dist, packDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	platforms := []struct{ os, arch, slug string }{
		{"darwin", "amd64", "darwin_amd64_v1"}, {"darwin", "arm64", "darwin_arm64_v8.0"},
		{"linux", "amd64", "linux_amd64_v1"}, {"linux", "arm64", "linux_arm64_v8.0"},
	}
	var records []map[string]string
	var checksumLines []string
	addChecksum := func(name string, data []byte) {
		digest := sha256.Sum256(data)
		checksumLines = append(checksumLines, fmt.Sprintf("%s  %s", hex.EncodeToString(digest[:]), name))
	}
	for _, platform := range platforms {
		archive := fmt.Sprintf("yolo-jail_%s_%s_%s.tar.gz", version, platform.os, platform.arch)
		archiveData := []byte("prepared archive: " + archive)
		if err := os.WriteFile(filepath.Join(dist, archive), archiveData, 0o600); err != nil {
			t.Fatal(err)
		}
		addChecksum(archive, archiveData)
		records = append(records, map[string]string{"type": "Archive", "name": archive, "path": "dist/" + archive})
		binaryDir := filepath.Join(dist, "yolo_"+platform.slug)
		if err := os.Mkdir(binaryDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binaryDir, "yolo"), []byte("built binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		records = append(records, map[string]string{"type": "Binary", "name": "yolo", "path": "dist/yolo_" + platform.slug + "/yolo"})
	}
	for name, content := range map[string]string{"metadata.json": "{}\n", "config.yaml": "version: 2\n"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	records = append(records, map[string]string{"type": "Metadata", "name": "metadata.json", "path": "dist/metadata.json"},
		map[string]string{"type": "Checksum", "name": "checksums.txt", "path": "dist/checksums.txt"})
	packName := fmt.Sprintf("fixture_%s_linux_amd64", version)
	packBytes := []byte("official pinned pack program")
	if err := os.WriteFile(filepath.Join(packDir, packName), packBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	addChecksum(packName, packBytes)
	for name, content := range map[string]string{"artifacts.json": mustJSON(t, records), "checksums.txt": strings.Join(checksumLines, "\n") + "\n"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRequestHostRecipeDoesNotTagLocally(t *testing.T) {
	data := string(mustRead(t, "Justfile"))
	start := strings.Index(data, "release version:")
	if start < 0 {
		t.Fatal("Justfile has no release version recipe")
	}
	body := data[start:]
	if !strings.Contains(body, "gh workflow run release-request.yml --ref main") || !strings.Contains(body, "sha=$sha") {
		t.Fatal("host request no longer sends exact SHA to trusted main")
	}
	if strings.Contains(body, "git tag") || strings.Contains(body, "git push") {
		t.Fatal("host recipe must not create/push the immutable release tag")
	}
}

func namedStep(t *testing.T, workflow releaseWorkflow, jobID, name string) workflowStep {
	t.Helper()
	for _, step := range workflow.Jobs[jobID].Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("workflow job %s has no production caller %q", jobID, name)
	return workflowStep{}
}

func runWorkflowCommand(t *testing.T, step workflowStep, root, bin, trace string, env ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
	cmd.Dir = root
	cmd.Env = commandEnv(bin, trace, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestWorkflowHelperCallsAreExecutableAndTargetCheckoutCannotPersistCredentials(t *testing.T) {
	for _, rel := range []string{"release-request.yml", "release.yml", "publish.yml"} {
		wf := readWorkflow(t, ".github/workflows/"+rel)
		for jobID, job := range wf.Jobs {
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					if step.With["persist-credentials"] != "false" {
						t.Errorf("%s/%s persists checkout credentials", rel, jobID)
					}
					if hasAnyWritePermission(job.Permissions) && step.With["ref"] != "${{ github.sha }}" {
						t.Errorf("write job %s/%s does not use trusted workflow SHA", rel, jobID)
					}
				}
				for _, word := range strings.Fields(step.Run) {
					if !strings.HasPrefix(word, "tools/release-wiring/") || !strings.HasSuffix(word, ".sh") {
						continue
					}
					info, err := os.Stat(filepath.Join(repositoryRoot(t), word))
					if err != nil || info.Mode().Perm()&0o111 == 0 {
						t.Errorf("actual %s/%s caller invokes non-executable %s: %v", rel, jobID, word, err)
					}
				}
			}
		}
	}
}

// This executes the production eligibility and preparation entrypoints against
// real Git objects. The selected source has a hostile request script; it must
// not run in eligibility or be substituted for the trusted mutation caller.
func TestTrustedEligibilityRejectsUnmergedSourceBeforeHostileTargetCanRun(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command(realGit, args...)
		c.Dir = root
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %v: %s", args, e, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "fixture")
	if err := os.WriteFile(filepath.Join(root, "safe"), []byte("trusted"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "trusted main")
	trustedSHA := git("rev-parse", "HEAD")
	origin := filepath.Join(t.TempDir(), "origin.git")
	git("clone", "--bare", root, origin)
	git("remote", "add", "origin", origin)
	if err := os.MkdirAll(filepath.Join(root, "tools/release-wiring"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostile := `#!/bin/sh
printf 'TARGET EXECUTED token=%s\n' "$GH_TOKEN" >> "$TRACE"
exit 42
`
	if err := os.WriteFile(filepath.Join(root, "tools/release-wiring/request.sh"), []byte(hostile), 0o755); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "unmerged hostile target")
	hostileSHA := git("rev-parse", "HEAD")
	git("checkout", "--detach", trustedSHA)
	if err := os.MkdirAll(filepath.Join(root, "tools/release-wiring"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"preflight.sh", "request.sh", "prepare.sh"} {
		if err := os.WriteFile(filepath.Join(root, "tools/release-wiring", name), mustRead(t, "tools/release-wiring/"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin, trace := fakeCommands(t)
	// Use real Git so ancestry/ref checks cannot be implicitly stubbed successful.
	if err := os.Remove(filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	step := namedStep(t, readWorkflow(t, ".github/workflows/release-request.yml"), "eligibility", "Validate trusted source, immutable input, main ancestry and exact regular CI")
	for _, tc := range []struct {
		name, sha, ref string
		gate           bool
	}{
		{"unmerged", hostileSHA, "main", false}, {"malformed", "$(touch injected)", "main", false}, {"wrong ref", trustedSHA, "rogue", false}, {"eligible exact SHA", trustedSHA, "main", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Remove(trace)
			out, e := runWorkflowCommand(t, step, root, bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+tc.sha, "GITHUB_REF_NAME="+tc.ref, "RELEASE_PRETAG=1", "WORKFLOW_SHA="+trustedSHA, "GH_TOKEN=readonly-fixture")
			if (e == nil) != tc.gate {
				t.Fatalf("eligibility error=%v output=%s", e, out)
			}
			lines := readTrace(t, trace)
			if traceIndex(lines, "TARGET EXECUTED") >= 0 || traceIndex(lines, "gh ") >= 0 {
				t.Fatalf("eligibility executed source or write: %v", lines)
			}
			if (traceIndex(lines, "tools/release-gate") >= 0) != tc.gate {
				t.Fatalf("exact CI caller ordering broken: %v", lines)
			}
		})
	}
}

func TestMainBackfillCallerUsesTrustedToolsWhenOldTagHasNone(t *testing.T) {
	wf := readWorkflow(t, ".github/workflows/release.yml")
	step := namedStep(t, wf, "eligibility", "Resolve only the immutable tag and prove trusted exact-CI eligibility")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tools/release-wiring"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resolve-tag.sh", "preflight.sh", "ensure-release-present.sh", "validate-target.sh"} {
		if err := os.WriteFile(filepath.Join(root, "tools/release-wiring", name), mustRead(t, "tools/release-wiring/"+name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The published historical target contains neither orchestration nor API tools.
	old := filepath.Join(root, "old-published-tag")
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"scripts", "tools/pack-binaries"} {
		if err := os.MkdirAll(filepath.Join(old, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(old, "scripts/changelog-section.sh"), []byte("#!/bin/sh\nprintf 'Historical notes\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "tools/pack-binaries/main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, old, "git", "init", "-q")
	mustRun(t, old, "git", "config", "user.name", "fixture")
	mustRun(t, old, "git", "config", "user.email", "fixture@example.invalid")
	mustRun(t, old, "git", "add", ".")
	mustRun(t, old, "git", "commit", "-qm", "historical published source")
	mustRun(t, old, "git", "tag", "v9.8.7")
	oldSHA := strings.TrimSpace(string(mustRun(t, old, "git", "rev-parse", "HEAD")))
	if _, err := os.Stat(filepath.Join(old, "tools/release-wiring")); !os.IsNotExist(err) {
		t.Fatal("historical tag unexpectedly contains new tools")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, trace := fakeCommands(t)
	if err := os.Remove(filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"git $*\" >> \"$TRACE\"\ncase \"$*\" in\n -C*) exec \"$REAL_GIT\" \"$@\";;\n *ls-remote*) printf '%s\\trefs/tags/v9.8.7\\n' \"$TEST_SHA\";;\n *rev-parse*) echo \"$TEST_SHA\";;\n *) exit 0;;\nesac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\necho \"gh $*\" >> \"$TRACE\"\n[ \"${DRAFT:-false}\" != unknown ] || exit 1\necho \"${DRAFT:-false}\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, draft := range []string{"false", "true", "unknown"} {
		_ = os.Remove(trace)
		output := filepath.Join(t.TempDir(), "outputs")
		out, e := runWorkflowCommand(t, step, root, bin, trace, "INPUT_MODE=homebrew-only", "INPUT_VERSION=9.8.7", "INPUT_SHA=", "WORKFLOW_SHA="+oldSHA, "GITHUB_OUTPUT="+output, "DRAFT="+draft, "GH_TOKEN=readonly-fixture", "TEST_SHA="+oldSHA, "REAL_GIT="+realGit)
		if (e == nil) != (draft == "false") {
			t.Fatalf("backfill draft=%s error=%v output=%s", draft, e, out)
		}
		lines := readTrace(t, trace)
		if traceIndex(lines, "tools/release-gate") < 0 || traceIndex(lines, "releases/tags/v9.8.7") < 0 {
			t.Fatalf("actual backfill skipped trusted gate: %v", lines)
		}
		if traceIndex(lines, "verify-release-request") >= 0 || traceIndex(lines, "workflow run") >= 0 {
			t.Fatalf("backfill acquired normal publication route: %v", lines)
		}
		if e == nil && !strings.Contains(readFile(t, output), "sha="+oldSHA) {
			t.Fatal("backfill did not transport resolved immutable SHA")
		}

		if e == nil {
			prep := namedStep(t, wf, "prepare-target", "Verify target notes and official binary pins without write credentials")
			out, e = runWorkflowCommand(t, prep, root, bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+oldSHA, "TARGET_ROOT="+old, "REAL_GIT="+realGit)
			if e != nil {
				t.Fatalf("old immutable source required new tools: %v %s", e, out)
			}
			if traceIndex(readTrace(t, trace), "tools/pack-binaries check 9.8.7") < 0 {
				t.Fatal("old source preparation bypassed pin gate")
			}
		}
	}
	if wf.Jobs["homebrew-only"].If != "inputs.mode == 'homebrew-only'" || wf.Jobs["goreleaser-prepare"].If != "inputs.mode == 'publish'" {
		t.Fatal("backfill is not separated from GoReleaser")
	}
}

func TestConcurrentActualRequestCallersHaveOneCreateOnlyTagWinner(t *testing.T) {
	root := repositoryRoot(t)
	bin, trace := fakeCommands(t)
	state := t.TempDir()
	// mkdir is the atomic fake ref endpoint; the loser sees the same immutable ref
	// conflict GitHub returns. Both callers pass eligibility before that race.
	body := `#!/bin/sh
echo "gh $*" >> "$TRACE"
case "$*" in
 *git/tags*) echo "$TEST_TAG_OBJECT";;
 *git/refs*) mkdir "$STATE/ref" 2>/dev/null || exit 1;;
 *"workflow run release.yml"*) :;;
 *actions/workflows/release.yml*) echo 55;;
 *"/runs?per_page=100"*) printf '202\tcompleted\tsuccess\t1\n';;
 *"workflow run publish.yml"*) :;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := exec.Command("bash", "tools/release-wiring/request.sh")
			c.Dir = root
			c.Env = commandEnv(bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+testSHA, "GITHUB_RUN_ID=101", "STATE="+state)
			b, e := c.CombinedOutput()
			errs[i] = e
			outs[i] = string(b)
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("expected one ref winner: errs=%v output=%v", errs, outs)
	}
	lines := readTrace(t, trace)
	count := 0
	for _, line := range lines {
		if strings.Contains(line, "workflow run publish.yml") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d publishers dispatched: %v", count, lines)
	}
	// A subsequent request is not replay authorization, even for the same SHA.
	out, e := runScript(t, root, "tools/release-wiring/request.sh", bin, trace, "RELEASE_VERSION=9.8.7", "RELEASE_SHA="+testSHA, "GITHUB_RUN_ID=101", "STATE="+state)
	if e == nil || !strings.Contains(out, "Could not create v9.8.7") {
		t.Fatalf("duplicate request was not refused: %v %s", e, out)
	}
}

func TestPinnedRealGoReleaserReadonlyPreparationFeedsActualPackagingCaller(t *testing.T) {
	binary := os.Getenv("GORELEASER_TEST_BINARY")
	if binary == "" {
		t.Skip("set GORELEASER_TEST_BINARY to the independently checksum-verified official v2.18.2 CLI")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	versionOut, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOut), "GitVersion:    2.18.2") {
		t.Fatalf("requires official pinned v2.18.2: %v %s", err, versionOut)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(filepath.Join(target, "cmd/yolo"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module fixture.invalid/yolo\n\ngo 1.25\n", "cmd/yolo/main.go": "package main\nfunc main() {}\n", "notes.md": "Offline release body.\n",
		"prepare.sh": "#!/bin/sh\nset -eu\nmkdir -p bundle/share/yolo-jail/bin/linux-amd64 bundle/share/yolo-jail/bin/linux-arm64 bundle/pack-binaries\nprintf 'fixture flake' > bundle/share/yolo-jail/flake.nix\nprintf 'linux binary' > bundle/share/yolo-jail/bin/linux-amd64/yolo-entrypoint\nprintf 'arm binary' > bundle/share/yolo-jail/bin/linux-arm64/yolo-entrypoint\nprintf 'pack binary' > bundle/pack-binaries/fixture_1.2.3_linux_amd64\n",
	}
	// Preserve the production archive/checksum/extra-file settings and matrix;
	// replace only expensive project hooks with inert tiny fixture inputs.
	var config map[string]any
	if err := yaml.Unmarshal(mustRead(t, ".goreleaser.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	config["before"] = map[string]any{"hooks": []string{"sh prepare.sh"}}
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	files[".goreleaser.yaml"] = string(data)
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(target, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(cmd string, args ...string) string {
		t.Helper()
		c := exec.Command(cmd, args...)
		c.Dir = target
		c.Env = append(os.Environ(), "GITHUB_TOKEN=", "GH_TOKEN=", "GOPROXY=off", "GOSUMDB=off")
		out, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("%s %v: %v\n%s", cmd, args, e, out)
		}
		return string(out)
	}
	run("git", "init", "-b", "main")
	run("git", "config", "user.name", "fixture")
	run("git", "config", "user.email", "fixture@example.invalid")
	run("git", "add", ".")
	run("git", "commit", "-m", "offline fixture")
	run("git", "tag", "v1.2.3")
	run("git", "remote", "add", "origin", "https://github.com/fixture/fixture.git")
	out := run(binary, "release", "--clean", "--skip=publish", "--release-notes", "notes.md")
	t.Log(out)
	step := namedStep(t, readWorkflow(t, ".github/workflows/release.yml"), "goreleaser-prepare", "Package only the supported inert GoReleaser output contract")
	c := exec.Command("bash", "-e", "-o", "pipefail", "-c", step.Run)
	c.Dir = root
	c.Env = append(os.Environ(), "RELEASE_VERSION=1.2.3")
	packOut, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("actual packaging rejected real pinned CLI output: %v\n%s", e, packOut)
	}
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		f, e := os.Open(filepath.Join(root, "release-assets/yolo-jail_1.2.3_"+platform+".tar.gz"))
		if e != nil {
			t.Fatal(e)
		}
		gz, e := gzip.NewReader(f)
		if e != nil {
			t.Fatal(e)
		}
		tr := tar.NewReader(gz)
		seen := map[string]bool{}
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			seen[h.Name] = true
		}
		gz.Close()
		f.Close()
		for _, name := range []string{"yolo", "share/yolo-jail/flake.nix", "share/yolo-jail/bin/linux-amd64/yolo-entrypoint", "share/yolo-jail/bin/linux-arm64/yolo-entrypoint"} {
			if !seen[name] {
				t.Errorf("%s archive omitted intended layout %s: %v", platform, name, seen)
			}
		}
	}
	if err := verifyReleaseChecksums(filepath.Join(root, "release-assets"), []string{"yolo-jail_1.2.3_darwin_amd64.tar.gz", "yolo-jail_1.2.3_darwin_arm64.tar.gz", "yolo-jail_1.2.3_linux_amd64.tar.gz", "yolo-jail_1.2.3_linux_arm64.tar.gz", "fixture_1.2.3_linux_amd64"}); err != nil {
		t.Fatal(err)
	}
}

func buildTrustedHelper(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release-wiring")
	cmd := exec.Command("go", "build", "-o", path, "./tools/release-wiring")
	cmd.Dir = repositoryRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build trusted CLI: %v %s", err, out)
	}
	return path
}

// The real claim/version/provenance CLI is run from the real workflow step,
// using a TLS stateful fake GitHub endpoint and inert fake registry commands.
// There is no API/registry/native call outside this process-local fixture.
func TestActualPublisherCallerRefusesDuplicatePartialOldAndFailedOriginalState(t *testing.T) {
	helper := buildTrustedHelper(t)
	wf := readWorkflow(t, ".github/workflows/publish.yml")
	claimStep := namedStep(t, wf, "claim-publication", "Reverify trusted provenance and atomically claim the release before building")
	buildStep := namedStep(t, wf, "build-wheels", "Build exact target wheels with no publication credentials")
	uploadStep := namedStep(t, wf, "publish-wheels", "Publish to PyPI")
	for _, tc := range []struct {
		name, conclusion, newer string
		ambiguous               bool
		wantBuild               bool
	}{
		{name: "initial then duplicate", conclusion: "success", wantBuild: true},
		{name: "partial claim upload", conclusion: "success", ambiguous: true},
		{name: "older version", conclusion: "success", newer: "v9.9.0"},
		{name: "failed original Release", conclusion: "failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			claimed := false
			posts := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/actions/workflows/release-request.yml":
					writeJSON(w, workflowIdentity{ID: 1, Path: ".github/workflows/release-request.yml"})
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/actions/workflows/release.yml":
					writeJSON(w, workflowIdentity{ID: 2, Path: ".github/workflows/release.yml"})
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/actions/runs/101":
					writeJSON(w, actionRun{ID: 101, WorkflowID: 1, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release request v9.8.7 @ " + testSHA, Status: "completed", Conclusion: "success", Attempt: 1})
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/actions/runs/202":
					writeJSON(w, actionRun{ID: 202, WorkflowID: 2, Event: "workflow_dispatch", HeadBranch: "main", DisplayName: "Release v9.8.7 @ " + testSHA + " / request 101", Status: "completed", Conclusion: tc.conclusion, Attempt: 1})
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/releases":
					releases := []publishedRelease{{TagName: "v9.8.7"}}
					if tc.newer != "" {
						releases = append(releases, publishedRelease{TagName: tc.newer})
					}
					writeJSON(w, releases)
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/git/ref/tags/v9.8.7":
					writeJSON(w, map[string]any{"object": map[string]string{"type": "commit", "sha": testSHA}})
				case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/releases/tags/v9.8.7":
					writeJSON(w, claimRelease{ID: 77, TagName: "v9.8.7", UploadURL: serverURL(r) + "/repos/owner/repo/releases/77/assets{?name,label}"})
				case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/releases/77/assets":
					posts++
					if claimed {
						w.WriteHeader(http.StatusUnprocessableEntity)
						return
					}
					if r.URL.Query().Get("name") != publicationClaimAsset {
						http.Error(w, "bad claim", 400)
						return
					}
					var claim publicationClaim
					if err := json.NewDecoder(r.Body).Decode(&claim); err != nil || claim != validTestClaim() {
						http.Error(w, "bad provenance", 400)
						return
					}
					claimed = true
					if tc.ambiguous {
						w.WriteHeader(http.StatusBadGateway)
					} else {
						w.WriteHeader(http.StatusCreated)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cert := filepath.Join(t.TempDir(), "cert.pem")
			if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			bin, trace := fakeCommands(t)
			goScript := `#!/bin/sh
echo "go $*" >> "$TRACE"
case "$*" in
 *tools/release-gate*) exit 0;;
 *tools/release-wiring*) shift 2; exec "$HELPER" "$@";;
 *tools/build-wheels*) echo 'TARGET BUILD' >> "$TRACE";;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(goScript), 0o755); err != nil {
				t.Fatal(err)
			}
			gitScript := `#!/bin/sh
case "$*" in
 *ls-remote*) printf '%s\trefs/tags/v9.8.7\n' "$TEST_SHA";;
 *rev-parse*) echo "$TEST_SHA";;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
				t.Fatal(err)
			}
			env := []string{"RELEASE_VERSION=9.8.7", "RELEASE_SHA=" + testSHA, "REQUEST_RUN_ID=101", "RELEASE_RUN_ID=202", "GITHUB_RUN_ID=303", "RELEASE_ORDER_CHECK=1", "RELEASE_ORDER_ALLOW_CURRENT=1", "GH_TOKEN=offline-test-token", "GITHUB_API_URL=" + server.URL, "GITHUB_UPLOADS_URL=" + server.URL, "SSL_CERT_FILE=" + cert, "HELPER=" + helper}
			runPipeline := func() (string, error) {
				out, e := runWorkflowCommand(t, claimStep, repositoryRoot(t), bin, trace, env...)
				if e != nil {
					return out, e
				}
				if _, e = runWorkflowCommand(t, buildStep, repositoryRoot(t), bin, trace, env...); e != nil {
					return out, e
				}
				return runWorkflowCommand(t, uploadStep, repositoryRoot(t), bin, trace, env...)
			}
			out, e := runPipeline()
			if (e == nil) != tc.wantBuild {
				t.Fatalf("initial %s result=%v output=%s trace=%v", tc.name, e, out, readTrace(t, trace))
			}
			lines := readTrace(t, trace)
			if (traceIndex(lines, "TARGET BUILD") >= 0) != tc.wantBuild || (traceIndex(lines, "registry: uv") >= 0) != tc.wantBuild {
				t.Fatalf("publisher crossed refusal: %v", lines)
			}
			if tc.wantBuild || tc.ambiguous {
				_ = os.Remove(trace)
				out, e = runPipeline()
				if e == nil || !strings.Contains(out, "duplicate upload names") {
					t.Fatalf("persisted claim did not refuse replay: %v %s", e, out)
				}
				lines = readTrace(t, trace)
				if traceIndex(lines, "TARGET BUILD") >= 0 || traceIndex(lines, "registry: uv") >= 0 {
					t.Fatalf("duplicate/partial claim invoked build/upload: %v", lines)
				}
				mu.Lock()
				defer mu.Unlock()
				if posts != 2 {
					t.Fatalf("claim retried ambiguous upload or failed to reach conflict: posts=%d", posts)
				}
			} else {
				mu.Lock()
				defer mu.Unlock()
				if posts != 0 {
					t.Fatalf("failed eligibility acquired claim: %d", posts)
				}
			}
		})
	}
}

func TestActualTrustedUploadCallerPreservesAllAssetsNotesPrereleaseAndPartialState(t *testing.T) {
	helper := buildTrustedHelper(t)
	root := repositoryRoot(t)
	sha := "ce77c0099d4261c54370cecf2652d109075419b3"
	for _, tc := range []struct {
		name, fail string
		forbidSh   bool
	}{{name: "bash helper interpreter boundary", forbidSh: true}, {name: "initial complete"}, {name: "partial upload", fail: "upload"}, {name: "ambiguous draft creation", fail: "create"}, {name: "existing release", fail: "exists"}, {name: "invalid inert artifact", fail: "artifact"}} {
		t.Run(tc.name, func(t *testing.T) {
			version := "9.8.7-rc.1"
			assets, err := expectedReleaseAssets(root, sha, version, filepath.Join(root, ".goreleaser.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			dir := makeAssetFixture(t, assets)
			if tc.fail == "artifact" {
				writeAsset(t, dir, "unknown.sh", []byte("hostile"))
			}
			notes := filepath.Join(t.TempDir(), "notes.md")
			body := "Exact source notes with a [link](https://example.invalid).\n$(do-not-run)\n"
			if err := os.WriteFile(notes, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			bin, trace := fakeCommands(t)
			if err := os.Remove(filepath.Join(bin, "sh")); err != nil {
				t.Fatal(err)
			}
			if tc.forbidSh {
				if err := os.WriteFile(filepath.Join(bin, "sh"), []byte("#!/bin/bash\necho 'FORBIDDEN sh override' >> \"$TRACE\"\nexit 78\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			state := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\ncase \"$*\" in\n *ls-remote*) printf '%s\\trefs/tags/v9.8.7-rc.1\\n' \"$RELEASE_SHA\";;\n *rev-parse*) echo \"$RELEASE_SHA\";;\n cat-file*|show*|ls-tree*) exec \"$REAL_GIT\" \"$@\";;\n *) exit 0;;\nesac\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\ncase \"$*\" in\n *validate-assets*) shift 2; exec \"$HELPER\" \"$@\";;\n *) exit 0;;\nesac\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			gh := `#!/bin/sh
echo "gh $*" >> "$TRACE"
case "$*" in
 *releases/tags*) if [ -d "$STATE/draft" ] || [ "$FAIL" = exists ]; then exit 0; fi; printf 'HTTP/2 404\n';exit 1;;
 *"--method POST"*"/releases"*)
   mkdir "$STATE/draft" || exit 1
   while [ "$#" -gt 0 ]; do if [ "$1" = --input ]; then cp "$2" "$STATE/payload.json"; break; fi;shift;done
   [ "$FAIL" != create ] || exit 1
   echo 77;;
 *"release upload"*) [ "$FAIL" != upload ] || exit 1;;
 *"--method PATCH"*) : > "$STATE/finalized";;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
				t.Fatal(err)
			}
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			env := []string{"GITHUB_WORKSPACE=" + root, "RELEASE_VERSION=" + version, "RELEASE_SHA=" + sha, "RELEASE_ASSETS_DIR=" + dir, "RELEASE_NOTES_FILE=" + notes, "HELPER=" + helper, "STATE=" + state, "FAIL=" + tc.fail, "REAL_GIT=" + realGit}
			out, e := runScript(t, root, "tools/release-wiring/publish-release.sh", bin, trace, env...)
			if (e == nil) != (tc.fail == "") {
				t.Fatalf("upload result=%v output=%s trace=%v", e, out, readTrace(t, trace))
			}
			lines := readTrace(t, trace)
			if tc.forbidSh && traceIndex(lines, "FORBIDDEN sh override") >= 0 {
				t.Fatalf("Bash helper was invoked through sh despite eventual success: %v", lines)
			}
			if tc.fail == "artifact" {
				if traceIndex(lines, "gh ") >= 0 {
					t.Fatalf("invalid data reached release API: %v", lines)
				}
				return
			}
			if tc.fail == "exists" {
				if traceIndex(lines, "--method POST") >= 0 {
					t.Fatalf("existing release overwritten: %v", lines)
				}
				return
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(state, "payload.json"))), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["body"] != body || payload["prerelease"] != true || payload["draft"] != true || payload["tag_name"] != "v"+version {
				t.Fatalf("release body/prerelease/tag lost: %v", payload)
			}
			if tc.fail != "" {
				if traceIndex(lines, "--method PATCH") >= 0 {
					t.Fatalf("partial upload finalized: %v", lines)
				}
			} else {
				for _, name := range append(assets, "checksums.txt") {
					if traceIndex(lines, filepath.Join(dir, name)) < 0 {
						t.Errorf("release omitted %s: %v", name, lines)
					}
				}
				if traceIndex(lines, "--method PATCH") < traceLastIndex(lines, "release upload") {
					t.Fatalf("release finalized before all uploads: %v", lines)
				}
			}
			_ = os.Remove(trace)
			_, e = runScript(t, root, "tools/release-wiring/publish-release.sh", bin, trace, env...)
			if e == nil {
				t.Fatal("existing complete/partial release was replayed")
			}
			if traceIndex(readTrace(t, trace), "--method POST") >= 0 {
				t.Fatal("existing complete/partial release clobbered")
			}
		})
	}
}

// Run only the selected mutation job, retaining successful prerequisite jobs as
// Actions' failed-jobs-only rerun does. Uses steps are inert action/credential
// markers; every production shell run is executed with local fake tools.
func runCachedPublisherJob(t *testing.T, job workflowJob, attempt, root, bin, trace string) error {
	t.Helper()
	cached := map[string]bool{"publisher-preflight": true, "claim-publication": true, "build-wheels": true, "build-image-cache": true, "cache-eligibility": true, "build-builder-image": true, "push-builder-image": true}
	for _, need := range needsList(t, job.Needs) {
		if !cached[need] {
			t.Fatalf("fixture has no cached successful prerequisite %s", need)
		}
	}
	replacements := strings.NewReplacer("${{ github.run_attempt }}", attempt, "${{ inputs.version }}", "9.8.7", "${{ github.repository_owner }}", "owner", "${{ github.actor }}", "fixture", "${{ github.token }}", "offline-token", "${{ secrets.CACHIX_AUTH_TOKEN }}", "offline-cachix", "${{ needs.cache-eligibility.outputs.cache }}", "fixture-cache")
	for _, step := range job.Steps {
		if step.Uses != "" {
			f, e := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if e != nil {
				t.Fatal(e)
			}
			fmt.Fprintf(f, "action/credential %s\n", step.Uses)
			f.Close()
			continue
		}
		env := []string{"GITHUB_RUN_ATTEMPT=" + attempt, "GITHUB_WORKSPACE=" + root, "LATEST=" + filepath.Join(root, "latest"), "RELEASE_VERSION=9.8.7"}
		for key, value := range step.Env {
			if strings.Contains(value, "github.token") || strings.Contains(value, "secrets.") {
				f, e := os.OpenFile(trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if e != nil {
					t.Fatal(e)
				}
				fmt.Fprintf(f, "credential env %s\n", key)
				f.Close()
			}
			env = append(env, key+"="+replacements.Replace(value))
		}
		step.Run = replacements.Replace(step.Run)
		if out, e := runWorkflowCommand(t, step, root, bin, trace, env...); e != nil {
			return fmt.Errorf("%s: %w: %s", step.Name, e, out)
		}
	}
	return nil
}

func TestFailedJobsOnlyPublisherRerunRefusesAtEachActualMutationJobEntry(t *testing.T) {
	wf := readWorkflow(t, ".github/workflows/publish.yml")
	for _, id := range []string{"publish-wheels", "publish-image-cache", "push-builder-image", "publish-builder-index"} {
		t.Run(id, func(t *testing.T) {
			job, ok := wf.Jobs[id]
			if !ok {
				t.Fatalf("missing production mutation job %s", id)
			}
			root := t.TempDir()
			bin, trace := fakeCommands(t)
			for _, dir := range []string{"dist", "nix-cache", "builder-amd64", "builder-arm64"} {
				if e := os.MkdirAll(filepath.Join(root, dir), 0o755); e != nil {
					t.Fatal(e)
				}
			}
			for _, name := range []string{"dist/fixture.whl", "builder-amd64/builder.tar", "builder-arm64/builder.tar"} {
				if e := os.WriteFile(filepath.Join(root, name), []byte("inert prepared input"), 0o600); e != nil {
					t.Fatal(e)
				}
			}
			paths := ""
			for _, name := range []string{"image", "minimal", "copier"} {
				paths += "/nix/store/" + strings.Repeat("a", 32) + "-" + name + "\n"
			}
			if e := os.WriteFile(filepath.Join(root, "nix-cache/store-paths.txt"), []byte(paths), 0o600); e != nil {
				t.Fatal(e)
			}
			for name, body := range map[string]string{
				"go": "exit 0\n", "nix": "exit 0\n",
				"uv":     `echo 'registry uv publish' >> "$TRACE"; echo "$RELEASE_VERSION" > "$LATEST"`,
				"cachix": `echo 'registry cachix push' >> "$TRACE"; echo "$RELEASE_VERSION" > "$LATEST"`,
				"skopeo": `case "$1" in inspect) case "$*" in *arm64*) echo linux/arm64;; *) echo linux/amd64;; esac;; login) echo 'credential skopeo login' >> "$TRACE";; copy) echo "registry skopeo $*" >> "$TRACE"; echo "$RELEASE_VERSION" > "$LATEST";; esac`,
				"docker": `case "$*" in *login*) echo 'credential docker login' >> "$TRACE";; *imagetools\ create*) echo "registry docker $*" >> "$TRACE"; echo "$RELEASE_VERSION" > "$LATEST";; esac`,
			} {
				if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); e != nil {
					t.Fatal(e)
				}
			}
			if e := runCachedPublisherJob(t, job, "1", root, bin, trace); e != nil {
				t.Fatalf("first attempt did not reach actual fake writer: %v", e)
			}
			lines := readTrace(t, trace)
			if traceIndex(lines, "registry ") < 0 {
				t.Fatalf("first attempt skipped actual mutation command: %v", lines)
			}
			if id == "push-builder-image" {
				for _, tag := range []string{"9.8.7-amd64", "9.8.7-arm64", "latest-amd64", "latest-arm64"} {
					if traceIndex(lines, ":"+tag) < 0 {
						t.Errorf("baseline missed image tag %s: %v", tag, lines)
					}
				}
			}
			if id == "publish-builder-index" && traceIndex(lines, ":latest") < 0 {
				t.Fatalf("baseline missed latest index: %v", lines)
			}
			// A newer version now exists, while every old prerequisite remains cached.
			if e := os.WriteFile(filepath.Join(root, "latest"), []byte("9.9.0"), 0o600); e != nil {
				t.Fatal(e)
			}
			_ = os.Remove(trace)
			e := runCachedPublisherJob(t, job, "2", root, bin, trace)
			lines = readTrace(t, trace)
			if e == nil || traceIndex(lines, "credential") >= 0 || traceIndex(lines, "registry ") >= 0 || readFile(t, filepath.Join(root, "latest")) != "9.9.0" {
				t.Fatalf("failed-jobs-only attempt 2 bypassed job entry refusal: err=%v trace=%v latest=%s", e, lines, readFile(t, filepath.Join(root, "latest")))
			}
			if !strings.Contains(e.Error(), "inspect") {
				t.Fatalf("refusal omitted read-only next step: %v", e)
			}
		})
	}
}

func TestOtherNormalPublicationJobsGuardBeforeCredentialSteps(t *testing.T) {
	for _, tc := range []struct{ path, id string }{{"publish.yml", "claim-publication"}, {"release.yml", "publish-release"}} {
		t.Run(tc.id, func(t *testing.T) {
			job := readWorkflow(t, ".github/workflows/"+tc.path).Jobs[tc.id]
			if len(job.Steps) == 0 || job.Steps[0].Uses != "" || job.Steps[0].Name != "Refuse publication replay at job entry" {
				t.Fatalf("%s does not refuse before its first credential/action step", tc.id)
			}
			first := job.Steps[0]
			if first.Env["GITHUB_RUN_ATTEMPT"] != "${{ github.run_attempt }}" {
				t.Fatal("entry guard is not bound to actual Actions attempt")
			}
			bin, trace := fakeCommands(t)
			for _, attempt := range []string{"1", "2"} {
				out, e := runWorkflowCommand(t, first, repositoryRoot(t), bin, trace, "GITHUB_RUN_ATTEMPT="+attempt)
				if (e == nil) != (attempt == "1") {
					t.Fatalf("guard attempt %s: %v %s", attempt, e, out)
				}
			}
		})
	}
}

func TestHomebrewFormulaHeredocNeverExecutesLiteralYoloForNormalOrBackfill(t *testing.T) {
	wf := readWorkflow(t, ".github/workflows/release.yml")
	for _, id := range []string{"publish-release", "homebrew-only"} {
		t.Run(id, func(t *testing.T) {
			found := false
			for _, step := range wf.Jobs[id].Steps {
				found = found || strings.Contains(step.Run, `tools/release-wiring/update-homebrew.sh`)
			}
			if !found {
				t.Fatalf("%s lost actual formula helper caller", id)
			}
			root := t.TempDir()
			bin, trace := fakeCommands(t)
			for name, body := range map[string]string{
				"curl": "printf 'offline source archive'\n",
				"yolo": `echo 'FORBIDDEN yolo command substitution' >> "$TRACE"; exit 79`,
				"git":  `case "$1" in clone) mkdir -p "$3/Formula";; diff) exit 1;; push) echo 'fake tap writer' >> "$TRACE";; esac`,
			} {
				if e := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/bash\n"+body+"\n"), 0o755); e != nil {
					t.Fatal(e)
				}
			}
			out, e := runScript(t, root, filepath.Join(repositoryRoot(t), "tools/release-wiring/update-homebrew.sh"), bin, trace, "VERSION=9.8.7", "RELEASE_TAG=v9.8.7", "HOMEBREW_TAP_TOKEN=offline-tap-token")
			if e != nil {
				t.Fatalf("formula generation failed: %v %s", e, out)
			}
			formula := readFile(t, filepath.Join(root, "tap/Formula/yolo-jail.rb"))
			lines := readTrace(t, trace)
			if traceIndex(lines, "FORBIDDEN yolo") >= 0 || !strings.Contains(formula, "The first `yolo` run in a workspace") {
				t.Fatalf("%s formula executed literal caveat command or lost backticks: trace=%v formula=%s", id, lines, formula)
			}
			if !strings.Contains(formula, "refs/tags/v9.8.7.tar.gz") || traceIndex(lines, "fake tap writer") < 0 {
				t.Fatal("variable-expanding formula/publication baseline was lost")
			}
		})
	}
}
