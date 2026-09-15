package openaiauthdaemon

import "testing"

func TestMainRefusesRelativeStatePathBeforeCreatingAnything(t *testing.T) {
	t.Chdir(t.TempDir())
	if rc := Main([]string{"--self-check", "--state-file", "{state}/credentials.json"}); rc != 2 {
		t.Fatalf("Main relative state path rc = %d, want usage failure 2", rc)
	}
}
