package integration

// jailgrant_test.go is the integration tier of --with-credentials AT A JAIL LAUNCH
// (docs/design/credential-sources-separation.md §5.2, ES-D31 to ES-D36): a real jail launched with
// the grant holds the granted key in its first session and in a session attached to it later,
// holds no key the grant did not name, carries the value on no process's command line, and refuses
// an attach that asks for a provider it was not launched with. The unit tier, which pins each call
// site, is internal/cli/run's jailgrant_test.go; only a real container shows the runtime taking a
// bare `-e NAME`'s value from its client's environment and every exec session inheriting it.

import (
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

func TestAJailLaunchedWithCredentialsHoldsThemForEverySession(t *testing.T) {
	requireJail(t)
	const zaiKey, cerebrasKey = "integration-grant-zai-not-a-real-key", "integration-grant-cerebras-not-a-real-key"
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["zai", "cerebras"], "env_sources": [`+
		`{"ZAI_API_KEY": "`+zaiKey+`", "CEREBRAS_API_KEY": "`+cerebrasKey+`"}]}`)

	const releaseName = "release-jail-grant"
	first := startYoloBackgroundWith(t, "first", dir, []string{"--with-credentials", "zai"},
		`echo "FIRST-ZAI=[${ZAI_API_KEY-}] FIRST-CEREBRAS=[${CEREBRAS_API_KEY-}]"; `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644)
	})
	// THE ANSWER, NOT THE MENTION (attachprofile_test.go's firstHasAnswer): the boot echoes the
	// command before running it, so the sync point excludes the unexpanded `$`.
	answered := regexp.MustCompile(`FIRST-ZAI=\[[^\]$]*\] FIRST-CEREBRAS=\[[^\]$]*\]`)
	deadline := time.Now().Add(jailTimeout())
	for !answered.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("the granted launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the granted session never reported its env within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	up := first.combined()
	if want := "FIRST-ZAI=[" + zaiKey + "] FIRST-CEREBRAS=[]"; !strings.Contains(up, want) {
		t.Errorf("the first session must hold the granted key and no other (%q):\n%s", want, up)
	}
	if !strings.Contains(up, "Credential grant (--with-credentials zai): this jail holds") {
		t.Errorf("the launch must disclose the grant:\n%s", up)
	}
	if strings.Count(up, zaiKey) > 1 {
		t.Errorf("the launch printed the granted VALUE beyond the session's own echo:\n%s", up)
	}
	// No process on this machine carries the value on its command line: the container argv names
	// the key alone. /proc is Linux's; the value the session echoed is in no argv either.
	if goruntime.GOOS == "linux" {
		if hits := cmdlinesHolding(zaiKey); len(hits) > 0 {
			t.Errorf("a command line carries the granted value: %v", hits)
		}
	}
	awaitLaunchLockReleased(t, dir, first)

	// A later session, attached with no flag, holds the jail's set and is told so.
	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc",
		`echo "ATTACH-ZAI=[${ZAI_API_KEY-}] ATTACH-CEREBRAS=[${CEREBRAS_API_KEY-}]"`))
	if r.rc != 0 {
		t.Fatalf("the plain attach failed: rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"Attaching to existing jail",
		"ATTACH-ZAI=[" + zaiKey + "] ATTACH-CEREBRAS=[]",
		"Credential grant (this jail was launched with --with-credentials zai)"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the attached session must show %q:\n%s", want, r.combined())
		}
	}

	// An attach asking for a provider the jail was not launched with is refused, naming the fresh
	// launch, and runs nothing.
	r = runCommand(t, dir, append(jailRunArgs(), "--with-credentials", "cerebras", "--", "bash", "-lc",
		`echo SHOULD-NOT-RUN`))
	if r.rc == 0 {
		t.Fatalf("an attach asking for cerebras must be refused:\n%s", r.combined())
	}
	for _, want := range []string{"Refusing to attach", "cerebras (CEREBRAS_API_KEY)", "'yolo stop'",
		"yolo --with-credentials zai,cerebras -- bash -lc"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
	if strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
		t.Errorf("the refused attach ran its command:\n%s", r.combined())
	}
}

// cmdlinesHolding lists the pids whose command line contains value.
func cmdlinesHolding(value string) []string {
	var hits []string
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		if b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline")); err == nil &&
			strings.Contains(string(b), value) {
			hits = append(hits, e.Name()+": "+strings.ReplaceAll(string(b), "\x00", " "))
		}
	}
	return hits
}
