package cli

import (
	"bytes"
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
	if rc := applyMain(nil, &out, &errw, false); rc != 0 {
		t.Fatalf("apply (jail) rc=%d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "jail") {
		t.Errorf("apply (jail) should say so:\n%s", out.String())
	}

	// --host and --sealed are recognized flags that fail closed until their phase ships.
	for _, flag := range []string{"--host", "--sealed"} {
		out.Reset()
		errw.Reset()
		if rc := applyMain([]string{flag}, &out, &errw, false); rc == 0 {
			t.Errorf("apply %s should fail closed (not built yet), got rc=0", flag)
		}
	}

	// A bogus notch is a usage error (rc 2), not a silent default.
	out.Reset()
	errw.Reset()
	if rc := applyMain([]string{"--at", "bogus"}, &out, &errw, false); rc != 2 {
		t.Errorf("apply --at bogus should be a usage error (rc 2), got %d", rc)
	}
}
