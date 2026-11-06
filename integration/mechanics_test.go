package integration

// mechanics_test.go is the real nested-jail regression for the launch's applied mechanics
// snapshot: the skill-only pack makes a mechanics destination without starting an agent, and a
// user host_files fixture proves the recorded facts describe bytes the guest actually received.
// It then separates desired config from applied state at attach, and follows one supported live
// mount through config apply, the authenticated records, the host map and a later attach.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const nestedMechanicsSkillPath = "/home/agent/.claude/skills/jail-mechanics"

// TestNestedMechanicsTracksAppliedLaunchAndLiveMounts proves the consumer path in a nested
// Linux/Podman jail. The synthetic pack contributes only skills: it installs no executable and
// selects no real coding agent. Its host-file source and the later mount source are disposable
// files minted by this test, never files copied from the current home.
func TestNestedMechanicsTracksAppliedLaunchAndLiveMounts(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" || detectRuntime() != "podman" {
		t.Skip("the real live-mount apply path is supported here only by Linux Podman")
	}

	fixtureRoot := resolvedTempDir(t)
	fixtureBytes := []byte("NESTED-MECHANICS-FIXTURE-BYTES-7319\n")
	fixtureSource := filepath.Join(fixtureRoot, "mechanics-fixture.txt")
	if err := os.WriteFile(fixtureSource, fixtureBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	packRoot := resolvedTempDir(t)
	skillDir := filepath.Join(packRoot, "skills", "fixture-rules")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: fixture-rules\ndescription: mechanics integration fixture\n---\n# Fixture rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"nested-mechanics-fixture","description":"skills-only mechanics integration fixture","contributes":[{"kind":"skills","from":"skills","into":".claude/skills"}]}`
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs":[{"source":"file://`+filepath.ToSlash(packRoot)+`","name":"nested-mechanics-fixture"}],`+
		`"host_files":[{"path":"~/.config/yolo-mechanics/fixture.txt","source":"`+filepath.ToSlash(fixtureSource)+`"}]}`)

	workspace := writeProject(t, `{}`)
	cname := jailName(workspace)
	const release = "release-nested-mechanics"
	holder := startYoloBackground(t, "mechanics-holder", workspace,
		`printf 'FIXTURE-BEGIN\n'; cat /home/agent/.config/yolo-mechanics/fixture.txt; `+
			`printf 'FIXTURE-END\n'; `+
			`printf 'INITIAL-INDEX-BEGIN\n'; cat `+nestedMechanicsSkillPath+`/SKILL.md; `+
			`printf '\nINITIAL-INDEX-END\n'; `+
			`printf 'INITIAL-MAP-BEGIN\n'; cat `+nestedMechanicsSkillPath+`/host-map.md; `+
			`printf '\nINITIAL-MAP-END\n'; `+
			`printf 'POST-MAP-DIGEST='; sha256sum /home/agent/.config/yolo-mechanics/fixture.txt | cut -d ' ' -f 1; `+
			`for _ in $(seq 1 1200); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(workspace, release), []byte("release\n"), 0o644) })
	awaitOutput(t, holder, regexp.MustCompile(regexp.QuoteMeta(string(fixtureBytes[:len(fixtureBytes)-1]))))
	digest := sha256.Sum256(fixtureBytes)
	awaitOutput(t, holder, regexp.MustCompile("POST-MAP-DIGEST="+hex.EncodeToString(digest[:])))
	awaitLaunchLockReleased(t, workspace, holder)

	initialOutput := holder.combined()
	for _, want := range []string{"FIXTURE-BEGIN", string(fixtureBytes), "INITIAL-INDEX-BEGIN", "# This jail", "INITIAL-MAP-BEGIN", "# Paths in this environment", fixtureSource} {
		if !strings.Contains(initialOutput, want) {
			t.Fatalf("fresh nested launch output lacks %q; mechanics index/map or delivered fixture bytes were not observed:\n%s", want, initialOutput)
		}
	}
	initialSnapshot, initialMounts := nestedMechanicsRecords(t, cname)
	initialSkillStage := nestedMechanicsSkillStage(t, initialSnapshot)
	initialMap, err := os.ReadFile(filepath.Join(initialSkillStage, "jail-mechanics", "host-map.md"))
	if err != nil {
		t.Fatalf("read the host-side mechanics map staged for the guest: %v", err)
	}
	fixtureFact := nestedMechanicsPath(t, initialSnapshot, "/home/agent/.config/yolo-mechanics/fixture.txt")
	if fixtureFact.OriginalSource != fixtureSource || fixtureFact.BackingSource != fixtureSource ||
		fixtureFact.HostSource != fixtureSource || fixtureFact.Delivery != "read-only bind from original host source" {
		t.Fatalf("fixture path fact does not identify the delivered original host file: %+v", fixtureFact)
	}
	if !strings.Contains(string(initialMap), fixtureSource) || !strings.Contains(string(initialMap), fixtureFact.Delivery) {
		t.Fatalf("fresh host map does not report the host-file source and actual delivery:\n%s", initialMap)
	}
	if len(initialMounts.Entries) != 0 {
		t.Fatalf("fresh live-mount record unexpectedly starts with mounts: %+v", initialMounts.Entries)
	}

	// Editing desired config alone must not cause an ordinary non-interactive attach to
	// describe or mount it. This attach intentionally omits --accept-config-changes: that flag
	// asks the attach-time live-apply gate to act, which is the explicit yolo config apply below.
	liveRoot := resolvedTempDir(t)
	liveBytes := "LIVE-MOUNT-FIXTURE-BYTES-2846\n"
	if err := os.WriteFile(filepath.Join(liveRoot, "live.txt"), []byte(liveBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	liveDest := "/ctx/mechanics-live"
	workspaceConfig, err := json.Marshal(struct {
		Mounts []string `json:"mounts"`
	}{Mounts: []string{filepath.ToSlash(liveRoot) + ":" + liveDest}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), append(workspaceConfig, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	pending := runCommand(t, workspace, []string{"run", "--", "bash", "-lc",
		`if test -e /ctx/mechanics-live; then echo PENDING-MOUNT-PRESENT; else echo PENDING-MOUNT-ABSENT; fi; ` +
			`cat ` + nestedMechanicsSkillPath + `/host-map.md`})
	if pending.rc != 0 || !strings.Contains(pending.stderr, "Attaching to existing jail") || !strings.Contains(pending.stdout, "PENDING-MOUNT-ABSENT") {
		t.Fatalf("non-approving attach did not leave the desired mount pending: rc=%d\n%s", pending.rc, pending.combined())
	}
	if strings.Contains(pending.stdout, liveDest) {
		t.Fatalf("attach rendered edited desired config instead of authenticated applied facts; pending mount %s appears in the map:\n%s", liveDest, pending.stdout)
	}
	unchangedSnapshot, unchangedMounts := nestedMechanicsRecords(t, cname)
	if unchangedSnapshot.Token != initialSnapshot.Token || unchangedMounts.Token != initialMounts.Token || len(unchangedMounts.Entries) != 0 {
		t.Fatalf("a non-approving attach changed authenticated applied state: snapshot=%q mounts=%+v", unchangedSnapshot.Token, unchangedMounts)
	}

	applied := runYoloCLI(t, workspace, "config", "apply", "--accept-config-changes")
	if applied.rc != 0 || !strings.Contains(applied.combined(), liveDest+" (read-only): applied now") {
		t.Fatalf("supported `yolo config apply --accept-config-changes` did not apply the live mount: rc=%d\n%s", applied.rc, applied.combined())
	}
	updatedSnapshot, updatedMounts := nestedMechanicsRecords(t, cname)
	if updatedSnapshot.Token != initialSnapshot.Token || updatedMounts.Token != initialSnapshot.Token {
		t.Fatalf("live apply lost the running jail's authentication token: initial=%q mechanics=%q mounts=%q", initialSnapshot.Token, updatedSnapshot.Token, updatedMounts.Token)
	}
	if len(updatedMounts.Entries) != 1 || updatedMounts.Entries[0].Source != filepath.ToSlash(liveRoot) ||
		updatedMounts.Entries[0].Dest != liveDest || updatedMounts.Entries[0].RW {
		t.Fatalf("authenticated live-mount record does not contain the applied read-only fixture: %+v", updatedMounts.Entries)
	}
	liveFact := nestedMechanicsPath(t, updatedSnapshot, liveDest)
	if liveFact.HostSource != filepath.ToSlash(liveRoot) || liveFact.Mode != "read-only" {
		t.Fatalf("authenticated mechanics facts omit the applied mount: %+v", liveFact)
	}
	updatedSkillStage := nestedMechanicsSkillStage(t, updatedSnapshot)
	updatedMap, err := os.ReadFile(filepath.Join(updatedSkillStage, "jail-mechanics", "host-map.md"))
	if err != nil {
		t.Fatalf("read updated host mechanics map: %v", err)
	}
	if !strings.Contains(string(updatedMap), liveDest) || !strings.Contains(string(updatedMap), filepath.ToSlash(liveRoot)) {
		t.Fatalf("successful config apply did not refresh the host map from applied facts:\n%s", updatedMap)
	}

	attached := runCommand(t, workspace, []string{"run", "--", "bash", "-lc",
		`printf 'ATTACHED-LIVE-BYTES='; cat /ctx/mechanics-live/live.txt; ` +
			`printf '\nATTACHED-MAP-BEGIN\n'; cat ` + nestedMechanicsSkillPath + `/host-map.md; printf '\nATTACHED-MAP-END\n'`})
	if attached.rc != 0 || !strings.Contains(attached.stderr, "Attaching to existing jail") {
		t.Fatalf("attach after live apply did not enter the existing jail: rc=%d\n%s", attached.rc, attached.combined())
	}
	if !strings.Contains(attached.stdout, "ATTACHED-LIVE-BYTES="+strings.TrimSpace(liveBytes)) ||
		!strings.Contains(attached.stdout, liveDest) || !strings.Contains(attached.stdout, filepath.ToSlash(liveRoot)) {
		t.Fatalf("post-apply attach did not see the live mount and its applied mechanics map:\n%s", attached.combined())
	}
}

type nestedMechanicsRecord struct {
	Token string
	Facts struct {
		Paths []nestedMechanicsPathFact
	}
}

type nestedMechanicsPathFact struct {
	Path, HostSource, OriginalSource, BackingSource, Mode, Delivery, Origin string
}

type nestedMechanicsMountRecord struct {
	Token   string
	Entries []struct {
		Source string
		Dest   string
		RW     bool
	}
}

func nestedMechanicsRecords(t *testing.T, cname string) (nestedMechanicsRecord, nestedMechanicsMountRecord) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	inspect, err := exec.CommandContext(ctx, detectRuntime(), "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname).Output()
	if err != nil {
		t.Fatalf("inspect running jail env for mechanics authentication: %v", err)
	}
	var inspectedToken string
	for _, line := range strings.Split(string(inspect), "\n") {
		if value, ok := strings.CutPrefix(line, "YOLO_CTX_MOUNTS_TOKEN="); ok {
			inspectedToken = value
		}
	}
	if inspectedToken == "" {
		t.Fatal("running Podman jail has no mechanics/live-mount authentication token")
	}
	base := filepath.Join(paths.AgentsDir(), cname)
	mechanicsBytes, err := os.ReadFile(filepath.Join(base, "ctx-mounts", "mechanics.json"))
	if err != nil {
		t.Fatalf("read applied mechanics record: %v", err)
	}
	var mechanics nestedMechanicsRecord
	if err := json.Unmarshal(mechanicsBytes, &mechanics); err != nil {
		t.Fatalf("decode applied mechanics record: %v", err)
	}
	mountBytes, err := os.ReadFile(filepath.Join(base, "ctx-mounts", "record.json"))
	if err != nil {
		t.Fatalf("read live-mount record: %v", err)
	}
	var mounts nestedMechanicsMountRecord
	if err := json.Unmarshal(mountBytes, &mounts); err != nil {
		t.Fatalf("decode live-mount record: %v", err)
	}
	if mechanics.Token != inspectedToken || mounts.Token != inspectedToken {
		t.Fatalf("mechanics/live-mount records do not authenticate to the inspected jail: inspect=%q mechanics=%q mounts=%q", inspectedToken, mechanics.Token, mounts.Token)
	}
	return mechanics, mounts
}

func nestedMechanicsSkillStage(t *testing.T, record nestedMechanicsRecord) string {
	t.Helper()
	for _, fact := range record.Facts.Paths {
		if fact.Origin == "pack skills staging" && fact.Path == "/home/agent/.claude/skills" && fact.HostSource != "" {
			return fact.HostSource
		}
	}
	t.Fatalf("authenticated facts have no staging source for the declared skills-only pack: %+v", record.Facts.Paths)
	return ""
}

func nestedMechanicsPath(t *testing.T, record nestedMechanicsRecord, path string) nestedMechanicsPathFact {
	t.Helper()
	for _, fact := range record.Facts.Paths {
		if fact.Path == path {
			return fact
		}
	}
	t.Fatalf("authenticated mechanics facts omit %s: %+v", path, record.Facts.Paths)
	return nestedMechanicsPathFact{}
}
