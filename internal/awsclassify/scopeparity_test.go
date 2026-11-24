package awsclassify

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestAWSRunAndShippedScopeMatcherAgreeWithoutWideningAdmission(t *testing.T) {
	declaration, err := loopholedecl.LoadDir(filepath.Join("..", "..", "packs", "aws", "loopholes", "aws-broker"))
	if err != nil {
		t.Fatal(err)
	}
	scope := declaration.CallBroker.Scope
	if scope.Nesting != "segment" || scope.Separator != "/" || len(scope.Anchors) != 2 {
		t.Fatalf("shipped matching metadata missing: %+v", scope)
	}
	matcher, err := brokerpolicy.NewScopeMatcher(scope.Nesting, scope.Separator, scope.Anchors)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, root, address, id string
		admitted                bool
	}{
		{"bucket-empty-key", "s3://bkt", "s3://bkt", "s3://bkt/", true},
		{"bucket-root-slash", "s3://bkt", "s3://bkt/", "s3://bkt/", true},
		{"bucket-nested-key", "s3://bkt", "s3://bkt/team/report/", "s3://bkt/team/report/", true},
		{"exact-prefix", "s3://bkt/team/", "s3://bkt/team/", "s3://bkt/team/", true},
		{"nested-prefix", "s3://bkt/team/", "s3://bkt/team/report/", "s3://bkt/team/report/", true},
		{"prefix-without-separator", "s3://bkt/team/", "s3://bkt/team", "s3://bkt/team", false},
		{"prefix-sibling", "s3://bkt/team/", "s3://bkt/team-mate/", "s3://bkt/team-mate/", false},
		{"parent-of-prefix", "s3://bkt/team/", "s3://bkt/", "s3://bkt/", false},
		{"bucket-name-sibling", "s3://bkt", "s3://bkt-other/", "s3://bkt-other/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := input([]string{tc.root}, "s3", "ls", tc.address)
			request.Version = typedProtocolVersion
			raw, _ := json.Marshal(request)
			var stdout, stderr bytes.Buffer
			if rc := Run([]string{"aws"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
				t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
			}
			var answer callbroker.Answer
			if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
				t.Fatal(err)
			}
			if (answer.Classification != nil) != tc.admitted || matcher.Contains(tc.root, tc.id) != tc.admitted {
				t.Fatalf("H9 matcher/admission disagree: root=%q id=%q answer=%+v", tc.root, tc.id, answer)
			}
			if tc.admitted {
				classification := answer.Classification
				chain := classification.Reads[0]
				if classification.Argv[2] != tc.id || chain[0].ID != tc.id || chain[len(chain)-1].ID != tc.root {
					t.Fatalf("H9 canonical address or approved root bytes changed: %+v", classification)
				}
			}
		})
	}
	// Scope-only spellings must not become new parseRoots/admission grammar.
	for _, root := range []string{"s3://", "s3://bkt/", "s3://bkt/team", "ce://"} {
		if err := matcher.ValidateScope(root); err != nil {
			t.Fatalf("scope-only bytes rejected: %q %v", root, err)
		}
		request := input([]string{root}, "s3", "ls", "s3://bkt/team/")
		if answer := Classify(request); answer.Classification != nil {
			t.Fatalf("scope matcher admitted new root grammar: %q", root)
		}
	}
	for _, argv := range [][]string{{"s3", "cp", "s3://bkt/team/x", "-"}, {"s3api", "head-object", "--bucket", "bkt", "--key", "team/x"}} {
		request := input([]string{"s3://bkt/team/"}, argv...)
		request.Version = typedProtocolVersion
		raw, _ := json.Marshal(request)
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"aws"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
			t.Fatal(stderr.String())
		}
		var answer callbroker.Answer
		if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil || answer.Classification == nil {
			t.Fatalf("object/head row not classified: %v %+v", err, answer)
		}
		chain := answer.Classification.Reads[0]
		if chain[0].ID != "s3://bkt/team/x" || chain[1].ID != "s3://bkt/team/" || !matcher.Contains(chain[1].ID, chain[0].ID) {
			t.Fatal("H9 object/head parity failed")
		}
	}
}
