package awsclassify

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func TestAWSRunNamesOnlyTheSevenExistingReadRows(t *testing.T) {
	period := "Start=2026-01-01,End=2026-02-01"
	for _, tc := range []struct {
		cell string
		argv []string
	}{
		{"list_objects", []string{"s3", "ls", "s3://bkt/team/", "--recursive"}},
		{"get_objects", []string{"s3", "cp", "s3://bkt/team/x", "-"}},
		{"head_objects", []string{"s3api", "head-object", "--bucket", "bkt", "--key", "team/x"}},
		{"get_cost_and_usage", []string{"ce", "get-cost-and-usage", "--time-period", period, "--granularity", "MONTHLY", "--metrics", "UnblendedCost"}},
		{"get_cost_forecast", []string{"ce", "get-cost-forecast", "--time-period", period, "--granularity", "DAILY", "--metric", "UNBLENDED_COST"}},
		{"get_dimension_values", []string{"ce", "get-dimension-values", "--time-period", period, "--dimension", "SERVICE"}},
		{"get_tags", []string{"ce", "get-tags", "--time-period", period}},
	} {
		t.Run(tc.cell, func(t *testing.T) {
			request := input([]string{"s3://bkt/team/", "ce://host-login"}, tc.argv...)
			var answers []callbroker.Answer
			for _, version := range []string{protocolVersion, typedProtocolVersion} {
				request.Version = version
				raw, err := json.Marshal(request)
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				if rc := Run([]string{"aws"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
					t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
				}
				var answer callbroker.Answer
				if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
					t.Fatal(err)
				}
				if answer.Version != typedProtocolVersion || answer.Classification == nil {
					t.Fatalf("not advertised/classified: %+v", answer)
				}
				answers = append(answers, answer)
			}
			legacy, typed := answers[0].Classification, answers[1].Classification
			if len(legacy.ReadDescriptors) != 0 || len(typed.ReadDescriptors) != 1 || typed.ReadDescriptors[0].Cell != tc.cell {
				t.Fatalf("body does not follow request version: %+v %+v", legacy, typed)
			}
			if !reflect.DeepEqual(legacy.Argv, typed.Argv) || !reflect.DeepEqual(legacy.Reads, typed.Reads) || legacy.StdinSHA256 != typed.StdinSHA256 {
				t.Fatal("naming changed canonical argv, digest or resource admission")
			}
		})
	}
}

func TestAWSVersionTwoDoesNotAddGrammarOrCredentialRoutes(t *testing.T) {
	for _, argv := range [][]string{
		{"s3", "sync", "s3://bkt", "s3://other"},
		{"s3api", "get-object", "--bucket", "bkt", "--key", "x"},
		{"ce", "get-reservation-utilization"},
		{"s3", "ls", "s3://bkt/", "--profile", "host"},
		{"s3", "ls", "s3://bkt/team-mate/"},
		{"s3", "ls", "s3://bkt/team/", "--endpoint-url=https://example.invalid"},
	} {
		request := input([]string{"s3://bkt/team/", "ce://host-login"}, argv...)
		request.Version = typedProtocolVersion
		raw, _ := json.Marshal(request)
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"aws"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
			t.Fatalf("unexpected dispatch failure: %s", stderr.String())
		}
		var answer callbroker.Answer
		if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil || answer.Refused == nil || answer.Classification != nil {
			t.Fatalf("expanded grammar/authority: %+v %v", answer, err)
		}
	}
}
