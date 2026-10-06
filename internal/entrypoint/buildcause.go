package entrypoint

// buildcause.go is a MISSING BUILD'S CAUSE as the host's build act states it, for every reader of
// it: the launch's refusal of a missing patched build (internal/cli/run's missingbuilds.go,
// docs/design/patched-extensions.md PPX-D40, patched-forks.md PF-D77), its warnings, and the jail's
// launcher gate (treeGateFor), which is handed it in YOLO_PATCHED_TREES. A BUILD CAUSE (a term
// coined here) is the cause alone, in plain words and with no extension key in it, so several
// builds that failed one way share one and are said once with the list of what they left without
// a build (PPX-D42).

import "strings"

// BuildCause is why a build left nothing, as its act found it.
type BuildCause struct {
	// Lines are the cause, one line each, the second and later indented under the first as they
	// print; never a key, so builds that met one cause have equal Lines.
	Lines []string `json:"lines,omitempty"`
	// YoloBug says the cause is yolo's, not the pack's or the user's: the build jail refused a
	// config the user's own launch accepts (its seal mounted read-only a directory a selected pack
	// writes, say), so what the user can do is work around it and report it, never fix it.
	YoloBug bool `json:"yolo_bug,omitempty"`
	// Packs are the packs its build jail was sealed to, which a yolo bug is not the fault of.
	Packs []string `json:"packs,omitempty"`
	// Log is the build's own log, where its whole output is, "" for none.
	Log string `json:"log,omitempty"`
}

// Same reports whether c and o are one cause: the same words, owned by the same party, of a jail
// sealed to the same packs. Their logs may differ.
func (c *BuildCause) Same(o *BuildCause) bool {
	if c == nil || o == nil {
		return c == o
	}
	return c.YoloBug == o.YoloBug && strings.Join(c.Lines, "\n") == strings.Join(o.Lines, "\n") &&
		strings.Join(c.Packs, ",") == strings.Join(o.Packs, ",")
}

// says reports whether c has anything to say beyond its build's reason: lines, or whose bug it is.
func (c *BuildCause) says() bool { return c != nil && (len(c.Lines) > 0 || c.YoloBug) }

// WhoFixes is who can fix the cause and how, one line each: for a yolo bug, that it is one, whose
// it is not, and where to report it; for anything else, to fix what it names and then retry, as
// retry says ("`yolo capture <key>` builds it; the next fresh launch tries too").
func (c *BuildCause) WhoFixes(retry string) []string {
	if c == nil || !c.YoloBug {
		return []string{"Fix what it names, then " + retry + "."}
	}
	whose := "the build jail refused a config your own launch accepts"
	if len(c.Packs) > 0 {
		whose = "not in pack " + joinAnd(c.Packs) + ": " + whose
	}
	report := "Report it at " + IssuesURL
	if c.Log != "" {
		report += ", with " + c.Log
	}
	return []string{"This is a bug in yolo, " + whose + ".", report + "; once it is fixed, " + retry + "."}
}

// joinAnd is "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// JoinAnd is joinAnd for the host's lines.
func JoinAnd(items []string) string { return joinAnd(items) }
