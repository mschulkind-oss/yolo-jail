package cli

import (
	"strings"
	"testing"
)

func TestRequestWritesProjectThenChecks(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\", \"new_branches\"]\n\n[allow.github]\nalways = [\"read\"]\n")
	userBefore, fragmentBefore := readFile(t, fx.userConfig), readFile(t, fx.fragment)

	rc, out, errs := captureStd(t, func() int { return registry["request"]([]string{"request", "git", "push", "main"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	if got := readFile(t, fx.project); !strings.Contains(got, "[requests.git]") || !strings.Contains(got, `push = ["main"]`) {
		t.Fatalf("project file was not written:\n%s\nstdout=%q stderr=%q", got, out, errs)
	}
	if !strings.Contains(out, fx.project) || !strings.Contains(out, "yolo config apply") {
		t.Fatalf("the verb did not name the file and the host step: %q", out)
	}
	if strings.Contains(out, "runs without it") {
		t.Fatalf("an allowed request was reported as skipped: %q", out)
	}

	// A second ref goes into the same list; a yes/no capability is set to true.
	rc, out, errs = captureStd(t, func() int { return registry["request"]([]string{"request", "git", "push", "release/*"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	rc, out, errs = captureStd(t, func() int { return registry["request"]([]string{"request", "git", "new_branches"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	rc, out, errs = captureStd(t, func() int { return registry["request"]([]string{"request", "github", "repos", "acme/other"}) })
	assertPublicPostWriteCheck(t, rc, out, errs)
	got := readFile(t, fx.project)
	for _, want := range []string{`push = ["main", "release/*"]`, "new_branches = true", "[requests.github]", `repos = ["acme/other"]`} {
		if !strings.Contains(got, want) {
			t.Fatalf("project file lacks %q:\n%s", want, got)
		}
	}
	if readFile(t, fx.userConfig) != userBefore || readFile(t, fx.fragment) != fragmentBefore {
		t.Fatal("yolo request edited the user config")
	}

	// Refusals name the declared set and the command that works.
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"request", "git", "pushh", "main"}, []string{"fetch", "new_branches", "push", "push_tags", "yolo request git"}},
		{[]string{"request", "git", "push"}, []string{"ref name", "yolo request git push main"}},
		{[]string{"request", "git", "new_branches", "main"}, []string{"yes/no", "yolo request git new_branches"}},
		{[]string{"request", "git", "repos", "acme/other"}, []string{"git remote add"}},
		{[]string{"request", "git", "fetch"}, []string{"yolo allow git fetch"}},
		{[]string{"request", "github", "repos", "nope"}, []string{"OWNER/REPO", "yolo request github repos acme/other"}},
	} {
		rc, out, errs := captureStd(t, func() int { return registry["request"](tc.args) })
		if rc != 1 {
			t.Fatalf("%v rc=%d, want 1; stdout=%q stderr=%q", tc.args, rc, out, errs)
		}
		for _, want := range tc.want {
			if !strings.Contains(errs, want) {
				t.Fatalf("%v refusal lacks %q: %q", tc.args, want, errs)
			}
		}
	}
}

// A dispatch_workflows request takes one workflow file name, or *, into a list; anything else is
// refused naming the command that works (docs/design/github-write-scopes.md GW-D14).
func TestRequestDispatchWorkflowsTakesAWorkflowFile(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.github]\nalways = [\"read\"]\non_request = [\"dispatch_workflows\"]\n")
	for _, file := range []string{"nightly-macos.yml", "macos-user.yml"} {
		rc, out, errs := captureStd(t, func() int {
			return registry["request"]([]string{"request", "github", "dispatch_workflows", file})
		})
		assertPublicPostWriteCheck(t, rc, out, errs)
		if strings.Contains(out, "runs without it") {
			t.Fatalf("an allowed request was reported as skipped: %q", out)
		}
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `dispatch_workflows = ["nightly-macos.yml", "macos-user.yml"]`) {
		t.Fatalf("project file lacks the workflows:\n%s", got)
	}
	for _, args := range [][]string{
		{"request", "github", "dispatch_workflows"},
		{"request", "github", "dispatch_workflows", ".github/workflows/nightly.yml"},
		{"request", "github", "dispatch_workflows", "12345"},
	} {
		rc, out, errs := captureStd(t, func() int { return registry["request"](args) })
		if rc != 1 || !strings.Contains(errs, "workflow file name") || !strings.Contains(errs, "yolo request github dispatch_workflows nightly.yml") {
			t.Fatalf("%v rc=%d, stdout=%q stderr=%q", args, rc, out, errs)
		}
	}
}

func TestRequestOutsideCeilingIsSkippedNotFatal(t *testing.T) {
	fx := newNamedVerbFixture(t, "", "[allow.git]\nalways = [\"fetch\"]\n")
	rc, out, errs := captureStd(t, func() int { return registry["request"]([]string{"request", "git", "push", "main"}) })
	// The verb's exit is the post-write check's, whatever that check finds about the rest of the
	// fixture; what this test pins is that the ceiling itself refuses nothing.
	assertPublicPostWriteCheck(t, rc, out, errs)
	if strings.Contains(errs, "yolo request:") || strings.Contains(errs, "config edit refused") {
		t.Fatalf("a request outside the ceiling was refused: stdout=%q stderr=%q", out, errs)
	}
	if got := readFile(t, fx.project); !strings.Contains(got, `push = ["main"]`) {
		t.Fatalf("the request was not written:\n%s", got)
	}
	for _, want := range []string{"runs without it", "yolo allow git on_request push", "yolo config apply"} {
		if !strings.Contains(out, want) {
			t.Fatalf("skipped request report lacks %q: %q", want, out)
		}
	}
}
