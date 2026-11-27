package toolskills

import (
	"fmt"
	"io"
)

// SkipLine is the line for a skill that was not delivered for REASON, naming the next step. The
// host's read (internal/cli/run) and the jail's provisioning step print it, so the two cannot word
// one skip differently. esc is applied to every interpolated string: the host passes its markup
// escaper, the jail passes nil (plain text). The strings must already be sanitized.
func SkipLine(name, tool, reason string, esc func(string) string) string {
	esc = orIdentity(esc)
	return fmt.Sprintf("yolo: mise skill \"%s\" (%s) not delivered: %s. Fix that skill in the tool, "+
		"or remove the tool from your install.mise, then relaunch yolo.", esc(name), esc(tool), esc(reason))
}

// ShadowLine is the line for the loser of a same-name conflict between two allowlisted tools.
func ShadowLine(name, loser, winner string, esc func(string) string) string {
	esc = orIdentity(esc)
	return fmt.Sprintf("yolo: mise skill \"%s\" from %s not delivered: %s comes first in your "+
		"install.mise and wins. Reorder install.mise (`yolo config unset install.mise.<tool> --user`, then "+
		"`yolo config set` it again) to change that.", esc(name), esc(loser), esc(winner))
}

func orIdentity(f func(string) string) func(string) string {
	if f == nil {
		return func(s string) string { return s }
	}
	return f
}

// discloseSkips names every skill the step dropped, in the words the host's read uses: a name
// that failed ValidName, the loser of a same-name conflict between two tools, and a skill the
// copier refused (outside the installs directory, over the cap, an escaping link). Strings are
// sanitized here: a tool and its skills are named by a third party.
func discloseSkips(out io.Writer, rep Report) {
	for _, iv := range rep.Selected.Invalid {
		fmt.Fprintln(out, SkipLine(iv.Name, Sanitize(iv.Tool), "not a usable skill name", nil))
	}
	for _, sh := range rep.Selected.Shadowed {
		fmt.Fprintln(out, ShadowLine(Sanitize(sh.Skill.Name), Sanitize(sh.Skill.Tool), Sanitize(sh.Winner), nil))
	}
	if rep.Snapshot == nil {
		return
	}
	for _, sk := range rep.Snapshot.Skipped {
		fmt.Fprintln(out, SkipLine(Sanitize(sk.Skill.Name), Sanitize(sk.Skill.Tool), Sanitize(sk.Reason), nil))
	}
}
