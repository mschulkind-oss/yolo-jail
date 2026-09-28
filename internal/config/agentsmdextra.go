package config

// AgentsMDExtraUser is the USER-SCOPE `agents_md_extra`: the markdown the user's own config
// appends to every briefing, read the way `yolo host apply` reads every other key it composes
// into a real home — from user scope alone (UserScopeConfigOrEmpty). A workspace
// yolo-jail.jsonc is agent-editable, so what it says reaches a jail's briefing and never the
// human's own files. "" when the key is unset or not a string (validation refuses the latter).
func AgentsMDExtraUser() string {
	v, _ := UserScopeConfigOrEmpty().Get("agents_md_extra")
	s, _ := v.(string)
	return s
}
