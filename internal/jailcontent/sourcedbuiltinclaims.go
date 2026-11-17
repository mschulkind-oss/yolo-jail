package jailcontent

// BuiltinSkillClaimNames includes the delivered suite names and their bare
// aliases; it derives every spelling from the existing built-in authority.
func BuiltinSkillClaimNames(prefix string) []string {
	var names []string
	for _, bare := range BuiltinSkillBareNames() {
		names = append(names, bare)
		if delivered := BuiltinSkillName(prefix, bare); delivered != bare {
			names = append(names, delivered)
		}
	}
	return names
}
