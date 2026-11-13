package integration

import (
	"errors"
	"fmt"
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

// s3BrokerJail is a workspace selecting the shipped aws-s3 pack against an in-process fake S3
// service on 127.0.0.1, with a fake host `aws` (the s3fixture binary installed under that name)
// first on the host PATH. No AWS account, credential or endpoint is involved: every value is
// the fixture's synthetic constant. Its roots are `s3://bkt/team/`, approved by a host
// `yolo check`.
type s3BrokerJail struct {
	server    *s3fixture.Server
	workspace string
	opts      []runOption
}

func newS3BrokerJail(t *testing.T, userConfig string) *s3BrokerJail {
	t.Helper()
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

	packHome(t, userConfig)
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
	return &s3BrokerJail{server: server, workspace: workspace, opts: opts}
}

// hostYolo runs the host's yolo in the workspace. It never fails the test, so the host half of
// a test may call it from its goroutine.
func (fx *s3BrokerJail) hostYolo(args ...string) (string, error) {
	cfg := runConfig{}
	for _, o := range append(append([]runOption{}, fx.opts...), withHostSemantics()) {
		o(&cfg)
	}
	name, argv := launchCommand(cfg, yoloBin, args)
	cmd := exec.Command(name, argv...)
	cmd.Dir, cmd.Env = fx.workspace, launchEnvironment(cfg)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// awaitRequestID waits for the jail to write the refusal file at path naming a request, and
// returns its id. The deadline is only a failure guard for a jail that never asks.
func awaitRequestID(path string) (string, error) {
	deadline := time.Now().Add(4 * time.Minute)
	for ; time.Now().Before(deadline); time.Sleep(time.Second) {
		b, _ := os.ReadFile(path)
		if m := requestIDInRefusal.FindSubmatch(b); m != nil {
			return string(m[1]), nil
		}
	}
	return "", fmt.Errorf("the jail never wrote %s naming a request", filepath.Base(path))
}

// askedAndRecorded is a jail-side script step: it runs cmd, keeps its output in file (moved
// into place whole, so the host never reads a partial one) and exits with cmd's status.
func askedAndRecorded(cmd, file string) string {
	return "(" + cmd + " > " + file + ".tmp 2>&1; rc=$?; mv " + file + ".tmp " + file + "; exit $rc)"
}

// awaitMarker is a jail-side script step: it waits for the host half's marker file. The bound
// is only a failure guard.
func awaitMarker(file string) string {
	return "for i in $(seq 240); do [ -f " + file + " ] && break; sleep 1; done; [ -f " + file + " ]"
}

// TestAWSS3BrokerMediatesReadsAndAsksForWrites runs the shipped aws-s3 pack in a real (nested)
// jail: the jail's `aws` is intercepted, the production classifier decides, and the fake host
// `aws` talks to the fake S3 service.
//
// It covers a read that runs, a write that asks and changes nothing, the host's
// `yolo approve` after which the same write lands, and a refusal that never reaches the
// service. The in-process twin is internal/callbroker/s3_e2e_test.go.
func TestAWSS3BrokerMediatesReadsAndAsksForWrites(t *testing.T) {
	// The user ceiling: reads always; the put cell may be asked for (never run unasked).
	fx := newS3BrokerJail(t, `{"packs":["aws-s3"],"allow":{"aws-s3":{"always":["read"],"on_request":["all.put_objects"]}}}`)
	server, workspace, opts := fx.server, fx.workspace, fx.opts
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
		if a.id, a.err = awaitRequestID(refusal); a.err != nil {
			return
		}
		a.out, a.err = fx.hostYolo("approve", a.id)
		if werr := os.WriteFile(marker, []byte("ok\n"), 0o644); a.err == nil {
			a.err = werr
		}
	}()

	put := "printf 'hello\\n' | aws s3 cp - s3://bkt/team/new.txt"
	script := ghScript(
		"aws s3 ls s3://bkt/team/",            // A: a read runs
		"aws s3 cp s3://bkt/team/a.txt -",     // B: a read of one object
		askedAndRecorded(put, ".put-refusal"), // C: asks, refused
		awaitMarker(".approved"),              // D: the host answers
		put,                                   // E: the same put lands
		"aws ec2 describe-instances",          // F: another service is refused
		"aws s3 ls s3://bkt/",                 // G: outside the declared root
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

// TestAWSS3BrokerTimedAnswerIsCappedAndCoversItsResource is the nested conformance of a timed
// pack answer (pack-brokers.md BX-D17, BX-D19) through the shipped aws-s3 pack: the jail's put
// and rm ask; the host's `yolo approve <id> --for <duration>` is refused above the cell's
// max_for (1h for put_objects, the 15-minute default for delete_objects, which declares none)
// and stores nothing; a shorter one is accepted. The timed answer then covers its cell on its
// object for the window, more than once and for a different body, while a put to another
// object still asks and never reaches the service.
func TestAWSS3BrokerTimedAnswerIsCappedAndCoversItsResource(t *testing.T) {
	fx := newS3BrokerJail(t, `{"packs":["aws-s3"],"allow":{"aws-s3":{"always":["read"],"on_request":["all.put_objects","all.delete_objects"]}}}`)
	server, workspace := fx.server, fx.workspace

	// One host-side answer: the over-cap --for, which must be refused naming the cap, then the
	// accepted one; the marker releases the jail either way.
	type answer struct {
		id, over, ok string
		overErr, err error
	}
	answerTimed := func(refusal, marker, overFor, okFor string) answer {
		var a answer
		defer func() {
			if werr := os.WriteFile(filepath.Join(workspace, marker), []byte("ok\n"), 0o644); a.err == nil && werr != nil {
				a.err = werr
			}
		}()
		if a.id, a.err = awaitRequestID(filepath.Join(workspace, refusal)); a.err != nil {
			return a
		}
		a.over, a.overErr = fx.hostYolo("approve", a.id, "--for", overFor)
		a.ok, a.err = fx.hostYolo("approve", a.id, "--for", okFor)
		return a
	}
	answers := make(chan [2]answer, 1)
	go func() {
		put := answerTimed(".put-refusal", ".put-answered", "2h", "30m")
		rm := answerTimed(".rm-refusal", ".rm-answered", "30m", "10m")
		answers <- [2]answer{put, rm}
	}()

	put := func(body, key string) string {
		return "printf '" + body + "\\n' | aws s3 cp - s3://bkt/team/" + key
	}
	rm := "aws s3 rm s3://bkt/team/a.txt"
	script := ghScript(
		askedAndRecorded(put("one", "new.txt"), ".put-refusal"),     // A: the put asks
		awaitMarker(".put-answered"),                                // B: the host answers --for
		put("one", "new.txt"),                                       // C: the same put runs
		put("two", "new.txt"),                                       // D: again, another body: still covered
		askedAndRecorded(put("other", "other.txt"), ".other-asked"), // E: another object asks
		askedAndRecorded(rm, ".rm-refusal"),                         // F: the rm asks
		awaitMarker(".rm-answered"),                                 // G: the host answers --for
		rm,                                                          // H: the rm runs
	)
	r := runCommand(t, workspace, []string{"run", config.AcceptConfigChangesFlag, "--", "bash", "-lc", script}, fx.opts...)
	got := <-answers
	t.Logf("launch: rc %d\n%s", r.rc, r.combined())
	for i, a := range got {
		t.Logf("answer %d (%s): over-cap err=%v\n%s\naccepted err=%v\n%s", i, a.id, a.overErr, a.over, a.err, a.ok)
		if a.err != nil {
			t.Fatalf("answer %d: the host's timed approval failed: %v\n%s", i, a.err, a.ok)
		}
	}

	for k, want := range map[string]string{"RCA": "75", "RCB": "0", "RCC": "0", "RCD": "0", "RCE": "75", "RCF": "75", "RCG": "0", "RCH": "0"} {
		if v := kvLine(r.stdout, k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
	// BX-D17: an over-cap answer is refused with the policy exit, names the cap, and decides
	// nothing (the accepted answer that follows proves the request was still pending).
	for i, c := range []struct{ cap string }{{"--for 1h"}, {"--for 15m"}} {
		a := got[i]
		var exit *exec.ExitError
		if !errors.As(a.overErr, &exit) || exit.ExitCode() != 5 || !strings.Contains(a.over, c.cap) {
			t.Errorf("answer %d: the over-cap --for was not refused naming %q (err=%v):\n%s", i, c.cap, a.overErr, a.over)
		}
	}
	if !strings.Contains(got[0].ok, "pack writes put_objects on") || !strings.Contains(got[1].ok, "pack writes delete_objects on") {
		t.Errorf("the timed answers do not name the cell and resource they cover:\n%s\n%s", got[0].ok, got[1].ok)
	}
	if b, ok := server.Object("bkt", "team/new.txt"); !ok || string(b) != "two\n" {
		t.Errorf("the covered puts did not land in order: %q ok=%v", b, ok)
	}
	if _, ok := server.Object("bkt", "team/other.txt"); ok {
		t.Error("a put outside the timed answer reached the service")
	}
	if _, ok := server.Object("bkt", "team/a.txt"); ok {
		t.Error("the approved rm did not delete the object")
	}
	if n := server.Writes(); n != 3 {
		t.Errorf("the fixture saw %d writes, want the two covered puts and the approved rm", n)
	}
	if asked, _ := os.ReadFile(filepath.Join(workspace, ".other-asked")); !requestIDInRefusal.Match(asked) {
		t.Errorf("the put to another object did not ask with a request id:\n%s", asked)
	}
	if strings.Contains(r.combined(), s3fixture.SecretAccessKey) {
		t.Error("jail-visible output contains the synthetic secret")
	}
	if t.Failed() {
		t.Fatalf("output above")
	}
}
