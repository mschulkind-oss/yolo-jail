package integration

import (
	"strings"
	"testing"
)

// These run under -short (no container): they cover the image-skew check's pure
// decision logic — the parts that decide whether the suite runs at all — so a
// mistake there is caught by the pre-commit gate rather than by a confusing
// integration run. The exec'ing halves (nix eval, the in-image `cat`) are
// exercised by every real integration run.

// TestParseSkewMode pins the DEFAULT: an unset knob must mean "fail", because the
// whole point is that the suite never silently tests stale code. An unrecognized
// value must be an error, not a silent fallback — "warning" must not read as
// "off" (nor as "fail" while the author believes it is off).
func TestParseSkewMode(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want skewMode
	}{
		{"", skewFail}, // unset → the safe default
		{"fail", skewFail},
		{"FAIL", skewFail}, // case-insensitive
		{"warn", skewWarn},
		{" warn ", skewWarn}, // tolerate stray whitespace
		{"off", skewOff},
	} {
		got, err := parseSkewMode(tc.in)
		if err != nil {
			t.Errorf("parseSkewMode(%q) errored: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSkewMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"warning", "true", "1", "no"} {
		if _, err := parseSkewMode(bad); err == nil {
			t.Errorf("parseSkewMode(%q) = nil error; a typo must not silently pick a mode", bad)
		}
	}
}

// TestParseImageIdentity locks the shape both sides must produce. The rejected
// values are not hypothetical: the first is what an image built before
// 2026-09-12 answers (a store path, from the readlink rung of
// identityReadScript), the second is an image with no identity at all, and the
// rest are a digest that is truncated, uppercased or untagged — each of which
// would otherwise compare as "just a different string" and be reported as
// staleness.
func TestParseImageIdentity(t *testing.T) {
	const id = "sha256:43bf9e8f065545ef74b4842241b872a053425004a28e19989cb2e2336c4bb4b2"
	got, err := parseImageIdentity(id + "\n")
	if err != nil {
		t.Fatalf("valid identity errored: %v", err)
	}
	if got != id {
		t.Errorf("got %q, want %q", got, id)
	}
	for _, bad := range []string{
		"",
		"/nix/store/bh2wnsa9rmbacx6lciwcfind54n1b5pj-yolo-jail-image-identity",
		identityAbsent,
		"sha256:43bf9e8f",                   // truncated
		strings.ToUpper(id),                 // not lowercase hex
		strings.TrimPrefix(id, "sha256:"),   // no algorithm tag
		"sha512:" + strings.Repeat("a", 64), // a tag we do not write
	} {
		if _, err := parseImageIdentity(bad); err == nil {
			t.Errorf("parseImageIdentity(%q) = nil error, want a parse failure", bad)
		}
	}
}

// TestIdentityHintExplainsTheOneTimeMismatch pins the message for the cost
// OQ-IP3 accepted: every image built before the identity became content-
// addressed mismatches once. The hint is what stops that single event from
// reading as a corrupt image, and it fires on the store-path shape, which is
// exactly what identityReadScript's readlink rung recovers from such an image.
func TestIdentityHintExplainsTheOneTimeMismatch(t *testing.T) {
	hint := identityHint("/nix/store/bh2wnsa9rmbacx6lciwcfind54n1b5pj-yolo-jail-image-identity")
	for _, want := range []string{"STORE PATH", "predates", "2026-09-12"} {
		if !strings.Contains(hint, want) {
			t.Errorf("the pre-2026-09-12 hint is missing %q:\n%s", want, hint)
		}
	}
	// An image with no identity at all is a different fault and must not be
	// described as an old image — it is a flake that stopped baking the file.
	for _, missing := range []string{identityAbsent, ""} {
		hint := identityHint(missing)
		if !strings.Contains(hint, "NO identity") {
			t.Errorf("identityHint(%q) does not name the missing file:\n%s", missing, hint)
		}
	}
	// A well-formed identity needs no explanation; a hint there would be noise
	// on the ONLY case that is a genuine stale image.
	if h := identityHint("sha256:" + strings.Repeat("a", 64)); h != "" {
		t.Errorf("a valid identity got a hint:\n%s", h)
	}
}

// TestSkewMessageIsActionable: the message is the deliverable — a mystery turned
// into an instruction. It must name BOTH identities (so the reader can see this
// is a source mismatch, not a flaky test) and hand over the commands that fix it,
// including the git-add caveat (nix only sees tracked files, so a rebuild without
// it produces a still-stale image and a second round of confusion).
func TestSkewMessageIsActionable(t *testing.T) {
	wantID := "sha256:" + strings.Repeat("a", 64)
	gotID := "sha256:" + strings.Repeat("b", 64)
	msg := skewMessage("localhost/yolo-jail:latest", "podman", wantID, gotID)
	for _, want := range []string{
		wantID,                          // what the source wants
		gotID,                           // what is loaded
		rebuildEnv + "=1",               // the one-command fix
		skewEnv + "=warn",               // the documented escape hatch
		"nix build --impure .#ociImage", // the manual fix
		"containers-storage:",           // ...delivered, not streamed
		".#imageCopier",                 // ...with the copier that reads it
		"git add",                       // the tracked-files trap
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("skew message is missing %q:\n%s", want, msg)
		}
	}
	// THE DESTINATION IS PER RUNTIME, and getting it wrong hands a Mac user a
	// command that writes into a store nothing reads. Apple Container has no
	// containers-storage; it takes a file and a loader.
	ac := skewMessage("yolo-jail:latest", "container", wantID, gotID)
	if strings.Contains(ac, "containers-storage:") {
		t.Errorf("the Apple Container fix names podman's store:\n%s", ac)
	}
	for _, want := range []string{"oci-archive:", "container image load -i"} {
		if !strings.Contains(ac, want) {
			t.Errorf("the Apple Container fix is missing %q:\n%s", want, ac)
		}
	}
}
