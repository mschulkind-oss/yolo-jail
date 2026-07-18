package runcmd

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholeResolver returns the config.LoopholeResolver backing _validate_config
// (file-backed discovery, include_disabled=True).
func loopholeResolver() config.LoopholeResolver {
	return loopholes.NewResolver()
}

// collectIdentityEnv ports run()'s identity-env block (lines 1280-1330): the
// `-e YOLO_GIT_NAME/EMAIL` and `-e YOLO_JJ_NAME/EMAIL` flags, from `git config
// --get user.name/email` and `jj config get user.name/email`. Missing tool /
// empty value / any error is silently skipped (the Python `except Exception:
// pass`). jj values are additionally stripped of surrounding double quotes.
func (o *Options) collectIdentityEnv() []string {
	var env []string
	add := func(varName string, argv []string, stripQuotes bool) {
		res := o.Exec(argv, "", nil, 30*time.Second)
		if !res.Ran || res.Timeout || res.RC != 0 {
			return
		}
		val := strings.TrimSpace(res.Stdout)
		if stripQuotes {
			val = strings.Trim(val, `"`)
		}
		if val != "" {
			env = append(env, "-e", varName+"="+val)
		}
	}
	add("YOLO_GIT_NAME", []string{"git", "config", "--get", "user.name"}, false)
	add("YOLO_GIT_EMAIL", []string{"git", "config", "--get", "user.email"}, false)
	add("YOLO_JJ_NAME", []string{"jj", "config", "get", "user.name"}, true)
	add("YOLO_JJ_EMAIL", []string{"jj", "config", "get", "user.email"}, true)
	return env
}
