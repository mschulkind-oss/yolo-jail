package integration

import (
	"errors"
	gobuild "go/build"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
	"github.com/mschulkind-oss/yolo-jail/internal/sigv4"
)

// requestIDInRefusal is the request id a call-broker ask names, without the backtick that
// closes the quoted command around it.
var requestIDInRefusal = regexp.MustCompile("yolo approve (r-[0-9a-f]+)")

// TestAWSS3BrokerMediatesReadsAndAsksForWrites runs the shipped aws-s3 pack in a real (nested)
// jail: the jail's `aws` is intercepted, the production classifier decides, and a fake host
// `aws` (the s3fixture binary installed under that name) talks to an in-process fake S3
// service on 127.0.0.1. No AWS account, credential or endpoint is involved: every value is
// the fixture's synthetic constant.
//
// It covers a read that runs, a write that asks and changes nothing, the host's
// `yolo approve` after which the same write lands, and a refusal that never reaches the
// service. The in-process twin is internal/callbroker/s3_e2e_test.go.
func TestAWSS3BrokerMediatesReadsAndAsksForWrites(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("the aws-s3 broker smoke needs podman, runtime is %q", rt)
	}
	goTool := filepath.Join(gobuild.Default.GOROOT, "bin", "go")
	if _, err := os.Stat(goTool); err != nil {
		if goTool, err = exec.LookPath("go"); err != nil {
			t.Fatalf("locate the Go compiler: %v", err)
		}
	}
	bin := t.TempDir()
	build := exec.Command(goTool, "build", "-o", filepath.Join(bin, "aws"), "../internal/s3fixture/cmd/s3fixture")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fake host aws: %v\n%s", err, out)
	}

	server := s3fixture.NewServer(s3fixture.Options{
		Credentials: sigv4.Credentials{AccessKeyID: s3fixture.AccessKeyID, SecretAccessKey: s3fixture.SecretAccessKey},
		Region:      s3fixture.Region,
		Buckets: map[string]map[string][]byte{
			"bkt":       {"team/a.txt": []byte("alpha from the fixture\n")},
			"other-bkt": {"x": []byte("another bucket\n")},
		},
	})
	s3fixture.MustStart(t, server)
	t.Cleanup(server.Close)

	// The user ceiling: reads always; the put cell may be asked for (never run unasked).
	packHome(t, `{"packs":["aws-s3"],"allow":{"aws-s3":{"always":["read"],"on_request":["all.put_objects"]}}}`)
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"AWS_ENDPOINT_URL_S3=" + server.URL(),
		"AWS_ACCESS_KEY_ID=" + s3fixture.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY=" + s3fixture.SecretAccessKey,
		"AWS_REGION=" + s3fixture.Region,
		"DBUS_SESSION_BUS_ADDRESS=",
		"XDG_CONFIG_HOME=" + filepath.Join(os.Getenv("HOME"), ".config"),
		"YOLO_NO_AUTO_IMAGE_REAP=1",
	}
	opts := []runOption{withEnv(env...)}
	workspace := writeProject(t, `{"brokered":{"aws-s3":{"roots":["s3://bkt/team/"]}}}`)
	hostGit(t, workspace, "init", "-q", "-b", "main")

	checked := runCommand(t, workspace, []string{"check", "--no-build", config.AcceptConfigChangesFlag}, opts...)
	if checked.rc != 0 {
		t.Fatalf("yolo check did not approve the aws-s3 roots (rc=%d):\n%s", checked.rc, checked.combined())
	}

	refusal, marker := filepath.Join(workspace, ".put-refusal"), filepath.Join(workspace, ".approved")

	// The host half, which may not fail the test from its goroutine: it waits for the jail's
	// refusal, approves the request it names with the host's `yolo approve`, and reports.
	type approval struct {
		id, out string
		err     error
	}
	approved := make(chan approval, 1)
	go func() {
		var a approval
		defer func() { approved <- a }()
		deadline := time.Now().Add(4 * time.Minute)
		for ; time.Now().Before(deadline); time.Sleep(time.Second) {
			b, _ := os.ReadFile(refusal)
			if m := requestIDInRefusal.FindSubmatch(b); m != nil {
				a.id = string(m[1])
				break
			}
		}
		if a.id == "" {
			a.err = errors.New("the jail never wrote a refusal naming a request")
			return
		}
		cfg := runConfig{}
		for _, o := range append(append([]runOption{}, opts...), withHostSemantics()) {
			o(&cfg)
		}
		name, argv := launchCommand(cfg, yoloBin, []string{"approve", a.id})
		cmd := exec.Command(name, argv...)
		cmd.Dir, cmd.Env = workspace, launchEnvironment(cfg)
		out, err := cmd.CombinedOutput()
		a.out, a.err = string(out), err
		if werr := os.WriteFile(marker, []byte("ok\n"), 0o644); a.err == nil {
			a.err = werr
		}
	}()

	put := "printf 'hello\\n' | aws s3 cp - s3://bkt/team/new.txt"
	script := ghScript(
		"aws s3 ls s3://bkt/team/",        // A: a read runs
		"aws s3 cp s3://bkt/team/a.txt -", // B: a read of one object
		"("+put+" > .put-out 2>&1; rc=$?; mv .put-out .put-refusal; exit $rc)",               // C: asks, refused
		"for i in $(seq 240); do [ -f .approved ] && break; sleep 1; done; [ -f .approved ]", // D: the host answers
		put,                          // E: the same put lands
		"aws ec2 describe-instances", // F: another service is refused
		"aws s3 ls s3://bkt/",        // G: outside the declared root
	)
	r := runCommand(t, workspace, []string{"run", config.AcceptConfigChangesFlag, "--", "bash", "-lc", script}, opts...)
	a := <-approved
	refused, _ := os.ReadFile(refusal)
	t.Logf("launch: rc %d\n%s\nfirst put:\n%s\nyolo approve %s: %v\n%s", r.rc, r.combined(), refused, a.id, a.err, a.out)
	if a.err != nil {
		t.Fatalf("the host's approval failed: %v\n%s", a.err, a.out)
	}

	for k, want := range map[string]string{"RCA": "0", "RCB": "0", "RCC": "75", "RCD": "0", "RCE": "0", "RCF": "64", "RCG": "64"} {
		if got := kvLine(r.stdout, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if !strings.Contains(r.stdout, "a.txt") || !strings.Contains(r.stdout, "alpha from the fixture") {
		t.Errorf("the reads did not return the fixture's data:\n%s", r.combined())
	}
	if !strings.Contains(string(refused), "yolo approve "+a.id) {
		t.Errorf("the put's refusal does not name `yolo approve %s`:\n%s", a.id, refused)
	}
	if got, ok := server.Object("bkt", "team/new.txt"); !ok || string(got) != "hello\n" {
		t.Errorf("the approved put did not land with the exact stdin: %q ok=%v", got, ok)
	}
	if n := server.Writes(); n != 1 {
		t.Errorf("the fixture saw %d writes, want exactly the one approved put", n)
	}
	// The two refusals must never have reached the service: every logged request names the
	// declared bucket and prefix.
	for _, req := range server.RequestLog() {
		if strings.HasPrefix(req.Path, "/other-bkt") || (req.Method == "GET" && req.Path == "/bkt" && !strings.Contains(req.Query, "prefix=team")) {
			t.Errorf("a refused call reached the fixture: %+v", req)
		}
	}
	for _, out := range []string{r.combined(), string(refused), a.out} {
		if strings.Contains(out, s3fixture.SecretAccessKey) {
			t.Errorf("a jail-visible or approval output contains the synthetic secret: %q", out)
		}
	}
	if t.Failed() {
		t.Fatalf("output above")
	}
}
