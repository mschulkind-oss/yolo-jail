package config

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestCallBrokerSourceRegistryRejectsDuplicatesAndUnmatchedFollows(t *testing.T) {
	cases := []struct {
		name  string
		known callBrokerFenceResolver
		want  string
	}{
		{
			name: "duplicate call broker sources",
			known: callBrokerFenceResolver{
				"alpha": {Name: "alpha", CallBroker: &loopholedecl.CallBroker{Source: "tasks"}},
				"beta":  {Name: "beta", CallBroker: &loopholedecl.CallBroker{Source: "tasks"}},
			},
			want: "duplicate source \"tasks\"",
		},
		{
			name: "unmatched follows source",
			known: callBrokerFenceResolver{
				"alpha": {Name: "alpha", CallBroker: &loopholedecl.CallBroker{Source: "tasks", Joins: []loopholedecl.CallBrokerJoin{{FollowSource: "missing", FollowFact: "branch"}}}},
			},
			want: "no selected pack declares it",
		},
		{
			name: "legacy source collision",
			known: callBrokerFenceResolver{
				"legacy": {Name: "legacy", Brokered: &loopholedecl.Brokered{Source: "tasks"}},
				"alpha":  {Name: "alpha", CallBroker: &loopholedecl.CallBroker{Source: "tasks"}},
			},
			want: "already owned by legacy broker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_VERSION", "")
			errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), tc.known)
			joined := strings.Join(errs, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("source registry problems %q do not contain %q", joined, tc.want)
			}
		})
	}
}
