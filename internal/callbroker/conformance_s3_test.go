package callbroker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	kit "github.com/mschulkind-oss/yolo-jail/internal/callbrokerconformance"
	"github.com/mschulkind-oss/yolo-jail/internal/s3fixture"
)

const conformanceS3UnsupportedNext = "Use `aws s3 ls s3://<bucket>[/<prefix>] [--recursive]`, `aws s3 cp s3://<bucket>/<key> -`, `aws s3 cp - s3://<bucket>/<key>`, `aws s3 rm s3://<bucket>/<key>` or `aws s3api head-object --bucket <bucket> --key <key>`, or run the command on the host"
const conformanceS3ScopeNext = "Add the root to `brokered.aws-s3.roots` in this workspace's config, then run `yolo config apply` on the host"

func conformanceS3Refusal(class, reason, next string) Answer {
	return Answer{Version: "yolo.call-broker-classifier/1", Refused: &Refusal{Class: class, Reason: reason, NextStep: next}}
}

func TestConformanceS3Classifications(t *testing.T) {
	f := newS3E2E(t)
	cases := []kit.ClassificationCase{}
	read := func(name string, original, canonical []string, typ, id string) {
		a := conformanceAnswer(canonical, nil)
		a.Classification.Reads = [][]Resource{{{Type: typ, ID: id}, {Type: "location", ID: s3Root}}}
		cases = append(cases, kit.ClassificationCase{Name: name, Call: kit.Call{Argv: original}, Want: conformanceWant(t, a)})
	}
	read("R1", []string{"s3", "ls", s3Root}, []string{"s3", "ls", s3Root}, "prefix", s3Root)
	read("R1 recursive order", []string{"s3", "ls", "--recursive", s3Root}, []string{"s3", "ls", s3Root, "--recursive"}, "prefix", s3Root)
	read("R2", []string{"s3", "cp", s3Root + "a.txt", "-"}, []string{"s3", "cp", s3Root + "a.txt", "-"}, "object", s3Root+"a.txt")
	read("R3", []string{"s3api", "head-object", "--bucket", "bkt", "--key", "team/a.txt"}, []string{"s3api", "head-object", "--bucket", "bkt", "--key", "team/a.txt"}, "object", s3Root+"a.txt")
	read("R3 equals normalization", []string{"s3api", "head-object", "--key=team/a.txt", "--bucket=bkt"}, []string{"s3api", "head-object", "--bucket", "bkt", "--key", "team/a.txt"}, "object", s3Root+"a.txt")
	for _, w := range []struct {
		name                  string
		argv                  []string
		stdin                 []byte
		cell, id, description string
	}{
		{"W1", []string{"s3", "cp", "-", s3Root + "new.txt"}, []byte("hello\n"), "put_objects", s3Root + "new.txt", "create or replace object " + s3Root + "new.txt from standard input"},
		{"W2", []string{"s3", "rm", s3Root + "a.txt"}, nil, "delete_objects", s3Root + "a.txt", "delete object " + s3Root + "a.txt"},
	} {
		a := conformanceAnswer(w.argv, w.stdin)
		a.Classification.Writes = []Write{{Cell: w.cell, Chain: []Resource{{Type: "object", ID: w.id}, {Type: "location", ID: s3Root}}, Description: w.description}}
		cases = append(cases, kit.ClassificationCase{Name: w.name, Call: kit.Call{Argv: w.argv, Stdin: w.stdin}, Want: conformanceWant(t, a)})
	}
	for _, r := range conformanceS3Refusals() {
		cases = append(cases, kit.ClassificationCase{Name: r.name, Call: kit.Call{Argv: r.argv}, Want: conformanceWant(t, r.answer)})
	}
	if err := kit.CheckClassifications(context.Background(), conformanceClassify(f.h.classifier, nil), cases); err != nil {
		t.Fatal(err)
	}
	t.Run("longest covering root", func(t *testing.T) {
		c := f.h.classifier
		c.Roots = []string{"s3://bkt", s3Root}
		if err := kit.CheckClassifications(context.Background(), conformanceClassify(c, nil), cases[:1]); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("malformed root covers nothing", func(t *testing.T) {
		c := f.h.classifier
		c.Roots = []string{"s3://bkt/team"}
		a := conformanceS3Refusal("scope", "no approved S3 root covers that bucket and key", conformanceS3ScopeNext)
		if err := kit.CheckClassifications(context.Background(), conformanceClassify(c, nil), []kit.ClassificationCase{{Name: "malformed root", Call: cases[0].Call, Want: conformanceWant(t, a)}}); err != nil {
			t.Fatal(err)
		}
	})
}

func conformanceS3Refusals() []struct {
	name   string
	argv   []string
	answer Answer
} {
	return []struct {
		name   string
		argv   []string
		answer Answer
	}{
		{"credential endpoint", []string{"--endpoint-url", "http://127.0.0.1:1", "s3", "ls", s3Root}, conformanceS3Refusal("credential", "the option --endpoint-url is not accepted from the jail", "Set it in the host's AWS configuration or environment; the broker never takes an endpoint, profile, region or signing option from the jail")},
		{"unsupported sync", []string{"s3", "sync", ".", s3Root}, conformanceS3Refusal("unsupported", "this s3 command is not enabled by the aws-s3 pack", conformanceS3UnsupportedNext)},
		{"uncovered bucket", []string{"s3", "rm", "s3://other-bkt/x"}, conformanceS3Refusal("scope", "no approved S3 root covers that bucket and key", conformanceS3ScopeNext)},
		{"malformed name", []string{"s3", "rm", "s3://BKT/team/a.txt"}, conformanceS3Refusal("unsupported", "the bucket name must be a plain lowercase S3 bucket name (not an ARN or an IP address)", conformanceS3UnsupportedNext)},
	}
}

func TestConformanceS3Lifecycle(t *testing.T) {
	f := newS3E2E(t)
	observe := func(o kit.Observation) kit.Observation {
		if strings.Contains(o.Output, s3fixture.SecretAccessKey) || strings.Contains(o.Output, s3fixture.AccessKeyID) {
			t.Fatal("observation leaked synthetic S3 credential")
		}
		o.Counts = conformanceCounts(t, f.store, f.server.Writes())
		return o
	}
	a := conformanceAdapter{read: kit.Call{Argv: []string{"s3", "cp", s3Root + "a.txt", "-"}}, write: kit.Call{Argv: []string{"s3", "cp", "-", s3Root + "new.txt"}, Stdin: []byte("hello\n")}, readOutput: "alpha from the fixture\n", secrets: []string{s3fixture.SecretAccessKey, s3fixture.AccessKeyID}}
	a.call = func(ctx context.Context, op string, call kit.Call) (kit.Observation, error) {
		before := f.requests()
		var o kit.Observation
		var err error
		if op == "act" {
			rc, out := f.aws(string(call.Stdin), call.Argv...)
			verdict := map[int]string{0: "runs", 75: "asks", 64: "refused"}[rc]
			if verdict == "" {
				err = fmt.Errorf("unexpected shim exit %d: %s", rc, out)
			}
			o = kit.Observation{Verdict: verdict, Output: out}
		} else {
			o, err = conformanceHandler(ctx, f.h, op, call)
		}
		if (op != "act" || o.Verdict != "runs") && f.requests() != before {
			t.Fatalf("%s/%s reached S3 without execution", op, o.Verdict)
		}
		return observe(o), err
	}
	a.approve = func(ctx context.Context) (kit.Observation, error) {
		before := f.requests()
		out, err := f.host("approve", conformancePending(t, f.store))
		o := kit.Observation{Output: out}
		if err == nil {
			o.Verdict = "approved"
		}
		if f.requests() != before {
			t.Fatal("host approval executed S3")
		}
		return observe(o), err
	}
	checkConformanceLifecycle(t, a)
	if got, ok := f.server.Object("bkt", "team/new.txt"); !ok || string(got) != "hello\n" {
		t.Fatalf("original stdin object=%q present=%v", got, ok)
	}
}

func TestConformanceS3ShimRefusals(t *testing.T) {
	f := newS3E2E(t)
	for _, r := range conformanceS3Refusals() {
		t.Run(r.name, func(t *testing.T) {
			before := f.requests()
			rc, out := f.aws("", r.argv...)
			if strings.Contains(out, s3fixture.SecretAccessKey) || strings.Contains(out, s3fixture.AccessKeyID) {
				t.Fatal("refusal leaked synthetic S3 credential")
			}
			if rc != 64 {
				t.Fatalf("rc=%d output=%s", rc, out)
			}
			if f.requests() != before || conformanceCounts(t, f.store, f.server.Writes()) != (kit.Counts{}) {
				t.Fatal("refused shim reached upstream or changed state")
			}
		})
	}
}
