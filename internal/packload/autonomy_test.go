package packload

import "testing"

// SurfacesFor folds the selected autonomy posture's config-managed keys into the pack's
// OWN surface (deep-merged, posture wins), and LaunchFlagsFor selects the posture's launch
// flags. This is the §4.2 mechanism: autonomy ON (jail) writes the bypass keys + flag;
// autonomy OFF (host) writes the guarded keys and drops the flag. Benign always-safe keys
// in the base surface's managed layer survive both.
func TestAutonomyPostureFoldAndLaunch(t *testing.T) {
	p := &Pack{Name: "claude", Decl: declFrom(t, `{"contributes":[
	  {"kind":"config","config":[{"agent":"claude","name":"settings","codec":"json",
	     "path":"~/.claude/settings.json","managed":{"preferences":{"autoUpdaterStatus":"disabled"}}}]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
	        "managed":{"permissions":{"defaultMode":"acceptEdits"},"skipDangerousModePermissionPrompt":true}}],
	     "launch":[{"bin":"claude","flags":["--dangerously-skip-permissions"]}]},
	   "guarded":{"config":[{"agent":"claude","name":"settings","codec":"json","path":"~/.claude/settings.json",
	        "managed":{"permissions":{"defaultMode":"default"}}}]}}]}`)}

	// autonomy ON: the base benign key survives, and the autonomous bypass keys are folded in.
	on, probs := p.SurfacesFor(true)
	if len(probs) != 0 {
		t.Fatalf("SurfacesFor(true) problems: %v", probs)
	}
	m := on[0].ManagedMap()
	if prefs, _ := m["preferences"].(map[string]any); prefs == nil || prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("benign base managed key must survive the fold: %+v", m)
	}
	if m["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("autonomous posture key must be folded in: %+v", m)
	}
	if perms, _ := m["permissions"].(map[string]any); perms == nil || perms["defaultMode"] != "acceptEdits" {
		t.Errorf("autonomous defaultMode should be acceptEdits: %+v", m)
	}

	// autonomy OFF (host): the benign key still survives, but the guarded posture wins —
	// defaultMode is "default" and the bypass-only key is NOT present.
	off, _ := p.SurfacesFor(false)
	mo := off[0].ManagedMap()
	if prefs, _ := mo["preferences"].(map[string]any); prefs == nil || prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("benign base managed key must survive the guarded fold too: %+v", mo)
	}
	if perms, _ := mo["permissions"].(map[string]any); perms == nil || perms["defaultMode"] != "default" {
		t.Errorf("guarded defaultMode should be 'default' (prompts on): %+v", mo)
	}
	if _, present := mo["skipDangerousModePermissionPrompt"]; present {
		t.Errorf("guarded posture must NOT carry the bypass key: %+v", mo)
	}

	// Launch flags follow the same policy.
	if got := LaunchFlagsFor([]*Pack{p}, true)["claude"]; len(got) != 1 || got[0] != "--dangerously-skip-permissions" {
		t.Errorf("autonomy ON launch flags = %v, want the bypass flag", got)
	}
	if got := LaunchFlagsFor([]*Pack{p}, false)["claude"]; len(got) != 0 {
		t.Errorf("autonomy OFF launch flags = %v, want none (guarded)", got)
	}
}
