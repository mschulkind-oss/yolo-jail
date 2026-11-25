package image

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// justLoadBody is the `load` recipe's shebang body from the repository's Justfile, with its
// recipe indent removed and `{{ runtime }}` rendered as rt, the way just would run it.
func justLoadBody(t *testing.T, rt string) string {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "Justfile"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "\nload: ")
	if start < 0 {
		t.Fatal("the Justfile has no `load` recipe")
	}
	var body []string
	for i, line := range strings.Split(string(src[start+1:]), "\n") {
		if i == 0 {
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			break
		}
		body = append(body, strings.ReplaceAll(strings.TrimPrefix(line, "    "), "{{ runtime }}", rt))
	}
	return strings.Join(body, "\n") + "\n"
}

// `just load` TAKES THE LAUNCH'S BRANCH. Only podman on Linux copies into containers-storage
// through `yolo internal image-copy`; podman on a Mac and Apple Container take the archive and
// the loader ImageLoadCmd names. Podman on a Mac used to reach image-copy, which refuses there,
// so the stale-image refusal's own fix command failed (lab report 2026-10-24, section 4a). The
// recipe runs here against stand-ins for uname, the copier, the loaders and go, which record
// what they were asked to do.
func TestJustLoadTakesTheLaunchsDeliveryBranch(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cases := []struct {
		rt, uname string
		want      []string // the recorded calls, in order, by their first words
	}{
		{"podman", "Linux", []string{"go run ./cmd/yolo internal image-copy --runtime podman"}},
		{"podman", "Darwin", []string{"skopeo --insecure-policy copy", "podman load -i"}},
		{"container", "Darwin", []string{"skopeo --insecure-policy copy", "container image load -i"}},
	}
	for _, c := range cases {
		t.Run(c.rt+"-"+c.uname, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "fakebin")
			log := filepath.Join(dir, "calls")
			record := "#!/bin/sh\necho \"$(basename \"$0\") $*\" >> " + shquote.Quote(log) + "\n"
			for _, name := range []string{"podman", "container", "go"} {
				writeExec(t, filepath.Join(bin, name), record)
			}
			writeExec(t, filepath.Join(bin, "uname"), "#!/bin/sh\necho "+c.uname+"\n")
			writeExec(t, filepath.Join(dir, "result-1", "bin", "skopeo"), record)
			if err := os.WriteFile(filepath.Join(dir, "image.json"), []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(dir, "image.json"), filepath.Join(dir, "result")); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(dir, "load.sh")
			if err := os.WriteFile(script, []byte(justLoadBody(t, c.rt)), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the recipe failed: %v\n%s", err, out)
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(calls) != len(c.want) {
				t.Fatalf("calls %q, want %d starting %q", calls, len(c.want), c.want)
			}
			for i, w := range c.want {
				if !strings.HasPrefix(calls[i], w) {
					t.Errorf("call %d is %q, want it to start %q", i, calls[i], w)
				}
			}
		})
	}
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
