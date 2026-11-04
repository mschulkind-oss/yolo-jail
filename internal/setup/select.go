package setup

import (
	"fmt"
	"sort"
	"strings"
)

// EnvVar is the environment's selection, second in design §2's order. It is also the
// session marker a launch exports into the session it starts (named-setups-plan.md SUP-D4).
const EnvVar = "YOLO_SETUP"

// Source is where the selection came from, for the launch line's "- from <source>".
type Source int

const (
	FromFlag Source = iota
	FromEnv
	FromUserConfig
	FromDefault
)

func (s Source) String() string {
	switch s {
	case FromFlag:
		return "--setup"
	case FromEnv:
		return EnvVar
	case FromUserConfig:
		return "your user config"
	case FromDefault:
		return "this OS's default"
	}
	return fmt.Sprintf("Source(%d)", int(s))
}

// Select resolves the selection in design §2's order, first match wins: --setup (flag), then
// YOLO_SETUP (env), then the user config's `setup` (userSetup), then this OS's default. user is
// the user's `[setups.*]` tables, already decoded; a built-in name always means the built-in.
//
// Select answers only which setup is named. Whether it can run here is Buildable's question,
// and refusing a host setup outside `yolo host` is the caller's, which knows its verb.
func Select(flag, env, userSetup string, user map[string]Setup, goos string) (Setup, Source, error) {
	for _, c := range []struct {
		name  string
		src   Source
		where string
	}{
		{flag, FromFlag, "--setup"},
		{env, FromEnv, EnvVar},
		{userSetup, FromUserConfig, "your user config's setup"},
	} {
		if c.name == "" {
			continue
		}
		if !ValidName(c.name) {
			return Setup{}, c.src, fmt.Errorf("%s: %s. See the defined setups:\n    yolo setup list", c.where, nameRefusal(c.name))
		}
		if s, ok := builtIn(c.name); ok {
			return s, c.src, nil
		}
		if s, ok := user[c.name]; ok {
			return s, c.src, nil
		}
		return Setup{}, c.src, fmt.Errorf("setup %q is not defined. Defined here: %s (yolo setup list)", c.name, strings.Join(definedNames(user), ", "))
	}
	return Default(goos), FromDefault, nil
}

// definedNames is every name a selection may use: the built-ins in design §5's order, then
// the user's, sorted.
func definedNames(user map[string]Setup) []string {
	var out []string
	for _, s := range builtIns {
		out = append(out, s.Name)
	}
	var mine []string
	for name := range user {
		if !IsBuiltIn(name) {
			mine = append(mine, name)
		}
	}
	sort.Strings(mine)
	return append(out, mine...)
}
