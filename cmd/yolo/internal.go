package main

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// runInternal dispatches the hidden `yolo internal <cmd>` family — debugging
// tooling for config inspection.
func runInternal(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal <config-dump> [args...]")
		return 2
	}
	switch args[0] {
	case "config-dump":
		return runConfigDump(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yolo internal: unknown command %q\n", args[0])
		return 2
	}
}

// runConfigDump loads + merges the config for a workspace (default: cwd) via
// internal/config and prints the merged config as canonical snapshot JSON,
// followed by the validation errors/warnings. Used for differential testing
// and for eyeballing the merged shape.
//
// Flags: --strict (raise on malformed config), positional workspace dir.
func runConfigDump(args []string) int {
	strict := false
	workspace := ""
	for _, a := range args {
		switch {
		case a == "--strict":
			strict = true
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "config-dump: unknown flag %q\n", a)
			return 2
		default:
			workspace = a
		}
	}
	if workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	}

	cfg, err := config.LoadConfig(workspace, strict, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	errs, warns := config.ValidateConfig(cfg, workspace, nil)

	out := jsonx.NewOrderedMap()
	out.Set("config", cfg)
	out.Set("errors", strAny(errs))
	out.Set("warnings", strAny(warns))
	snap, err := config.SnapshotJSON(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	fmt.Println(snap)
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func strAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
