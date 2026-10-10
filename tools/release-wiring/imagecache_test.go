package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The image-cache publication is a trust boundary: the build job names the
// exact store paths the credentialed job pushes. imageCopier is skopeo, whose
// installed outputs are out and man, so a launch's `nix build .#imageCopier`
// realizes four paths across the three attrs, not three. v0.13.0's cache jobs
// failed on that count. These tests run the production step text against a
// fake nix that prints one path per requested output, as nix does.

const fakeImageCacheNix = `#!/bin/bash
case "$1" in
  build)
    for arg in "$@"; do
      case "$arg" in
        .#*)
          installable=${arg#.#}
          attr=${installable%%^*}
          # A bare installable builds meta.outputsToInstall: skopeo's is out,man.
          outputs=out
          [ "$attr" != imageCopier ] || outputs=out,man
          [ "$installable" = "$attr" ] || outputs=${installable#*^}
          echo "nix build $installable" >> "$TRACE"
          IFS=, read -ra outs <<< "$outputs"
          for out in "${outs[@]}"; do
            echo "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-$attr-$out"
          done
          if [ "${EXTRA_OUTPUT:-}" = "$attr" ]; then
            echo "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-$attr-extra"
          fi
          ;;
      esac
    done
    ;;
  copy)
    echo "nix $*" >> "$TRACE"
    case "$*" in
      *--to\ file://*) dest=${3#file://}; mkdir -p "$dest"; echo fixture > "$dest/nix-cache-info" ;;
    esac
    ;;
esac
`

func imageCacheStep(t *testing.T, job, name string) workflowStep {
	t.Helper()
	wf := readWorkflow(t, ".github/workflows/publish.yml")
	for _, step := range wf.Jobs[job].Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("publish.yml %s lost its step %q", job, name)
	return workflowStep{}
}

func runImageCacheBuild(t *testing.T, extra ...string) (string, string, string, error) {
	t.Helper()
	step := imageCacheStep(t, "build-image-cache", "Build the target closures without the Cachix credential")
	root, runnerTemp := t.TempDir(), t.TempDir()
	bin, trace := fakeCommands(t)
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(fakeImageCacheNix), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runWorkflowCommand(t, step, root, bin, trace, append([]string{"RUNNER_TEMP=" + runnerTemp}, extra...)...)
	return root, bin, trace, withOutput(err, out)
}

type outputError struct {
	err error
	out string
}

func (e outputError) Error() string { return e.err.Error() + ": " + e.out }

func withOutput(err error, out string) error {
	if err == nil {
		return nil
	}
	return outputError{err, out}
}

func TestImageCacheBuildUploadsExactlyTheImagesAndBothCopierOutputs(t *testing.T) {
	root, bin, trace, err := runImageCacheBuild(t)
	if err != nil {
		t.Fatalf("the cache build refused the flake's real output set: %v", err)
	}
	got := strings.Fields(readFile(t, filepath.Join(root, "nix-cache/store-paths.txt")))
	want := []string{"ociImage-out", "ociImageMinimal-out", "imageCopier-out", "imageCopier-man"}
	for i, w := range want {
		want[i] = "/nix/store/" + strings.Repeat("a", 32) + "-" + w
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("store-paths.txt = %v, want exactly %v", got, want)
	}
	lines := readTrace(t, trace)
	copyAt := traceIndex(lines, "nix copy --to file://")
	if copyAt < 0 {
		t.Fatalf("the validated paths were never copied: %v", lines)
	}
	for _, w := range want {
		if !strings.Contains(lines[copyAt], w) {
			t.Errorf("nix copy omitted %s: %s", w, lines[copyAt])
		}
	}

	// The credentialed job re-validates the artifact's count, so it must
	// accept exactly what the build job produced.
	publish := imageCacheStep(t, "publish-image-cache", "Import inert Nix paths and push the validated closure names")
	if err := os.WriteFile(filepath.Join(bin, "cachix"), []byte("#!/bin/sh\necho \"cachix $*\" >> \"$TRACE\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runWorkflowCommand(t, publish, root, bin, trace, "GITHUB_WORKSPACE="+root, "CACHE_NAME=fixture-cache")
	if err != nil {
		t.Fatalf("publish-image-cache refused the build job's own artifact: %v %s", err, out)
	}
	pushed := readTrace(t, trace)
	pushAt := traceIndex(pushed, "cachix push fixture-cache")
	if pushAt < 0 {
		t.Fatalf("nothing was pushed: %v", pushed)
	}
	for _, w := range want {
		if !strings.Contains(pushed[pushAt], w) {
			t.Errorf("cachix push omitted %s: %s", w, pushed[pushAt])
		}
	}
}

func TestImageCacheBuildRefusesAnUnexpectedOutputBeforeAnyCopy(t *testing.T) {
	for _, attr := range []string{"ociImage", "ociImageMinimal", "imageCopier"} {
		t.Run(attr, func(t *testing.T) {
			_, _, trace, err := runImageCacheBuild(t, "EXTRA_OUTPUT="+attr)
			if err == nil {
				t.Fatalf("an extra %s output path was accepted for upload", attr)
			}
			if !strings.Contains(err.Error(), ".#"+attr) || !strings.Contains(err.Error(), "update it") {
				t.Fatalf("refusal does not name the installable and the next step: %v", err)
			}
			if lines := readTrace(t, trace); traceIndex(lines, "nix copy") >= 0 {
				t.Fatalf("paths were copied despite the refusal: %v", lines)
			}
		})
	}
}
