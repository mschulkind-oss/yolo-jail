package callbroker

import (
	"context"
	"testing"

	kit "github.com/mschulkind-oss/yolo-jail/internal/callbrokerconformance"
)

func TestConformanceCEClassificationsAndCoreChains(t *testing.T) {
	f := newS3E2E(t)
	classifier := f.h.classifier
	classifier.Roots = []string{s3Root, "ce://host-login"}
	cases := []kit.ClassificationCase{}
	for _, row := range []struct {
		op   string
		args []string
	}{
		{"get-cost-and-usage", []string{"--time-period", cePeriod, "--granularity", "DAILY", "--metrics", "UnblendedCost"}},
		{"get-cost-forecast", []string{"--time-period", cePeriod, "--granularity", "MONTHLY", "--metric", "NET_UNBLENDED_COST"}},
		{"get-dimension-values", []string{"--time-period", cePeriod, "--dimension", "SERVICE", "--context", "COST_AND_USAGE"}},
		{"get-tags", []string{"--time-period", cePeriod}},
	} {
		argv := append([]string{"ce", row.op}, row.args...)
		canonical := append(append([]string{}, argv...), "--output", "json", "--no-paginate")
		answer := conformanceAnswer(canonical, nil)
		answer.Classification.Reads = [][]Resource{{{Type: "cost_query", ID: "ce://host-login/" + row.op}, {Type: "location", ID: "ce://host-login"}}}
		cases = append(cases, kit.ClassificationCase{Name: row.op, Call: kit.Call{Argv: argv}, Want: conformanceWant(t, answer)})
	}
	if err := kit.CheckClassifications(context.Background(), conformanceClassify(classifier, nil), cases); err != nil {
		t.Fatal(err)
	}
	// ClassifyCLI's production caller already validates every returned chain.
	if f.requests() != 0 || len(f.invocations()) != 0 || len(f.pending()) != 0 {
		t.Fatal("classification had effects or asked for CE approval")
	}
}
