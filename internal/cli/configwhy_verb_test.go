package cli

import (
	"strings"
	"testing"
)

// OQ-RC10: an unset key that has a named verb is pointed at that verb with the right
// arguments, and only a key with none gets the generic `yolo config set`.
func TestConfigWhyNamesTheVerbOfAnUnsetKey(t *testing.T) {
	newNamedVerbFixture(t, "", "")
	for key, want := range map[string]string{
		"allow.git.always":                   "set it with `yolo allow git always <capability>`",
		"allow.git.on_request":               "set it with `yolo allow git on_request <capability>`",
		"allow.github":                       "set it with `yolo allow github <capability>`",
		"requests.git.push":                  "set it with `yolo request git push <ref>`",
		"requests.github.repos":              "set it with `yolo request github repos <OWNER/REPO>`",
		"requests.git.new_branches":          "set it with `yolo request git new_branches`",
		"requests.github.dispatch_workflows": "set it with `yolo request github dispatch_workflows <workflow file>`",
		"active_provider.claude":             "set it with `yolo active-provider claude <provider|native>`",
		"active_provider.default":            "set it with `yolo active-provider default <provider|native>`",

		"packs[5]":                 "set it with `yolo pack add <pack>`",
		"machine.resources.memory": "set it with `yolo config set machine.resources.memory <value>`",
	} {
		rc, out, errs := captureStd(t, func() int { return registry["config"]([]string{"config", "why", key}) })
		if rc != 0 || !strings.Contains(out, want) {
			t.Fatalf("config why %s rc=%d stdout=%q stderr=%q; want %q", key, rc, out, errs, want)
		}
		if !strings.Contains(want, "yolo config set") && strings.Contains(out, "yolo config set") {
			t.Fatalf("config why %s offers the generic verb beside the named one: %q", key, out)
		}
	}
}
