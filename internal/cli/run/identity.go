package run

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholeResolver returns the config.LoopholeResolver backing config validation
// (file-backed discovery, include_disabled=True).
func loopholeResolver() config.LoopholeResolver {
	return loopholes.NewResolver()
}

// collectIdentityEnv builds the identity-env block: the
// `-e YOLO_GIT_NAME/EMAIL` flags, from `git config --get user.name/email`.
// Missing tool / empty value / any error is silently skipped.
func (o *Options) collectIdentityEnv() []string {
	var env []string
	add := func(varName string, argv []string) {
		res := o.Exec(argv, "", nil, 30*time.Second)
		if !res.Ran || res.Timeout || res.RC != 0 {
			return
		}
		val := strings.TrimSpace(res.Stdout)
		if val != "" {
			env = append(env, "-e", varName+"="+val)
		}
	}
	add("YOLO_GIT_NAME", []string{"git", "config", "--get", "user.name"})
	add("YOLO_GIT_EMAIL", []string{"git", "config", "--get", "user.email"})
	return env
}
