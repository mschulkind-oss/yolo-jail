package check

// setupfixture_test.go is how a check test chooses the backend it predicts for, now that the
// removed `runtime` and `confinement` keys choose nothing (named-setups-plan.md slice 5): the
// setup a launch from here would select, through YOLO_SETUP, on the OS that setup runs on.

// setupForRuntime is the built-in setup a test's old runtime value names, and whether it runs on
// a Mac: "" is no setup (the OS default).
func setupForRuntime(rt string) (name string, mac bool) {
	switch rt {
	case "podman":
		return "podman-self", false
	case "container":
		return "apple-container-self", true
	case "macos-user":
		return "seatbelt-yolo-account", true
	}
	return "", false
}

// setupGetenv is a Getenv that answers YOLO_SETUP with name and nothing else.
func setupGetenv(name string) func(string) string {
	return func(k string) string {
		if k == "YOLO_SETUP" {
			return name
		}
		return ""
	}
}

// withSetup is a runCheckOverConfigWith tweak that answers YOLO_SETUP with name, every other key
// as the options already did.
func withSetup(name string) func(*Options) {
	return func(o *Options) {
		prev := o.Getenv
		o.Getenv = func(k string) string {
			if k == "YOLO_SETUP" {
				return name
			}
			return prev(k)
		}
	}
}

// optionsOnRuntime is an Options that selects the setup the old runtime value named, on its OS.
func optionsOnRuntime(rt string) *Options {
	name, mac := setupForRuntime(rt)
	return &Options{IsMacOS: mac, Getenv: setupGetenv(name)}
}
