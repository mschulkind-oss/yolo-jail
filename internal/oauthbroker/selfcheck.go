package oauthbroker

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixdiag"
)

// selfCheckLine is one graded line of --self-check output: the wire's three
// levels ("FAIL", "NOTE", "OK") plus the continuation lines printed beneath the
// title. `yolo check` parses exactly this back out (nixdiag.SplitSelfCheckLines),
// so a detail written here reaches the user as the finding's remediation note.
type selfCheckLine struct {
	grade  string
	title  string
	detail string
}

// SelfCheck is the `yolo doctor` health check behind the broker loophole's
// doctor_cmd. Distinguishes fail (rc=1), warn (rc=0 + NOTE lines), and ok (rc=0).
// Emits FAIL:/NOTE:/OK: lines; openssl is only a failure when CA/leaf state is
// missing. Prints to stdout and returns the exit code.
//
// The shared-credentials FRESHNESS grading lives here, and that is the whole
// point of this function existing rather than `yolo check` doing it: `check`
// used to stat and parse the shared `.credentials.json` itself, reaching into
// `claudeAiOauth.expiresAt` and hardcoding this daemon's remediation strings —
// a third copy of a schema core has no business knowing, inside the one command
// that is supposed to be agent-agnostic. `doctor_cmd` was already the declared
// extension point for exactly this (the manifest has named
// `["yolo","internal","daemon","claude-oauth-broker","--self-check"]` all along),
// so the grading moved behind it rather than the loophole growing a second,
// core-side check. See docs/design/pack-code-separation.md §4.
func SelfCheck(credsPath string) int {
	dir := BrokerDir()
	var lines []selfCheckLine
	note := func(title, detail string) {
		lines = append(lines, selfCheckLine{grade: "NOTE", title: title, detail: detail})
	}

	missingState := false
	if !isFile(caCrt(dir)) {
		note(caCrt(dir)+" not yet generated — run `--init-ca` or `just deploy`", "")
		missingState = true
	}
	if !isFile(serverCrt(dir)) {
		note(serverCrt(dir)+" not yet generated — run `--init-ca` or `just deploy`", "")
		missingState = true
	}
	if resolveOpenssl() == "" && missingState {
		lines = append(lines, selfCheckLine{grade: "FAIL",
			title: "openssl not on PATH and no CA/leaf state yet — install openssl so `--init-ca` can run"})
	}

	lines = append(lines, gradeSharedCreds(credsPath, time.UnixMilli(nowMS()))...)

	failed, warned := 0, 0
	for _, want := range []string{"FAIL", "NOTE", "OK"} {
		for _, l := range lines {
			if l.grade != want {
				continue
			}
			switch l.grade {
			case "FAIL":
				failed++
			case "NOTE":
				warned++
			}
			fmt.Println(l.grade + ": " + l.title)
			for _, d := range strings.Split(l.detail, "\n") {
				if strings.TrimSpace(d) != "" {
					fmt.Println("  " + strings.TrimSpace(d))
				}
			}
		}
	}

	if failed > 0 {
		return 1
	}
	if warned > 0 {
		fmt.Println("OK (broker present; state not yet primed)")
		return 0
	}
	fmt.Println("OK")
	return 0
}

// gradeSharedCreds grades the shared credentials file's expiry against now,
// using the file mtime as a "time since last refresh" proxy.
//
// Ported verbatim (message text included) from `yolo check`'s
// checkBrokerCredsFreshness, which is deleted: the REMAINING LIFETIME is the
// useful half of this check, so it survives as the text of an OK:/NOTE:/FAIL:
// line rather than as a pass/fail bit. The grade thresholds are the broker's own
// operational contract — a healthy refresh cadence keeps the token above an hour
// of remaining life, so dipping below that is a warning even though nothing has
// broken yet, and crossing zero means refreshes are not landing at all.
func gradeSharedCreds(credsPath string, now time.Time) []selfCheckLine {
	info, err := os.Stat(credsPath)
	if err != nil {
		return []selfCheckLine{{grade: "NOTE",
			title: credsPath + " does not exist — run Claude and `/login` first"}}
	}
	raw, rerr := os.ReadFile(credsPath)
	if rerr != nil {
		return []selfCheckLine{{grade: "FAIL", title: fmt.Sprintf("%s: %s", credsPath, rerr)}}
	}
	if strings.TrimSpace(string(raw)) == "" {
		// The documented pre-login placeholder: `yolo run` creates the file empty
		// so the bind mount has something to bind. Not a finding of any grade.
		return nil
	}
	oauth, derr := oauthFromCredsBytes(raw)
	if derr != nil {
		return []selfCheckLine{{grade: "FAIL", title: fmt.Sprintf("%s: %s", credsPath, derr)}}
	}
	v, ok := oauth.Get("expiresAt")
	if !ok {
		return []selfCheckLine{{grade: "NOTE",
			title:  "shared creds " + credsPath + ": no claudeAiOauth.expiresAt",
			detail: "The file parsed but carries no OAuth expiry — `/login` has not completed."}}
	}
	expiresAtMS, ok := asInt64(v)
	if !ok {
		return []selfCheckLine{{grade: "NOTE",
			title:  "shared creds " + credsPath + ": no claudeAiOauth.expiresAt",
			detail: "The file parsed but carries no OAuth expiry — `/login` has not completed."}}
	}

	remainingS := int((expiresAtMS - now.UnixMilli()) / 1000)
	mtimeAgeS := int(now.Sub(info.ModTime()).Seconds())
	if mtimeAgeS < 0 {
		mtimeAgeS = 0
	}
	lastWrite := "last write " + nixdiag.FmtDuration(mtimeAgeS) + " ago"

	switch {
	case remainingS < 0:
		return []selfCheckLine{{grade: "FAIL",
			title: "shared creds expired " + nixdiag.FmtDuration(-remainingS) +
				" ago (" + lastWrite + ")",
			detail: "Refreshes are not landing.  Run /login from inside a " +
				"jail to recover; check broker log at " +
				"~/.local/share/yolo-jail/logs/host-service-claude-oauth-broker.log"}}
	case remainingS < 3600:
		return []selfCheckLine{{grade: "NOTE",
			title: "shared creds expire in " + nixdiag.FmtDuration(remainingS) +
				" (" + lastWrite + ")",
			detail: "Approaching expiry without a refresh having landed.  " +
				"Healthy cadence keeps this above 1h."}}
	default:
		return []selfCheckLine{{grade: "OK",
			title: "shared creds valid for " + nixdiag.FmtDuration(remainingS) +
				", " + lastWrite}}
	}
}
