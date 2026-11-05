package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

func TestResourceGroupsOwnerSetAdviceCommandIsRegisteredAndTyped(t *testing.T) {
	user, ws := genericFixture(t, "# keep user\n", configdoc.TOML, "# keep project\n")
	const chosenUnit = "owner-choice.slice"
	rc, out, errs := runConfigVerb(t, "set", "resource_groups.default", `"`+chosenUnit+`"`, "--user")
	assertPublicPostWriteCheck(t, rc, out, errs)
	if !strings.Contains(out, user.Path) || !strings.Contains(out, `default = "`+chosenUnit+`"`) {
		t.Fatalf("owner set command tokens did not write selected owner value: %q", out)
	}
	got := assertValidFile(t, user.Path, configdoc.TOML, "# keep user", ws.Root)
	if !strings.Contains(got, `[resource_groups]`) || !strings.Contains(got, `default = "`+chosenUnit+`"`) || strings.Contains(got, "project-bad.slice") {
		t.Fatalf("owner config did not preserve only the independently selected unit:\n%s", got)
	}
	cfg, _, err := config.LoadConfigWithSources(ws.Root, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := config.SelectResourceGroup(cfg, ws.Root)
	if err != nil || selection.Unit != chosenUnit || selection.Source != "default" {
		t.Fatalf("loaded owner resource default = %#v, %v; want %q", selection, err, chosenUnit)
	}
}
