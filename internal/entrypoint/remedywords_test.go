package entrypoint

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// offeredCommands returns the words of every command msg OFFERS — each indented line whose
// first word is one of names — as a shell splits them for the reader who pastes the line.
//
// THE SHELL SPLITS, NOT THIS HELPER. A path with a space in it is one word only if the message
// quoted it, and only a shell can say whether it did: a test that cut the line on spaces, or
// compared it with the unquoted path, would pass a remedy that removes the path's first word
// and then its second. Each name is defined as a shell function that prints its own argv, so
// a line is parsed and nothing it names is run.
func offeredCommands(t *testing.T, msg string, names ...string) [][]string {
	t.Helper()
	var defs strings.Builder
	for _, n := range names {
		defs.WriteString(n + "() { printf '%s\\0' " + n + " \"$@\"; }\n")
	}
	var out [][]string
	for _, line := range strings.Split(msg, "\n") {
		trimmed := strings.TrimSpace(line)
		if line == strings.TrimLeft(line, " \t") || trimmed == "" {
			continue // not an indented command line
		}
		if !slices.Contains(names, strings.Fields(trimmed)[0]) {
			continue
		}
		cmd := exec.Command("/bin/sh", "-c", defs.String()+trimmed)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("the offered command %q is not one a shell can parse: %v\n%s", trimmed, err, stderr.String())
		}
		out = append(out, strings.Split(strings.TrimSuffix(string(got), "\x00"), "\x00"))
	}
	return out
}

// offers reports whether msg offers exactly the command want, word for word.
func offers(t *testing.T, msg string, want ...string) bool {
	t.Helper()
	return slices.ContainsFunc(offeredCommands(t, msg, want[0]), func(got []string) bool {
		return slices.Equal(got, want)
	})
}

// A REMEDY IS PASTED INTO A SHELL, so every path it names has to come back out of that shell
// as ONE word. The paths here sit under "Application Support", the shape a macOS home takes
// as soon as anything lives under ~/Library, and a workspace under "My Projects" is the same
// shape: printed bare, `sudo rm -rf <sidecar mirror>` removed `…/Application` and then
// `Support/…` relative to wherever the reader stood, and `sudo rm <link>` never reached the
// link at all.
//
// Each row is one refusal the macos-user layout and overlay print, built at the function that
// words it; the call-site tests (darwinhomelayout_test.go, darwinoverlay_test.go,
// darwinoverlaylinks_test.go, gitconfiglink_test.go) reach the same refusals through Apply,
// the overlay install and the bootstrap, and read their remedies through offeredCommands too.
func TestEveryLayoutRemedyNamesItsPathAsOneShellWord(t *testing.T) {
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(resolved, "Application Support")
	home := filepath.Join(base, "home")
	ws := filepath.Join(base, "My Projects", "thing")
	sidecar := filepath.Join(ws, ".yolo", "home")
	elsewhere := filepath.Join(base, "elsewhere")
	for _, d := range []string{home, sidecar, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name   string
		refuse func(t *testing.T) string
		want   []string
	}{
		{
			name: "a link in the account home that this launch did not lay",
			refuse: func(t *testing.T) string {
				link := filepath.Join(home, ".claude")
				symlinkAt(t, link, elsewhere)
				_, err := overlayLinks{follow: map[string]string{}}.route(home, ".claude/skills")
				return errString(t, err)
			},
			want: []string{"sudo", "rm", filepath.Join(home, ".claude")},
		},
		{
			name: "a link in the sidecar past the layout's own",
			refuse: func(t *testing.T) string {
				layoutLink := filepath.Join(home, ".pi")
				symlinkAt(t, layoutLink, filepath.Join(sidecar, "pi"))
				symlinkAt(t, filepath.Join(sidecar, "pi", "agent"), elsewhere)
				l := overlayLinks{follow: map[string]string{layoutLink: filepath.Join(sidecar, "pi")}}
				_, err := l.route(home, ".pi/agent/AGENTS.md")
				return errString(t, err)
			},
			want: []string{"sudo", "rm", filepath.Join(sidecar, "pi", "agent")},
		},
		{
			name: "an occupied account home",
			refuse: func(t *testing.T) string {
				return errString(t, occupiedLayoutError(home, []string{filepath.Join(home, ".claude")}, nil))
			},
			want: []string{"sudo", "rm", "-rf", home},
		},
		{
			name: "an occupied mirror in the workspace sidecar",
			refuse: func(t *testing.T) string {
				mirror := DarwinHomeLink{Path: filepath.Join(sidecar, "local"), Target: elsewhere}
				return errString(t, occupiedLayoutError(home, nil, []DarwinHomeLink{mirror}))
			},
			want: []string{"sudo", "rm", "-rf", filepath.Join(sidecar, "local")},
		},
		{
			name: "a link where the layout lays a sidecar directory",
			refuse: func(t *testing.T) string {
				return errString(t, &LinkedSidecarError{Links: []string{filepath.Join(ws, ".yolo")}})
			},
			want: []string{"sudo", "rm", filepath.Join(ws, ".yolo")},
		},
		{
			name: "a link on the way to a home file yolo writes outside the sandbox",
			refuse: func(t *testing.T) string {
				link := filepath.Join(home, ".config")
				symlinkAt(t, link, elsewhere)
				_, err := DarwinHomeLayout{Home: home}.homeFileThroughLayout(".config/git/config")
				return errString(t, err)
			},
			want: []string{"sudo", "rm", filepath.Join(home, ".config")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.refuse(t)
			if !offers(t, msg, tc.want...) {
				t.Errorf("the refusal does not offer %q as a shell reads it; it offers %q:\n%s",
					tc.want, offeredCommands(t, msg, tc.want[0]), msg)
			}
		})
	}
}

func errString(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("no refusal")
	}
	return err.Error()
}
