package integration

import (
	"bufio"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestAWSBrokerCostExplorerRequiresItsOwnRoot(t *testing.T) {
	fx := newS3BrokerJail(t, `{"packs":["aws"],"allow":{"aws":{"always":["read"]}}}`)
	if err := os.Remove(filepath.Join(fx.workspace, "yolo-jail.jsonc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.workspace, "yolo-jail.toml"), []byte("[call_roots]\naws=[\"s3://bkt/team/\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Keep the SAME jail/broker alive while config apply publishes and withdraws roots.
	// FIFOs signal actual readiness and release; no timed/polling pass condition.
	fifo := func(name string) *os.File {
		t.Helper()
		p := filepath.Join(fx.workspace, name)
		if err := syscall.Mkfifo(p, 0600); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close(); _ = os.Remove(p) })
		return f
	}
	ready, release := fifo("aws-ready"), fifo("aws-release")
	readyLine := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(ready).ReadString('\n'); readyLine <- line }()
	cfg := runConfig{}
	for _, o := range fx.opts {
		o(&cfg)
	}
	holder := startYoloBackground(t, "aws-ce-holder", fx.workspace, `printf 'ready\n' > /workspace/aws-ready; read -r _ < /workspace/aws-release`, cfg.env...)
	t.Cleanup(func() { _, _ = release.WriteString("release\n") })
	select {
	case line := <-readyLine:
		if line != "ready\n" {
			t.Fatalf("holder ready line=%q", line)
		}
	case <-holder.exited:
		t.Fatalf("holder exited before readiness: %s", holder.combined())
	case <-time.After(jailTimeout()):
		t.Fatalf("holder never signaled readiness: %s", holder.combined())
	}
	args := append(append(jailRunArgs(), "--"), "aws", "ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01")
	query := func() result { return runCommand(t, fx.workspace, args, fx.opts...) }
	before := len(fx.server.RequestLog())
	refused := query()
	if refused.rc != 64 || !strings.Contains(refused.combined(), "ce://host-login") || len(fx.server.RequestLog()) != before || len(fx.server.CEArgv()) != 0 {
		t.Fatalf("S3 root admitted CE: rc=%d\n%s", refused.rc, refused.combined())
	}
	apply := func(roots string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(fx.workspace, "yolo-jail.toml"), []byte("[call_roots]\naws="+roots+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		opts := append(append([]runOption{}, fx.opts...), withHostSemantics())
		checked := runCommand(t, fx.workspace, []string{"config", "check"}, opts...)
		if checked.rc != 0 {
			t.Fatalf("synthetic config check rc=%d\n%s", checked.rc, checked.combined())
		}
		applied := runCommand(t, fx.workspace, []string{"config", "apply", config.AcceptConfigChangesFlag}, opts...)
		if applied.rc != 0 {
			t.Fatalf("synthetic config apply rc=%d\n%s", applied.rc, applied.combined())
		}
	}
	apply(`["s3://bkt/team/","ce://host-login"]`)
	allowed := query()
	if allowed.rc != 0 || !strings.Contains(allowed.stdout, "{\"Tags\":[\"fixture\"]}") || len(fx.server.RequestLog()) != before+1 {
		t.Fatalf("approved CE root did not execute once: rc=%d\n%s", allowed.rc, allowed.combined())
	}
	want := [][]string{{"ce", "get-tags", "--time-period", "Start=2026-01-01,End=2026-02-01", "--output", "json", "--no-paginate"}}
	if !reflect.DeepEqual(fx.server.CEArgv(), want) {
		t.Fatalf("canonical host argv=%q want=%q", fx.server.CEArgv(), want)
	}
	apply(`["s3://bkt/team/"]`)
	withdrawn := query()
	if withdrawn.rc != 64 || !strings.Contains(withdrawn.combined(), "ce://host-login") || len(fx.server.CEArgv()) != 1 || len(fx.server.RequestLog()) != before+1 {
		t.Fatalf("withdrawn CE root executed: rc=%d\n%s", withdrawn.rc, withdrawn.combined())
	}
	_, _ = release.WriteString("release\n")
	select {
	case <-holder.exited:
	case <-time.After(jailTimeout()):
		t.Fatal("holder did not exit on release")
	}
}
