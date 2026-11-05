package check

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestCheckSelectedCircleCIPackReportsHostCLIWithoutProbingLogin(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cliFound bool
		want     []string
		avoid    []string
	}{
		{
			name: "missing CLI has host next step", cliFound: false,
			want: []string{"circleci: host CLI not found", "Install the CircleCI CLI on the host", "circleci auth login", "yolo check"},
		},
		{
			name: "present CLI does not attest login", cliFound: true,
			want:  []string{"circleci: host CLI found at /fixture/bin/circleci", "login status is not checked", "circleci auth login"},
			avoid: []string{"logged in", "authenticated", "credential found"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			writeCheckFile(t, paths.UserConfigPath(), `{"packs":["circleci"]}`, 0o600)
			var out bytes.Buffer
			r := newReporter(&out, false)
			o := &Options{
				Getenv: func(string) string { return "" },
				LookPath: func(name string) (string, bool) {
					return "/fixture/bin/" + name, name == "circleci" && tc.cliFound
				},
				Exec: func([]string, string, []string, time.Duration) ExecResult {
					t.Fatal("yolo check must not run auth, help, version, or service probes for CircleCI")
					return ExecResult{}
				},
			}
			o.sectionPacks(r, jsonx.NewOrderedMap())
			got := stripANSI(out.String())
			for _, text := range tc.want {
				if !strings.Contains(got, text) {
					t.Errorf("check output lacks %q:\n%s", text, got)
				}
			}
			for _, text := range tc.avoid {
				if strings.Contains(strings.ToLower(got), text) {
					t.Errorf("check output falsely attests %q:\n%s", text, got)
				}
			}
		})
	}
}
