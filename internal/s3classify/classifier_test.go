package s3classify

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

var testDigest = strings.Repeat("a", 64)

func input(roots []string, argv ...string) callbroker.CLIInput {
	return callbroker.CLIInput{
		Version:     protocolVersion,
		Front:       "cli",
		Argv:        argv,
		StdinSHA256: testDigest,
		Roots:       roots,
	}
}

func res(t, id string) callbroker.Resource { return callbroker.Resource{Type: t, ID: id} }

type allowed struct {
	name   string
	roots  []string
	argv   []string
	canon  []string
	reads  [][]callbroker.Resource
	writes []callbroker.Write
}

func allowedRows() []allowed {
	root := []string{"s3://bkt"}
	return []allowed{
		{name: "R1 bucket no slash", roots: root, argv: []string{"s3", "ls", "s3://bkt"},
			canon: []string{"s3", "ls", "s3://bkt/"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/"), res("location", "s3://bkt")}}},
		{name: "R1 bucket with slash", roots: root, argv: []string{"s3", "ls", "s3://bkt/"},
			canon: []string{"s3", "ls", "s3://bkt/"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/"), res("location", "s3://bkt")}}},
		{name: "R1 prefix", roots: root, argv: []string{"s3", "ls", "s3://bkt/team/x"},
			canon: []string{"s3", "ls", "s3://bkt/team/x"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/team/x"), res("location", "s3://bkt")}}},
		{name: "R1 recursive before", roots: root, argv: []string{"s3", "ls", "--recursive", "s3://bkt/team/"},
			canon: []string{"s3", "ls", "s3://bkt/team/", "--recursive"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/team/"), res("location", "s3://bkt")}}},
		{name: "R1 recursive after", roots: root, argv: []string{"s3", "ls", "s3://bkt", "--recursive"},
			canon: []string{"s3", "ls", "s3://bkt/", "--recursive"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/"), res("location", "s3://bkt")}}},
		{name: "R2 cp to stdout", roots: root, argv: []string{"s3", "cp", "s3://bkt/k.txt", "-"},
			canon: []string{"s3", "cp", "s3://bkt/k.txt", "-"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/k.txt"), res("location", "s3://bkt")}}},
		{name: "R3 head-object", roots: root, argv: []string{"s3api", "head-object", "--bucket", "bkt", "--key", "a/k"},
			canon: []string{"s3api", "head-object", "--bucket", "bkt", "--key", "a/k"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/a/k"), res("location", "s3://bkt")}}},
		{name: "R3 reversed", roots: root, argv: []string{"s3api", "head-object", "--key", "a/k", "--bucket", "bkt"},
			canon: []string{"s3api", "head-object", "--bucket", "bkt", "--key", "a/k"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/a/k"), res("location", "s3://bkt")}}},
		{name: "R3 equals form", roots: root, argv: []string{"s3api", "head-object", "--bucket=bkt", "--key=a/k"},
			canon: []string{"s3api", "head-object", "--bucket", "bkt", "--key", "a/k"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/a/k"), res("location", "s3://bkt")}}},
		{name: "R3 mixed form", roots: root, argv: []string{"s3api", "head-object", "--key=a/k", "--bucket", "bkt"},
			canon: []string{"s3api", "head-object", "--bucket", "bkt", "--key", "a/k"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/a/k"), res("location", "s3://bkt")}}},
		{name: "W1 put", roots: root, argv: []string{"s3", "cp", "-", "s3://bkt/new/k"},
			canon: []string{"s3", "cp", "-", "s3://bkt/new/k"},
			writes: []callbroker.Write{{Cell: "put_objects",
				Chain:       []callbroker.Resource{res("object", "s3://bkt/new/k"), res("location", "s3://bkt")},
				Description: "create or replace object s3://bkt/new/k from standard input"}}},
		{name: "W2 delete", roots: root, argv: []string{"s3", "rm", "s3://bkt/old"},
			canon: []string{"s3", "rm", "s3://bkt/old"},
			writes: []callbroker.Write{{Cell: "delete_objects",
				Chain:       []callbroker.Resource{res("object", "s3://bkt/old"), res("location", "s3://bkt")},
				Description: "delete object s3://bkt/old"}}},
		{name: "longest root wins for a read", roots: []string{"s3://bkt", "s3://bkt/team/"},
			argv:  []string{"s3", "cp", "s3://bkt/team/k", "-"},
			canon: []string{"s3", "cp", "s3://bkt/team/k", "-"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/team/k"), res("location", "s3://bkt/team/")}}},
		{name: "shorter root when the longer does not cover", roots: []string{"s3://bkt/team/", "s3://bkt"},
			argv:  []string{"s3", "cp", "s3://bkt/other/k", "-"},
			canon: []string{"s3", "cp", "s3://bkt/other/k", "-"},
			reads: [][]callbroker.Resource{{res("object", "s3://bkt/other/k"), res("location", "s3://bkt")}}},
		{name: "prefix root covers its own listing", roots: []string{"s3://bkt/team/"},
			argv:  []string{"s3", "ls", "s3://bkt/team/"},
			canon: []string{"s3", "ls", "s3://bkt/team/"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/team/"), res("location", "s3://bkt/team/")}}},
		{name: "malformed roots are ignored", roots: []string{"s3://B_ad", "http://b", "s3://bkt/noslash", "", "s3://bkt/", "s3://bkt"},
			argv:  []string{"s3", "ls", "s3://bkt/x"},
			canon: []string{"s3", "ls", "s3://bkt/x"},
			reads: [][]callbroker.Resource{{res("prefix", "s3://bkt/x"), res("location", "s3://bkt")}}},
		{name: "write under a prefix root", roots: []string{"s3://bkt/team/"},
			argv:  []string{"s3", "rm", "s3://bkt/team/old"},
			canon: []string{"s3", "rm", "s3://bkt/team/old"},
			writes: []callbroker.Write{{Cell: "delete_objects",
				Chain:       []callbroker.Resource{res("object", "s3://bkt/team/old"), res("location", "s3://bkt/team/")},
				Description: "delete object s3://bkt/team/old"}}},
	}
}

func TestS3ClassifierAllowsEveryTableRow(t *testing.T) {
	for _, row := range allowedRows() {
		t.Run(row.name, func(t *testing.T) {
			answer := Classify(input(row.roots, row.argv...))
			if answer.Refused != nil || answer.Classification == nil || answer.Local {
				t.Fatalf("not classified: %+v", answer)
			}
			got := answer.Classification
			if !reflect.DeepEqual(got.Argv, row.canon) {
				t.Fatalf("canonical argv = %q, want %q", got.Argv, row.canon)
			}
			if got.StdinSHA256 != testDigest {
				t.Fatalf("stdin digest not passed through: %q", got.StdinSHA256)
			}
			if !reflect.DeepEqual(got.Reads, row.reads) {
				t.Fatalf("reads = %+v, want %+v", got.Reads, row.reads)
			}
			if !reflect.DeepEqual(got.Writes, row.writes) {
				t.Fatalf("writes = %+v, want %+v", got.Writes, row.writes)
			}
			if len(answer.Lookups) != 0 {
				t.Fatalf("classifier asked for lookups: %+v", answer.Lookups)
			}
		})
	}
}

func TestS3ClassifierVersionIsRefusedAndNothingIsLocal(t *testing.T) {
	answer := Classify(input([]string{"s3://bkt"}, "--version"))
	if answer.Local || answer.Classification != nil || answer.Refused == nil ||
		answer.Refused.Class != "unsupported" || !strings.Contains(answer.Refused.NextStep, "aws --version` on the host") {
		t.Fatalf("--version must be refused unsupported naming the host: %+v", answer)
	}
	if a := Classify(input(nil, "--version", "s3")); a.Local || a.Refused == nil {
		t.Fatalf("--version with company was not refused: %+v", a)
	}
	for _, row := range allowedRows() {
		if Classify(input(row.roots, row.argv...)).Local {
			t.Fatalf("%s: classifier returned Local", row.name)
		}
	}
}

func TestS3ClassifierRootWithTrailingSlashOnBucketIsIgnored(t *testing.T) {
	a := Classify(input([]string{"s3://bkt/"}, "s3", "ls", "s3://bkt/x"))
	if a.Refused == nil || a.Refused.Class != "scope" {
		t.Fatalf("s3://bkt/ root must be ignored: %+v", a)
	}
}

func TestS3ClassifierChainsAreStructurallyValidForCore(t *testing.T) {
	// callbroker.Classifier.validateChain is unexported and the classifier runs as a
	// subprocess, so assert the properties it enforces: each type once, the last element
	// is a parentless `location` equal to a root, and a write's description is bounded.
	for _, row := range allowedRows() {
		t.Run(row.name, func(t *testing.T) {
			answer := Classify(input(row.roots, row.argv...))
			if answer.Classification == nil {
				t.Fatalf("not classified: %+v", answer)
			}
			chains := append([][]callbroker.Resource(nil), answer.Classification.Reads...)
			for _, w := range answer.Classification.Writes {
				chains = append(chains, w.Chain)
				if len(w.Description) == 0 || len(w.Description) > 512 {
					t.Fatalf("description length %d", len(w.Description))
				}
				if w.Cell != "put_objects" && w.Cell != "delete_objects" {
					t.Fatalf("undeclared cell %q", w.Cell)
				}
			}
			if len(chains) != 1 {
				t.Fatalf("want exactly one chain, got %d", len(chains))
			}
			for _, chain := range chains {
				seen := map[string]bool{}
				for _, r := range chain {
					if seen[r.Type] {
						t.Fatalf("type %q repeated in %+v", r.Type, chain)
					}
					seen[r.Type] = true
					if r.Type != "location" && r.Type != "prefix" && r.Type != "object" {
						t.Fatalf("undeclared type %q", r.Type)
					}
				}
				last := chain[len(chain)-1]
				if last.Type != "location" || len(chain) != 2 {
					t.Fatalf("chain must be child then location: %+v", chain)
				}
				isRoot := false
				for _, r := range row.roots {
					if r == last.ID {
						isRoot = true
					}
				}
				if !isRoot {
					t.Fatalf("location %q is not a declared root %q", last.ID, row.roots)
				}
			}
		})
	}
}

func TestS3ClassifierStringPrefixIsNotDirectoryCoverage(t *testing.T) {
	roots := []string{"s3://bkt/p/"}
	for _, argv := range [][]string{
		{"s3", "ls", "s3://bkt/p"},
		{"s3", "cp", "s3://bkt/p", "-"},
		{"s3", "cp", "s3://bkt/pp/k", "-"},
		{"s3", "rm", "s3://bkt/p"},
		{"s3", "ls", "s3://bkt"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			a := Classify(input(roots, argv...))
			if a.Refused == nil || a.Refused.Class != "scope" || a.Classification != nil {
				t.Fatalf("string-prefix trap not refused as scope: %+v", a)
			}
			if !strings.Contains(a.Refused.NextStep, "brokered.aws-s3.roots") || !strings.Contains(a.Refused.NextStep, "yolo config apply") {
				t.Fatalf("scope next step: %q", a.Refused.NextStep)
			}
		})
	}
}

func TestS3ClassifierRefusesOutsideTheRoots(t *testing.T) {
	for _, roots := range [][]string{nil, {}, {"s3://other"}, {"s3://B_ad", "garbage"}} {
		a := Classify(input(roots, "s3", "ls", "s3://bkt"))
		if a.Refused == nil || a.Refused.Class != "scope" || a.Refused.NextStep == "" {
			t.Fatalf("roots %q: %+v", roots, a)
		}
	}
}

func TestS3ClassifierRefusesEveryGlobalOptionAsCredential(t *testing.T) {
	roots := []string{"s3://bkt"}
	for _, argv := range [][]string{
		{"--endpoint-url", "http://x", "s3", "ls", "s3://bkt"},
		{"--endpoint-url=http://x", "s3", "ls", "s3://bkt"},
		{"s3", "ls", "s3://bkt", "--endpoint-url", "http://x"},
		{"s3", "--profile", "p", "ls", "s3://bkt"},
		{"s3", "ls", "--region", "us-east-1", "s3://bkt"},
		{"s3", "cp", "s3://bkt/k", "-", "--no-sign-request"},
		{"s3", "cp", "-", "s3://bkt/k", "--no-verify-ssl"},
		{"s3", "rm", "s3://bkt/k", "--ca-bundle", "/x"},
		{"s3", "ls", "s3://bkt", "--debug"},
		{"s3", "ls", "s3://bkt", "--output", "json"},
		{"s3api", "head-object", "--bucket", "bkt", "--key", "k", "--query", "x"},
		{"s3", "ls", "s3://bkt", "--cli-read-timeout", "1"},
		{"s3", "ls", "s3://bkt", "--cli-connect-timeout=1"},
		{"s3", "ls", "s3://bkt", "--color", "off"},
		{"s3", "ls", "s3://bkt", "--no-paginate"},
		{"ec2", "describe-instances", "--region", "x"},
		{"s3", "ls", "s3://bkt", "--some-future-option"},
		{"--version", "--profile", "p"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			a := Classify(input(roots, argv...))
			if a.Refused == nil || a.Refused.Class != "credential" || a.Classification != nil {
				t.Fatalf("not refused as credential: %+v", a)
			}
			if !strings.Contains(a.Refused.NextStep, "host's AWS configuration or environment") {
				t.Fatalf("next step: %q", a.Refused.NextStep)
			}
		})
	}
}

func TestS3ClassifierRefusesUnsupportedShapes(t *testing.T) {
	roots := []string{"s3://bkt"}
	long := strings.Repeat("k", 1025)
	for _, argv := range [][]string{
		{"s3", "rm", "--recursive", "s3://bkt/p/"},
		{"s3", "rm", "s3://bkt/p", "--recursive"},
		{"s3", "rm", "s3://bkt/p/"},
		{"s3", "cp", "s3://bkt/k", "-", "--recursive"},
		{"s3", "ls", "s3://bkt", "--include", "*"},
		{"s3", "ls", "--recursive", "--recursive", "s3://bkt"},
		{"s3", "ls", "s3://bkt", "s3://bkt"},
		{"s3", "ls"},
		{"s3", "ls", "-"},
		{"s3", "ls", "b"},
		{"s3", "sync", ".", "s3://bkt"},
		{"s3", "mv", "s3://bkt/a", "s3://bkt/c"},
		{"s3", "cp", "s3://bkt/a", "s3://bkt/c"},
		{"s3", "cp", "./local", "s3://bkt/c"},
		{"s3", "cp", "s3://bkt/a", "./local"},
		{"s3", "cp", "-", "-"},
		{"s3", "cp", "s3://bkt/a"},
		{"s3", "cp", "-", "s3://bkt/"},
		{"s3", "cp", "s3://bkt", "-"},
		{"s3", "presign", "s3://bkt/k"},
		{"s3", "mb", "s3://new"},
		{"s3", "rm"},
		{"s3"},
		{"s3api", "get-object", "--bucket", "bkt", "--key", "k", "out"},
		{"s3api", "put-object", "--bucket", "bkt", "--key", "k"},
		{"s3api", "delete-objects", "--bucket", "bkt"},
		{"s3api", "head-object", "--bucket", "bkt"},
		{"s3api", "head-object", "--key", "k"},
		{"s3api", "head-object", "--bucket", "bkt", "--bucket", "bkt", "--key", "k"},
		{"s3api", "head-object", "--bucket", "bkt", "--key", "k", "extra"},
		{"s3api", "head-object", "--bucket", "bkt", "--key"},
		{"s3api", "head-object", "--bucket", "bkt", "--key", "k", "--version-id", "v"},
		{"s3api", "head-object", "--bucket=", "--key", "k"},
		{"s3api"},
		{"s3", "ls", "arn:aws:s3:::b"},
		{"s3api", "head-object", "--bucket", "arn:aws:s3:::b", "--key", "k"},
		{"ec2", "describe-instances"},
		{"sts", "get-caller-identity"},
		{"help"},
		{"s3", "ls", "s3://bkt/" + long},
		{"s3", "cp", "s3://bkt/" + long, "-"},
		{"s3", "cp", "s3://bkt/a b", "-"},
		{"s3", "cp", "s3://bkt/a?x", "-"},
		{"s3", "cp", "s3://bkt/a%20b", "-"},
		{"s3", "cp", "s3://bkt/../k", "-"},
		{"s3", "cp", "s3://bkt/a/./k", "-"},
		{"s3", "cp", "s3://bkt/a//k", "-"},
		{"s3", "cp", "s3://bkt//k", "-"},
		{"s3", "ls", "s3://bkt/a//"},
		{"s3", "ls", "s3://bkt//"},
		{"s3", "cp", "s3://bkt/é", "-"},
		{"s3", "ls", "s3://B"},
		{"s3", "ls", "s3://a"},
		{"s3", "ls", "s3://-ab"},
		{"s3", "ls", "s3://ab-"},
		{"s3", "ls", "s3://a..b"},
		{"s3", "ls", "s3://192.168.0.1"},
		{"s3", "ls", "s3://" + strings.Repeat("a", 64)},
		{"s3", "ls", "s3://"},
		{"s3", "ls", "S3://b"},
		{"s3", "ls", "s3:/b"},
		{"-h"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			a := Classify(input(roots, argv...))
			if a.Refused == nil || a.Refused.Class != "unsupported" || a.Classification != nil || a.Local {
				t.Fatalf("not refused as unsupported: %+v", a)
			}
			if a.Refused.NextStep == "" || a.Refused.Reason == "" {
				t.Fatalf("refusal omitted reason or next step: %+v", a.Refused)
			}
		})
	}
}

func TestS3ClassifierBadNamesNameTheAlphabet(t *testing.T) {
	a := Classify(input([]string{"s3://bkt"}, "s3", "cp", "s3://bkt/a b", "-"))
	if a.Refused == nil || !strings.Contains(a.Refused.NextStep+a.Refused.Reason, "A-Za-z0-9._=/-") {
		t.Fatalf("key refusal does not name the alphabet: %+v", a.Refused)
	}
}

func TestS3ClassifierIsDeterministic(t *testing.T) {
	for _, row := range allowedRows() {
		in := input(row.roots, row.argv...)
		first, second := Classify(in), Classify(in)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("%s: two calls differ: %+v vs %+v", row.name, first, second)
		}
	}
	// Root order never changes the answer.
	a := Classify(input([]string{"s3://bkt", "s3://bkt/team/"}, "s3", "rm", "s3://bkt/team/k"))
	b := Classify(input([]string{"s3://bkt/team/", "s3://bkt"}, "s3", "rm", "s3://bkt/team/k"))
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("root order changed the answer: %+v vs %+v", a, b)
	}
}

func TestS3ClassifierDoesNotAliasTheInput(t *testing.T) {
	in := input([]string{"s3://bkt"}, "s3api", "head-object", "--bucket=bkt", "--key=k")
	a := Classify(in)
	a.Classification.Argv[0] = "changed"
	if in.Argv[0] != "s3api" {
		t.Fatalf("input argv aliased")
	}
}

func TestS3ClassifierUsesActualProtocolDispatch(t *testing.T) {
	raw, err := json.Marshal(input([]string{"s3://bkt"}, "s3", "ls", "s3://bkt/x"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if rc := Run([]string{"aws-s3"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
		t.Fatalf("dispatch rc=%d stderr=%s", rc, stderr.String())
	}
	var answer callbroker.Answer
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("invalid protocol: %v (%q)", err, stdout.String())
	}
	if answer.Classification == nil || answer.Classification.Reads[0][0].ID != "s3://bkt/x" {
		t.Fatalf("dispatch did not classify the read: %+v", answer)
	}
}

func TestS3ClassifierRunRefusesWrongSource(t *testing.T) {
	for _, args := range [][]string{nil, {"circleci"}, {"aws-s3", "x"}} {
		var stdout, stderr bytes.Buffer
		if rc := Run(args, strings.NewReader("{}"), &stdout, &stderr); rc != 64 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args %q rc=%d stdout=%q stderr=%q", args, rc, stdout.String(), stderr.String())
		}
	}
}

func TestS3ClassifierRejectsMalformedProtocol(t *testing.T) {
	good := `"version":"yolo.call-broker-classifier/1","front":"cli","argv":["s3","ls","s3://bkt"],"roots":["s3://bkt"]`
	for _, raw := range []string{
		`{` + good + `,"extra":true}`,
		`{` + good + `}{}`,
		`{` + good + `} x`,
		`not json`,
		``,
		`{"version":"yolo.call-broker-classifier/2","front":"cli","argv":["s3","ls","s3://bkt"],"roots":["s3://bkt"]}`,
		`{"version":"yolo.call-broker-classifier/1","front":"http","argv":["s3","ls","s3://bkt"],"roots":["s3://bkt"]}`,
		`{"version":"yolo.call-broker-classifier/1","front":"cli","argv":[],"roots":["s3://bkt"]}`,
		`{` + good + `,"stdin_sha256":"` + strings.Repeat("a", 70*1024) + `"}`,
	} {
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"aws-s3"}, strings.NewReader(raw), &stdout, &stderr); rc != 2 || stdout.Len() != 0 {
			t.Fatalf("malformed input %.60q rc=%d stdout=%q stderr=%q", raw, rc, stdout.String(), stderr.String())
		}
	}
}

func TestS3ClassifierRefusesBadProtocolInClassify(t *testing.T) {
	in := input([]string{"s3://bkt"}, "s3", "ls", "s3://bkt")
	in.Version = "other"
	if a := Classify(in); a.Refused == nil || a.Refused.Class != "unsupported" {
		t.Fatalf("bad version not refused: %+v", a)
	}
}

// A head-object value the host aws would parse as an option never reaches the canonical argv:
// `--key=--debug` must not become the bare word `--debug` on the host.
func TestS3ClassifierRefusesAnOptionShapedHeadObjectValue(t *testing.T) {
	roots := []string{"s3://bkt"}
	for _, argv := range [][]string{
		{"s3api", "head-object", "--bucket", "bkt", "--key=--debug"},
		{"s3api", "head-object", "--bucket", "bkt", "--key=--endpoint-url=a.b"},
		{"s3api", "head-object", "--bucket", "bkt", "--key=--profile=x"},
		{"s3api", "head-object", "--bucket", "bkt", "--key", "-x"},
		{"s3api", "head-object", "--bucket", "bkt", "--key=--recursive"},
		{"s3api", "head-object", "--bucket=-x", "--key", "k"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			a := Classify(input(roots, argv...))
			if a.Refused == nil || a.Classification != nil {
				t.Fatalf("an option-shaped value was classified: %+v", a)
			}
		})
	}
}

// A refusal names a jail-supplied option only when it is a plain option name, so a crafted
// word with control, format or bidi characters is never echoed (core refuses such text).
func TestS3ClassifierEchoesOnlyPlainOptionNames(t *testing.T) {
	roots := []string{"s3://bkt"}
	a := Classify(input(roots, "--profile=secret-name", "s3", "ls", "s3://bkt"))
	if a.Refused == nil || !strings.Contains(a.Refused.Reason, "--profile ") || strings.Contains(a.Refused.Reason, "secret-name") {
		t.Fatalf("plain option: %+v", a.Refused)
	}
	a = Classify(input(roots, "--a\u202eb", "s3", "ls", "s3://bkt"))
	if a.Refused == nil || a.Refused.Class != "credential" || strings.Contains(a.Refused.Reason, "\u202e") {
		t.Fatalf("crafted option: %+v", a.Refused)
	}
}
