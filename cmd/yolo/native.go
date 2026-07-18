package main

// nativeDispatch maps a subcommand to its native Go handler. It is
// intentionally EMPTY until each slice is byte-goldened against the Python
// output (frontdoor.nativeSubcommands must stay in lockstep). An empty map
// means every subcommand delegates to Python — behavior unchanged, which is the
// correct default until a slice's parity is proven. Slices (config-ref, init,
// init-user-config) register here as they land.
var nativeDispatch = map[string]func(args []string) int{}
