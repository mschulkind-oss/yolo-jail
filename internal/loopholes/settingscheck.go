package loopholes

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// SettingsCheckResult is the bounded outcome of a manifest-declared pure validator.
type SettingsCheckResult struct {
	Outcome  hostservice.CommandOutcome
	Reason   string
	Remedy   string
	ExitCode int
}

// RunSettingsCheck validates exactly frozen, newline-terminated settings bytes. The private input
// is removed on every return; the original frozen bytes remain with the caller for publication.
func RunSettingsCheck(lp *Loophole, frozen []byte) SettingsCheckResult {
	if lp == nil || lp.HostDaemon == nil || len(lp.HostDaemon.SettingsCheck) == 0 {
		return SettingsCheckResult{Outcome: hostservice.CommandAccepted, ExitCode: 0}
	}
	snapshot, cleanup, err := WritePrivateSettingsSnapshot(lp, frozen)
	if err != nil {
		return SettingsCheckResult{
			Outcome:  hostservice.CommandStartFailed,
			Reason:   "The settings validator input could not be prepared.",
			Remedy:   "Check host storage permissions and retry the launch.",
			ExitCode: -1,
		}
	}
	defer cleanup()
	argv := append([]string(nil), lp.HostDaemon.SettingsCheck...)
	for i := range argv {
		argv[i] = strings.ReplaceAll(argv[i], loopholedecl.TokenSettings, snapshot)
	}
	argv = execx.SelfExecArgv(argv)
	result := hostservice.RunBoundedCommand(argv, hostservice.SettingsCheckTimeout)
	out := SettingsCheckResult{Outcome: result.Outcome, ExitCode: result.ExitCode}
	switch result.Outcome {
	case hostservice.CommandAccepted:
		return out
	case hostservice.CommandTimedOut:
		out.Reason = "The settings validator did not finish within " + hostservice.SettingsCheckTimeout.String() + "."
		// Not "correct the settings": nothing says they are wrong. A validator reads one file, so a
		// second timeout on an idle host is the pack's to fix. No service bypass is offered as the
		// repair (docs/design/host-service-startup-diagnostics.md §3.1).
		out.Remedy = "Retry the launch; if the validator times out again on an idle machine, report it " +
			"to the maintainer of the pack that ships " + lp.Name + "."

	case hostservice.CommandStartFailed:
		out.Reason = "The settings validator could not be started."
		// Not the settings either: the pack's validator program is missing or not executable.
		out.Remedy = "Report it to the maintainer of the pack that ships " + lp.Name + ", then retry the launch."
	case hostservice.CommandRefused:
		out.Reason, out.Remedy = settingsCheckDiagnostic(result.Stdout, result.Stderr)
	}
	if out.Reason == "" {
		out.Reason = "The settings validator refused this configuration without a diagnostic."
	}
	if out.Remedy == "" {
		out.Remedy = "Correct the host service settings, run `yolo check --no-build`, then retry."
	}
	return out
}

func settingsCheckDiagnostic(stdout, stderr string) (reason, remedy string) {
	// A settings validator may return a structured safe diagnostic. Invalid or legacy text is
	// treated as one opaque reason, never as a path to inspect or a log to parse.
	for _, raw := range []string{stdout, stderr} {
		var message struct {
			Reason string `json:"reason"`
			Remedy string `json:"remedy"`
		}
		dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&message); err == nil {
			var trailing any
			if dec.Decode(&trailing) == io.EOF && message.Reason != "" {
				return hostservice.SafeCommandText(message.Reason), hostservice.SafeCommandText(message.Remedy)
			}
		}
		if text := hostservice.SafeCommandText(raw); text != "" {
			if reason == "" {
				reason = text
			} else {
				reason = hostservice.SafeCommandText(reason + " " + text)
			}
		}
	}
	return reason, remedy
}

// SettingsCheckMessage writes the one JSON record understood by settingsCheckDiagnostic. The pack
// owns the wording and must project secret-bearing resolver errors to fixed safe strings first.
func SettingsCheckMessage(reason, remedy string) []byte {
	body, _ := json.Marshal(struct {
		Reason string `json:"reason"`
		Remedy string `json:"remedy"`
	}{reason, remedy})
	return body
}
