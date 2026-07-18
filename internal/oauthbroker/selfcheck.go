package oauthbroker

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// SelfCheck is the `yolo doctor` health check. Distinguishes fail (rc=1),
// warn (rc=0 + NOTE lines), and ok (rc=0). Mirrors self_check, including the
// exact FAIL:/NOTE:/OK print lines and the openssl-only-fails-if-state-missing
// logic. Prints to stdout/stderr and returns the exit code.
func SelfCheck(credsPath string) int {
	dir := BrokerDir()
	var warnings, failures []string

	if !isFile(caCrt(dir)) {
		warnings = append(warnings, caCrt(dir)+" not yet generated — run `--init-ca` or `just deploy`")
	}
	if !isFile(serverCrt(dir)) {
		warnings = append(warnings, serverCrt(dir)+" not yet generated — run `--init-ca` or `just deploy`")
	}
	if resolveOpenssl() == "" {
		if len(warnings) > 0 {
			failures = append(failures,
				"openssl not on PATH and no CA/leaf state yet — install openssl so `--init-ca` can run")
		}
	}

	if _, err := os.Stat(credsPath); err == nil {
		raw, rerr := os.ReadFile(credsPath)
		if rerr != nil {
			failures = append(failures, fmt.Sprintf("%s: %s", credsPath, rerr))
		} else if strings.TrimSpace(string(raw)) != "" {
			if _, derr := jsonx.Decode(raw); derr != nil {
				failures = append(failures, fmt.Sprintf("%s: %s", credsPath, derr))
			}
		}
	} else {
		warnings = append(warnings, credsPath+" does not exist — run Claude and `/login` first")
	}

	if len(failures) > 0 {
		for _, p := range failures {
			fmt.Println("FAIL: " + p)
		}
		for _, p := range warnings {
			fmt.Println("NOTE: " + p)
		}
		return 1
	}
	if len(warnings) > 0 {
		for _, p := range warnings {
			fmt.Println("NOTE: " + p)
		}
		fmt.Println("OK (broker present; state not yet primed)")
		return 0
	}
	fmt.Println("OK")
	return 0
}
