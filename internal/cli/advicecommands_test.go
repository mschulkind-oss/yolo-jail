package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// adviceArgv splits a `yolo config ...` command that a message prints into the arguments the
// verb receives: words split on spaces, with a single-quoted run kept whole and unquoted, as a
// POSIX shell does for the commands these messages print.
func adviceArgv(t *testing.T, command string) []string {
	t.Helper()
	var argv []string
	var cur strings.Builder
	inWord, quoted := false, false
	for _, r := range command {
		switch {
		case quoted && r == '\'':
			quoted = false
		case quoted:
			cur.WriteRune(r)
		case r == '\'':
			quoted, inWord = true, true
		case r == ' ':
			if inWord {
				argv = append(argv, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		argv = append(argv, cur.String())
	}
	if len(argv) < 2 || argv[0] != "yolo" || argv[1] != "config" {
		t.Fatalf("%q is not a `yolo config` command", command)
	}
	return argv[2:]
}

// The commands the host-management and host-floor messages print are run as printed, against a
// TOML user config, and must write the setting the message promises. The messages once gave a
// JSON snippet that a TOML file cannot take, and no verb could write a per-pack `host_floor`.
func TestTheHostAdviceCommandsWriteWhatTheyPromise(t *testing.T) {
	for _, format := range []configdoc.Format{configdoc.TOML} {
		t.Run("toml", func(t *testing.T) {
			user, _ := genericFixture(t, "packs = []\n", format, "")

			rc, out, errs := runConfigVerb(t, adviceArgv(t, config.HostManagementOwnCommand)...)
			assertPublicPostWriteCheck(t, rc, out, errs)
			if got := config.HostManagementMode(); got != config.HostManagementOwn {
				t.Fatalf("after `%s`, host_management = %q; want own\nstdout=%q stderr=%q",
					config.HostManagementOwnCommand, got, out, errs)
			}

			cmd := config.HostFloorLeaveOutCommand("claude")
			rc, out, errs = runConfigVerb(t, adviceArgv(t, cmd)...)
			assertPublicPostWriteCheck(t, rc, out, errs)
			if got := strings.ReplaceAll(config.HostFloorWire(), " ", ""); got != `{"claude":false}` {
				t.Fatalf("after `%s`, host_floor = %q; want claude left out\nstdout=%q stderr=%q\n%s",
					cmd, got, out, errs, readFile(t, user.Path))
			}
		})
	}
}
