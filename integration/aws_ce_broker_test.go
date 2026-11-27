package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
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
	// Readiness is a marker the session prints (awaitOutput); release is a regular file the
	// session polls. Not FIFOs: a FIFO does not cross the macOS VM's workspace share.
	cfg := runConfig{}
	for _, o := range fx.opts {
		o(&cfg)
	}
	const release = "aws-release"
	holder := startYoloBackground(t, "aws-ce-holder", fx.workspace,
		`printf '\nAWS-HOLDER-READY-%s\n' $((40+2)); `+
			`for _ in $(seq 1 2400); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`, cfg.env...)
	releaseHolder := func() {
		_ = os.WriteFile(filepath.Join(fx.workspace, release), []byte("release\n"), 0o644)
	}
	t.Cleanup(releaseHolder)
	// The launch echoes the command line, so the marker is computed in the session.
	awaitOutput(t, holder, regexp.MustCompile("AWS-HOLDER-READY-42"))
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
	releaseHolder()
	select {
	case <-holder.exited:
	case <-time.After(jailTimeout()):
		t.Fatal("holder did not exit on release")
	}
}
