package cli

// configrefcoverage_test.go is enforcement item 3 of
// docs/reference/self-documenting-cli.md, and the doc calls it "the highest-leverage
// single test given config-ref is hand-maintained" for a plain reason: `yolo
// config-ref` is the CLI's only concept surface for the config schema, an
// in-jail agent has nothing else to read, and the schema and the text that
// documents it are two hand-edited lists in two packages. Nothing has ever
// compared them.
//
// It reads the schema's own key set (config.TopLevelConfigKeys) rather than a
// list retyped here, because a retyped list is exactly the drift this test
// exists to catch — it would go stale in the same commit as config-ref and agree
// with it forever after.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// configRefText is the raw embedded reference, tags and all — the same bytes
// `yolo config-ref` renders. Read raw rather than stripped so the
// `[bold]<key>[/bold]` section titles below are still findable.
func configRefText(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(configRefContent) == "" {
		t.Fatal("config_ref.txt embedded empty; every assertion here would pass vacuously")
	}
	return configRefContent
}

// TestConfigRefDocumentsEveryLiveKey: every key yolo ACCEPTS appears literally
// in the reference.
//
// MEASURED 2026-09-09, before this test existed: `required_capabilities` was
// accepted by the schema and documented nowhere in config_ref.txt — so the one
// surface an agent can interrogate about the config was silent about a live key.
// (The design doc predicted `repo_path`, `host_processes` and `prune` instead;
// all three have since been resolved, two by documentation and one by
// retirement. That is the argument for deriving the check rather than listing
// the gaps: the list of gaps was wrong within weeks, and the derivation was not.)
func TestConfigRefDocumentsEveryLiveKey(t *testing.T) {
	ref := configRefText(t)
	for _, key := range config.TopLevelConfigKeys() {
		if !strings.Contains(ref, key) {
			t.Errorf("`yolo config-ref` never mentions the accepted config key %q. "+
				"config-ref is the CLI's only concept surface for the schema, so an "+
				"undocumented key is a key an in-jail agent cannot discover at all.", key)
		}
	}
}

// TestConfigRefAnnouncesTheRefusalForEveryRetiredKey is the other direction, and
// it is deliberately not "a retired key must not appear".
//
// A retired key SHOULD have a section: someone who finds the old spelling in an
// old config comes to config-ref to learn what happened to it, and config_ref.txt
// already does this well — `host_processes` and `journal` each keep a titled entry
// whose first words are "REMOVED — a config carrying it is REFUSED", followed by
// the block that replaces it. The bar is therefore not the section's PRESENCE but
// its CONTENT: it must announce the refusal, so nobody reads a titled entry as an
// invitation to configure something yolo rejects.
func TestConfigRefAnnouncesTheRefusalForEveryRetiredKey(t *testing.T) {
	sections := configRefSectionBodies(t)
	for _, key := range config.RetiredConfigKeys() {
		body, documented := sections[key]
		if !documented {
			// Silence is acceptable — the key is gone. What is not acceptable is a
			// section that reads like a live one.
			continue
		}
		if !strings.Contains(body, "REMOVED") && !strings.Contains(body, "RETIRED") &&
			!strings.Contains(body, "REFUSED") {
			t.Errorf("config-ref gives the RETIRED key %q a section that never says it "+
				"is refused. Writing it is an error, so a titled entry that reads "+
				"like a live key sends a reader to configure something yolo rejects.\n"+
				"--- section ---\n%s", key, body)
		}
	}
}

// TestConfigRefKeySectionsAreAllKnown catches drift the other way: a bold-titled
// key section for something the schema does not accept. A reader following it
// writes a key and gets an unknown-key error from the very tool that documented
// it.
func TestConfigRefKeySectionsAreAllKnown(t *testing.T) {
	live := map[string]bool{}
	for _, k := range config.TopLevelConfigKeys() {
		live[k] = true
	}
	retired := map[string]bool{}
	for _, k := range config.RetiredConfigKeys() {
		retired[k] = true
	}

	sections := configRefSectionBodies(t)
	if len(sections) == 0 {
		t.Fatal("parsed no `[bold]<key>[/bold] (<type>)` sections out of config-ref — " +
			"the format changed and this whole file has stopped testing anything")
	}
	for key := range sections {
		if live[key] || retired[key] {
			continue
		}
		t.Errorf("config-ref documents %q as a top-level key, which the schema does "+
			"not accept — a reader who follows it gets an unknown-key error from "+
			"yolo itself", key)
	}
}

// configRefSectionBodies maps each top-level key config-ref documents to the
// text of its section — the `  [bold]<name>[/bold] (<type>): …` line plus the
// indented lines under it, up to the next such title.
//
// DOTTED NAMES ARE SKIPPED. config_ref.txt titles some sub-keys the same way
// (`network.mode`, `security.blocked_tools`), and those are documentation of a
// nested field, not a claim about a top-level key — reading them as top-level is
// how this parser's first cut reported four false "the schema does not accept
// this" failures against text that was entirely correct.
func configRefSectionBodies(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	cur := ""
	var body []string
	flush := func() {
		if cur != "" {
			out[cur] = strings.Join(body, "\n")
		}
		cur, body = "", nil
	}
	for _, line := range strings.Split(configRefText(t), "\n") {
		if name, ok := sectionTitle(line); ok {
			flush()
			if strings.Contains(name, ".") {
				continue
			}
			cur = name
			body = []string{line}
			continue
		}
		if cur != "" {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// sectionTitle recognizes a `  [bold]<name>[/bold] (<type>)` key title at the
// two-space top-level indent and returns the name.
func sectionTitle(line string) (string, bool) {
	if !strings.HasPrefix(line, "  [bold]") || strings.HasPrefix(line, "   ") {
		return "", false
	}
	name, after, ok := strings.Cut(strings.TrimPrefix(line, "  [bold]"), "[/bold]")
	if !ok || !strings.HasPrefix(after, " (") {
		return "", false
	}
	// A prose heading like "[bold]Two scopes[/bold]" is not a key.
	if name == "" || strings.ContainsAny(name, " \t") {
		return "", false
	}
	return name, true
}
