package check

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// runContainerImageSection drives sectionContainerImage alone, recording every
// probe argv so the QUESTION the checker asks is assertable and not merely its
// wording. Getenv returns "" so inJail() is false (in-jail the section is a
// one-line skip and nothing below applies).
func runContainerImageSection(t *testing.T, rt, storePath string,
	probe func(argv []string) ExecResult) (string, [][]string) {
	t.Helper()
	var out bytes.Buffer
	var seen [][]string
	exec := func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		seen = append(seen, append([]string(nil), argv...))
		return probe(argv)
	}
	o := &Options{
		Getenv:      func(string) string { return "" },
		Exec:        exec,
		Stdout:      &out,
		IsTTYStdout: func() bool { return false },
	}
	fillDefaults(o)
	o.Getenv = func(string) string { return "" }
	o.Exec = exec
	r := newReporter(&out, false)
	o.sectionContainerImage(r, rt, "hint", storePath)
	return out.String(), seen
}

// TestContainerImageSectionAsksForThisConfigsImage: with a store path in hand,
// the checker can make a claim it never could while one :latest tag named every
// image — that the image THIS config would run is loaded.
func TestContainerImageSectionAsksForThisConfigsImage(t *testing.T) {
	const storePath = "/nix/store/checkme-image"
	want := image.JailImageRef("podman", storePath)

	got, seen := runContainerImageSection(t, "podman", storePath,
		func(argv []string) ExecResult { return ExecResult{Ran: true, RC: 0} })

	if len(seen) != 1 || strings.Join(seen[0], " ") != "podman image inspect "+want {
		t.Fatalf("probe = %v, want an inspect of the content ref %q", seen, want)
	}
	if !strings.Contains(got, want) || !strings.Contains(got, "Image loaded for this config") {
		t.Errorf("report does not name this config's image:\n%s", got)
	}

	// The negative half: absent means absent, and the ref is named so the human
	// can go look for it.
	got, _ = runContainerImageSection(t, "podman", storePath,
		func(argv []string) ExecResult { return ExecResult{Ran: true, RC: 1} })
	if !strings.Contains(got, "not loaded") || !strings.Contains(got, want) {
		t.Errorf("absent image not reported against the content ref:\n%s", got)
	}
}

// TestContainerImageSectionWithoutAStorePathAsksByRepository is the regression
// for the false alarm C2 could have manufactured.
//
// `yolo check --no-build` (the documented in-jail preflight), an unresolvable
// repo root, and a host that cannot build all reach this section with no store
// path. Probing `:latest` there would report "image not loaded" on every host
// whose images are content-addressed — a failure invented entirely by the tag
// changing shape. The repository question has no such hole.
func TestContainerImageSectionWithoutAStorePathAsksByRepository(t *testing.T) {
	got, seen := runContainerImageSection(t, "podman", "", func(argv []string) ExecResult {
		return ExecResult{Ran: true, RC: 0,
			Stdout: "localhost/yolo-jail:deadbeefdeadbeef (3.55 GB)\n"}
	})
	if len(seen) != 1 {
		t.Fatalf("probes = %v, want exactly one", seen)
	}
	argv := strings.Join(seen[0], " ")
	if strings.Contains(argv, ":latest") {
		t.Errorf("the tagless probe still names a TAG (%q); a content-addressed host "+
			"would be reported as having no image at all", argv)
	}
	if !strings.Contains(argv, "podman images localhost/yolo-jail ") {
		t.Errorf("probe = %q, want a repository-filtered `images` query", argv)
	}
	if !strings.Contains(got, "deadbeefdeadbeef") {
		t.Errorf("report does not say WHICH image is loaded:\n%s", got)
	}

	// Nothing loaded → a warn naming the repository, not a phantom tag.
	got, _ = runContainerImageSection(t, "podman", "", func(argv []string) ExecResult {
		return ExecResult{Ran: true, RC: 0, Stdout: "\n"}
	})
	if !strings.Contains(got, "No 'localhost/yolo-jail' image loaded") {
		t.Errorf("empty store not reported by repository:\n%s", got)
	}
}

// TestContainerImageSectionAppleContainerRefs: Apple Container gets the same
// content ref (unqualified — its CLI carries no localhost/ prefix), and falls
// back to the legacy ref only where there is no store path, since this repo has
// no `container images <repo>` filter it can vouch for.
func TestContainerImageSectionAppleContainerRefs(t *testing.T) {
	const storePath = "/nix/store/ac-check-image"
	_, seen := runContainerImageSection(t, "container", storePath,
		func(argv []string) ExecResult { return ExecResult{Ran: true, RC: 0} })
	want := image.JailImageRef("container", storePath)
	if strings.HasPrefix(want, "localhost/") {
		t.Fatalf("apple container ref %q carries a registry prefix", want)
	}
	if len(seen) != 1 || strings.Join(seen[0], " ") != "container image inspect "+want {
		t.Fatalf("probe = %v, want an inspect of %q", seen, want)
	}

	_, seen = runContainerImageSection(t, "container", "",
		func(argv []string) ExecResult { return ExecResult{Ran: true, RC: 0} })
	legacy := image.JailImage("container")
	if len(seen) != 1 || strings.Join(seen[0], " ") != "container image inspect "+legacy {
		t.Fatalf("tagless probe = %v, want the legacy %q", seen, legacy)
	}
}

// TestContainerImageSectionProbeFailureIsNotAMissingImage: a probe that could
// not run says so. Reporting "not loaded" for "could not ask" is the kind of
// confident wrong answer this repo's image work exists to stop.
func TestContainerImageSectionProbeFailureIsNotAMissingImage(t *testing.T) {
	for name, res := range map[string]ExecResult{
		"did not run": {Ran: false},
		"timed out":   {Ran: true, Timeout: true},
	} {
		for _, storePath := range []string{"/nix/store/x-image", ""} {
			got, _ := runContainerImageSection(t, "podman", storePath,
				func([]string) ExecResult { return res })
			if !strings.Contains(got, "probe failed") {
				t.Errorf("%s (storePath=%q): want a probe-failed report, got:\n%s", name, storePath, got)
			}
			if strings.Contains(got, "not loaded") {
				t.Errorf("%s (storePath=%q): an unanswerable probe was reported as a "+
					"missing image:\n%s", name, storePath, got)
			}
		}
	}
}
