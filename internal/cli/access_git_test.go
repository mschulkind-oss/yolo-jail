package cli

import (
	"io"
	"strings"
	"testing"
)

func TestRenderGitPreAskDoesNotAlterControlOptionBytes(t *testing.T) {
	body := []byte(`{"verdict":"asks"}`)
	for _, option := range []string{"priority=\x1b[31m", "priority=\u200bhidden", "priority=\xff"} {
		out, _ := accessFiles(t)
		command := []string{"git", "push", "-o", option, "origin", "HEAD:refs/heads/main"}
		renderGitPreAsk(out, body, command)
		if _, err := out.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		text, err := io.ReadAll(out)
		if err != nil || !strings.Contains(string(text), "exact pre-ask command unavailable") || strings.Contains(string(text), "file this exact request") || strings.Contains(string(text), option) {
			t.Fatalf("control option %q: %q %v", option, text, err)
		}
	}
	out, _ := accessFiles(t)
	command := []string{"git", "push", "-o", "priority=a'b", "origin", "HEAD:refs/heads/main"}
	renderGitPreAsk(out, body, command)
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(out)
	if err != nil || !strings.Contains(string(text), "file this exact request without pushing") || !strings.Contains(string(text), `priority=a'"'"'b`) {
		t.Fatalf("ordinary option no longer has exact shell advice: %q %v", text, err)
	}
}

func TestRenderAccessGitPredictionSeparatesCommandFromAdvice(t *testing.T) {
	out, stderr := accessFiles(t)
	wire := []byte(`{"schema":"yolo.access.prediction/1","call":{"source":"git","canonical":["git-receive-pack","old new refs/heads/main"],"digest":"command"},"classified":[{"ref":"refs/heads/main","covered_by":"w-1"}],"verdict":"runs","rule":"covered by host","covered_by":"w-1","inputs_digest":"inputs","next":[{"who":"agent","where":"jail","detail":"Retry this exact push after approval."}]}`)
	renderAccessPrediction(out, wire)
	if _, err := out.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	text, err := io.ReadAll(out)
	if err != nil || !strings.Contains(string(text), "git push (broker-selected refs) → runs") || !strings.Contains(string(text), "ref refs/heads/main: covered by w-1") || !strings.Contains(string(text), "next (agent, jail): Retry this exact push") || strings.Contains(string(text), "git-receive-pack old") {
		t.Fatalf("text %q stderr=%v err=%v", text, stderr, err)
	}
}
