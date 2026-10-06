package macosuser

// StateDir is the root-owned state dir every macos-user launch stages into: /var/yolo-jail, holding
// the staged yolo and guest binaries (bin/), each workspace's pack tree (packs/), content tree
// (home-overlay/), context tree (ctx/) and env file (env/), and each workspace's Seatbelt profile
// (profile-<cname>.sb). Exported for the one reader outside this package that needs the path
// itself rather than a path built from it, `yolo stores`, which lists what is in it.
func StateDir() string { return stateDir }
