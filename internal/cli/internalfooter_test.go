package cli

import (
	"io"
	"os"
	"testing"
)

// TestInternalFooterIsRegistered pins the wiring every agent pack's status-line command
// depends on: `yolo internal footer …` through the real front door (Main), with the agent's
// JSON on stdin and the facts from the env, printing the one line on stdout and nothing on
// stderr. Deleting the `footer` arm of runInternal — or handing footer.Main anything but the
// process's own stdin and stdout — fails it.
func TestInternalFooterIsRegistered(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.10.0")
	t.Setenv("YOLO_USE_PROFILES", `{"claude": "bedrock"}`)
	t.Setenv("YOLO_PROFILES", `{"bedrock": {"provider": "bedrock"}}`)
	t.Setenv("YOLO_PROVIDERS", `{"bedrock": {"region": "us-east-1"}}`)

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inW.WriteString(`{"model": {"display_name": "Opus"}}`); err != nil {
		t.Fatal(err)
	}
	inW.Close()
	savedIn, savedOut, savedErr := os.Stdin, os.Stdout, os.Stderr
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	code := Main([]string{"yolo", "internal", "footer",
		"--agent", "claude", "--login", "Claude subscription", "--words", "bedrock=Bedrock",
		"--template", "{stdin.model.display_name} · yolo: {yolo.billing} · {yolo.notch}"})
	os.Stdin, os.Stdout, os.Stderr = savedIn, savedOut, savedErr
	outW.Close()
	errW.Close()
	stdout, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)

	if code != 0 {
		t.Errorf("yolo internal footer exited %d, want 0", code)
	}
	if want := "Opus · yolo: Bedrock · jail\n"; string(stdout) != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want nothing: an agent shows a status command's noise", stderr)
	}
}
