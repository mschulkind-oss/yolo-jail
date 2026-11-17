package awsclassify

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func TestCECanonicalRowsRequireTheirOwnRoot(t *testing.T) {
	rows := []struct {
		op         string
		args, want []string
	}{
		{"get-cost-and-usage", []string{"--metrics", "UsageQuantity", "UnblendedCost", "--granularity=MONTHLY", "--time-period", "End=2026-02-01,Start=2026-01-01"}, []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "MONTHLY", "--metrics", "UnblendedCost", "UsageQuantity"}},
		{"get-cost-forecast", []string{"--metric", "UNBLENDED_COST", "--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "DAILY"}, []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--granularity", "DAILY", "--metric", "UNBLENDED_COST"}},
		{"get-dimension-values", []string{"--dimension", "SERVICE", "--time-period", `{"End":"2026-02-01","Start":"2026-01-01"}`}, []string{"--time-period", "Start=2026-01-01,End=2026-02-01", "--dimension", "SERVICE", "--context", "COST_AND_USAGE"}},
		{"get-tags", []string{"--time-period=Start=2026-01-01,End=2026-02-01"}, []string{"--time-period", "Start=2026-01-01,End=2026-02-01"}},
	}
	for _, row := range rows {
		t.Run(row.op, func(t *testing.T) {
			argv := append([]string{"ce", row.op}, row.args...)
			a := Classify(input([]string{"ce://host-login", "s3://bkt"}, argv...))
			if a.Classification == nil || a.Refused != nil || a.Local || len(a.Lookups) != 0 {
				t.Fatalf("not classified: %+v", a)
			}
			want := append(append([]string{"ce", row.op}, row.want...), "--output", "json", "--no-paginate")
			if !reflect.DeepEqual(a.Classification.Argv, want) {
				t.Fatalf("argv=%q want=%q", a.Classification.Argv, want)
			}
			chain := [][]callbroker.Resource{{res("cost_query", "ce://host-login/"+row.op), res("location", "ce://host-login")}}
			if !reflect.DeepEqual(a.Classification.Reads, chain) || len(a.Classification.Writes) != 0 || a.Classification.StdinSHA256 != testDigest {
				t.Fatalf("classification=%+v", a.Classification)
			}
			for _, roots := range [][]string{nil, {"s3://bkt"}, {"ce://other"}, {"ce://host-login/"}} {
				denied := Classify(input(roots, argv...))
				if denied.Classification != nil || denied.Refused == nil || denied.Refused.Class != "scope" {
					t.Fatalf("roots=%q answer=%+v", roots, denied)
				}
				if !strings.Contains(denied.Refused.NextStep, "brokered.aws.roots") || !strings.Contains(denied.Refused.NextStep, "yolo config apply") {
					t.Fatal(denied.Refused)
				}
			}
		})
	}
}
