package hostskills

// SourcedBuiltinCollisions checks only sourced declarations against the names
// supplied by the built-in suite's authority. Ordinary override rules stay intact.
func SourcedBuiltinCollisions(dests []Destination, names []string) []Collision {
	var out []Collision
	for _, d := range dests {
		claims := destinationClaims(d)
		for _, name := range names {
			var sourced []Claim
			for _, claim := range claims[name] {
				if claim.Sourced {
					sourced = append(sourced, claim)
				}
			}
			if len(sourced) == 0 {
				continue
			}
			sourced = dedupePackClaims(sourced)
			sourced = append(sourced, Claim{Pack: "yolo built-in skills", Author: name, Generated: true})
			out = append(out, Collision{Dir: d.Dir, Name: name, Claims: sourced})
		}
	}
	return out
}
