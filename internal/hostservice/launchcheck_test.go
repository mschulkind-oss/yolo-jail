package hostservice

import (
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestLaunchCheckBudgetIsTheLaunchesAndCapped: the daemon waits as long as the launch said it
// would, never longer than the cap whoever asks (a jail can send the request too), and falls to
// the launch's default for a missing or unusable value.
func TestLaunchCheckBudgetIsTheLaunchesAndCapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"the launch's own", `{"budget_ms": 2000}`, 2 * time.Second},
		{"a fraction", `{"budget_ms": 150.5}`, 150500 * time.Microsecond},
		{"past the cap", `{"budget_ms": 600000}`, LaunchCheckBudgetCap},
		{"absent", `{}`, LaunchCheckBudget},
		{"zero", `{"budget_ms": 0}`, LaunchCheckBudget},
		{"negative", `{"budget_ms": -5}`, LaunchCheckBudget},
		{"a string", `{"budget_ms": "2000"}`, LaunchCheckBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decoded, err := jsonx.Decode([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			req, _ := decoded.(*jsonx.OrderedMap)
			if got := LaunchCheckBudgetOf(&Session{Request: req}); got != tc.want {
				t.Errorf("budget = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestAnUnknownLaunchCheckIsReadInEachHandlersSpelling: the aws-auth and openai-auth handlers
// print the action bare and the Claude broker quotes it, and each is a daemon too old for the
// check. A failure that merely mentions the action, or another action's refusal, is not.
func TestAnUnknownLaunchCheckIsReadInEachHandlersSpelling(t *testing.T) {
	for line, want := range map[string]bool{
		"unknown action: launch-check":     true,
		"unknown action: 'launch-check'":   true,
		"  unknown action: launch-check  ": true,
		"unknown action: status":           false,
		"launch-check failed: no state":    false,
		"":                                 false,
	} {
		if got := IsUnknownLaunchCheck(line); got != want {
			t.Errorf("IsUnknownLaunchCheck(%q) = %v, want %v", line, got, want)
		}
	}
}
