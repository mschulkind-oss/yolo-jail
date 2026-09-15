package macosuser

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestRunPlanGrantsAndCarriesOpenAIAuthEndpoint(t *testing.T) {
	const endpoint = "/private/tmp/yolo-services/openai-auth-broker.endpoint"
	env := jsonx.NewOrderedMap()
	env.Set("YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT", endpoint)
	plan := BuildRunPlan("/Users/Shared/proj", jsonx.NewOrderedMap(), []string{"pi"},
		[]string{"pi"}, "/opt/yolo", "", "", HostContext{}, env, nil, nil)

	want := EndpointGrantCommands(endpoint, "")
	if len(plan.StageCommands) < len(want) ||
		!reflect.DeepEqual(plan.StageCommands[len(plan.StageCommands)-len(want):], want) {
		t.Fatalf("endpoint ACL commands are absent or out of order:\n%v", plan.StageCommands)
	}
	if !strings.Contains(plan.EnvFileContent, "YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT=") ||
		!strings.Contains(plan.EnvFileContent, endpoint) {
		t.Fatalf("sandbox env does not carry endpoint path:\n%s", plan.EnvFileContent)
	}
}
