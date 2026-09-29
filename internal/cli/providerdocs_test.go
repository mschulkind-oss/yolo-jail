package cli

// providerdocs_test.go is the DRIFT GATE between a provider's two schemas and `yolo config-ref`
// (config_ref.txt): every field of a pack's provider contribution appears in the `provider` kind
// entry, and every key a user's `providers.<name>` entry accepts has its own row under
// `providers`. A review of the OQ-BR6 build found `region_env_name` documented only in a doc
// comment and a reference doc, and "no coverage test catches the gap, so the suite stays green";
// this is that test. It reads the lists the code accepts, so a field added tomorrow fails here
// until it is documented.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// providerKindEntry is config-ref's `provider` kind entry: its row and every continuation line
// (indented to the description column) under it.
func providerKindEntry(t *testing.T) string {
	t.Helper()
	lines := strings.Split(configRefContent, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "      provider ") {
			continue
		}
		entry := []string{l}
		for _, next := range lines[i+1:] {
			if !strings.HasPrefix(next, strings.Repeat(" ", 20)) {
				break
			}
			entry = append(entry, next)
		}
		return strings.Join(entry, "\n")
	}
	t.Fatal("config_ref.txt has no `provider` kind entry")
	return ""
}

// Every ProviderContribution field, spelled by the JSON name a manifest writes it under (the
// matching Contribution field's tag), is named in the kind entry.
func TestEveryProviderContributionFieldIsDocumented(t *testing.T) {
	entry := providerKindEntry(t)
	projection := reflect.TypeOf(packdecl.ProviderContribution{})
	contribution := reflect.TypeOf(packdecl.Contribution{})
	for i := 0; i < projection.NumField(); i++ {
		name := projection.Field(i).Name
		f, ok := contribution.FieldByName(name)
		if !ok {
			t.Errorf("ProviderContribution.%s has no Contribution field to read its JSON name from", name)
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(tag) + `\b`).MatchString(entry) {
			t.Errorf("the provider kind's %q is not named in config-ref's `provider` entry:\n%s", tag, entry)
		}
	}
}

// Every key a user's provider entry accepts has its own row in the `providers` section.
func TestEveryUserProviderKeyIsDocumented(t *testing.T) {
	start := strings.Index(configRefContent, "[bold]providers[/bold] (object)")
	if start < 0 {
		t.Fatal("config_ref.txt has no `providers` section")
	}
	section := configRefContent[start:]
	if end := strings.Index(section, "\n  [bold]"); end > 0 {
		section = section[:end]
	}
	row := regexp.MustCompile(`(?m)^      ([a-z_]+)\s{2,}`)
	documented := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}
	for _, key := range config.KnownProviderKeys() {
		if !documented[key] {
			t.Errorf("`providers.<name>.%s` is accepted and has no row under config-ref's `providers`", key)
		}
	}
}
