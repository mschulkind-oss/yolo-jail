package mcpsignin

// HeaderRoute approves only the shared header mechanism, never OAuth authority.
// Sources retain their plain native bootstrap unless the frozen delivered point
// is capturable or the canonical record was usable when the launch froze it.
func HeaderRoute(launch *Launch, server, agent, form string, source, reachable bool) (bool, string) {
	if form != "bare" && form != "json" {
		return false, "header route unsupported"
	}
	if !reachable {
		return false, "broker unreachable"
	}
	if launch != nil && launch.Version != LaunchVersion {
		if source {
			return false, "native plain bootstrap"
		}
		return false, "supplied sign-in facts invalid"
	}
	if !source {
		if launch == nil {
			return true, "" // Absent legacy packet; preserve the existing route.
		}
		for _, decision := range launch.Decisions {
			if decision.Server == server && decision.Agent == agent {
				return true, ""
			}
		}
		return false, "not this launch's user-level once server"
	}
	if launch != nil && launch.Version == LaunchVersion {
		for _, decision := range launch.Decisions {
			if decision.Server == server && decision.Agent == agent && (decision.UsableRecord || (decision.Capturable && decision.Point != nil && decision.Point.NativeName != "")) {
				return true, ""
			}
		}
	}
	return false, "native plain bootstrap"
}
