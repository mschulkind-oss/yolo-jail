package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

var configHelpANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestConfigHelpColorsWithoutChangingItsPlainBytes(t *testing.T) {
	baseline := configUsage + "\n"
	if subcommandUsage["config"].text != configUsage || richtext.Strip(configUsage) != configUsage {
		t.Fatal("registered config usage must remain plain text for raw consumers")
	}

	for _, args := range [][]string{
		{"config"},
		{"config", "--help"},
		{"config", "-h"},
		{"config", "help"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			home, cwd := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Chdir(cwd)
			standInTerminal(t)

			t.Setenv("NO_COLOR", "")
			var rc int
			out, errout := captureBoth(t, func() { rc = runConfig(args) })
			if rc != 0 {
				t.Fatalf("runConfig(%q) = %d, want 0", args, rc)
			}
			if errout != "" {
				t.Errorf("help disclosed or did work on stderr: %q", errout)
			}
			if !strings.Contains(out, "\x1b[1m") || !strings.Contains(out, "\x1b[36m") {
				t.Errorf("terminal help did not color both headings and command tokens:\n%s", out)
			}
			if plain := configHelpANSI.ReplaceAllString(out, ""); plain != baseline {
				t.Errorf("ANSI-stripped terminal output changed plain bytes: got %q, want %q", plain, baseline)
			}
			if got := entries(t, cwd); len(got) != 0 {
				t.Errorf("help created workspace files: %v", got)
			}
			if got := entries(t, filepath.Join(home, ".config")); len(got) != 0 {
				t.Errorf("help created user config state: %v", got)
			}

			t.Setenv("NO_COLOR", "1")
			out, errout = captureBoth(t, func() { rc = runConfig(args) })
			if rc != 0 || errout != "" {
				t.Errorf("NO_COLOR help returned %d, stderr %q", rc, errout)
			}
			if out != baseline {
				t.Errorf("NO_COLOR output = %q, want unchanged plain bytes %q", out, baseline)
			}

			t.Setenv("NO_COLOR", "")
			var piped bytes.Buffer
			var pipeErr bytes.Buffer
			if rc = configRunW(args[1:], &piped, &pipeErr); rc != 0 {
				t.Errorf("piped configRunW returned %d", rc)
			}
			if piped.String() != baseline || pipeErr.Len() != 0 {
				t.Errorf("piped output = %q, stderr = %q; want plain help and no disclosure", piped.String(), pipeErr.String())
			}
		})
	}
}

func TestConfigHelpStylesBothHelpFlagAliases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())
	t.Setenv("NO_COLOR", "")
	standInTerminal(t)

	var rc int
	out, errout := captureBoth(t, func() { rc = runConfig([]string{"config", "--help"}) })
	if rc != 0 || errout != "" {
		t.Fatalf("runConfig(config --help) returned %d, stderr %q", rc, errout)
	}
	if !strings.Contains(out, "\x1b[36m--help, -h\x1b[0m") {
		t.Fatalf("combined --help, -h aliases were not styled as an exact span:\n%s", out)
	}
	if plain := configHelpANSI.ReplaceAllString(out, ""); plain != configUsage+"\n" {
		t.Fatalf("ANSI-stripped help changed plain bytes: got %q, want %q", plain, configUsage+"\n")
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	got, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(got))
	for i := range got {
		names[i] = got[i].Name()
	}
	return names
}
