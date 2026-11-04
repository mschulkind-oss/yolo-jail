package check

// packnudge.go is the pack-switch nudge (docs/design/config-user.md §7): `packs` is the only
// switch, so an `allow.<pack>` table naming a pack `packs` does not list selects nothing. The
// check offers the fix first (`yolo pack add`, run in-process), and declined, or with nobody to
// ask, it fails and names the command. The same tables are refused at launch through
// config.AllowPackSwitchErrors, so a preflight and a launch cannot disagree.

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// canAskPackNudge reports whether a question can be seen and answered: a terminal and a human
// report to print it in (the orphan prompt's rule, helpers.go).
func (o *Options) canAskPackNudge() bool {
	return o.Stdin != nil && o.IsTTYStdout() && !outfmt.IsJSON(o.Format) && o.PackAdd != nil
}

func (o *Options) sectionPackNudge(r *reporter, user *jsonx.OrderedMap, src *configSources) {
	listed, err := o.listedPackNames(r)
	if err != nil {
		return // sectionPacks reports an unreadable list
	}
	facts := o.AllowFacts
	if facts == nil {
		facts = config.EmbeddedAllowPackFacts
	}
	var userSrc *config.Sources
	if src != nil {
		userSrc = src.user
	}
	nudges := config.AllowPackNudges(user, userSrc, facts(listed))
	if len(nudges) == 0 {
		return
	}
	r.sectionHeader("Pack switch")
	defer r.blank()
	var answers *bufio.Reader
	for _, n := range nudges {
		msg, step := termsafe.VisibleLines(n.Problem()), termsafe.VisibleLines(n.NextStep())
		switch n.Kind {
		case config.AllowNudgeLoginNoProfile:
			r.warn(msg, step)
		case config.AllowNudgeUnknown, config.AllowNudgeOffNotBool, config.AllowNudgeOffNoSwitch:
			r.fail(msg, step)
		default:
			r.line(fmt.Sprintf("%s: [allow.%s] is set, but %s is not in packs.", n.Location, termsafe.Visible(n.Table), n.Pack))
			if o.canAskPackNudge() {
				if answers == nil {
					answers = bufio.NewReader(o.Stdin)
				}
				r.line("  Add it to packs? [y/N] ")
				answer, _ := answers.ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(answer)); a == "y" || a == "yes" {
					if err := o.PackAdd(n.Pack); err != nil {
						r.fail(termsafe.VisibleLines(err.Error()), step)
					} else {
						r.ok("Added " + n.Pack + " to packs (yolo pack add " + n.Pack + "); re-run `yolo check` to see what it delivers")
					}
					continue
				}
			}
			r.fail(msg, step)
		}
	}
}

func (o *Options) listedPackNames(r *reporter) ([]string, error) {
	if o.ListedPacks != nil {
		return o.ListedPacks()
	}
	return config.ListedPackNames(r.configWarn)
}
