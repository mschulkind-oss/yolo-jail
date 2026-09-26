package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPublishRequiresTrustedPublishing pins how publish.yml uploads the wheels this package
// builds.
//
// PyPI authorizes the upload by Trusted Publishing, and it registers ONE workflow file by
// name. `uv publish` defaults to `--trusted-publishing automatic`, which on an OIDC failure —
// a renamed workflow, a changed environment — falls back to an unauthenticated upload and
// reports "Missing credentials", pointing at a token this repository never had instead of at
// the registration. `always` makes the same failure name itself. Nothing but the next release
// would run this line, so it is pinned here.
func TestPublishRequiresTrustedPublishing(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "workflows", "publish.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	uploads := regexp.MustCompile(`(?m)^\s*run:\s*uv publish\b.*$`).FindAllString(string(body), -1)
	if len(uploads) == 0 {
		t.Fatalf("%s no longer runs `uv publish` on a `run:` line — this pin has lost its "+
			"subject; move it to wherever the wheels are uploaded now", path)
	}
	for _, line := range uploads {
		if !strings.Contains(line, "--trusted-publishing always") {
			t.Errorf("%s uploads with %q, which lets an OIDC failure fall back to an "+
				"unauthenticated upload that reports \"Missing credentials\"; pass "+
				"--trusted-publishing always", path, strings.TrimSpace(line))
		}
	}
}
