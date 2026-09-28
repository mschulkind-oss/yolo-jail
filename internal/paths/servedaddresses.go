package paths

// ServedAddressesEnv carries a launch's SERVED ADDRESSES (coined in internal/packload's served.go,
// docs/plans/notch-convergence.md NC-D41): a JSON object mapping each declared loopback
// `host:port` a jail daemon or pack service serves at to the one it serves at in THIS launch,
// for the declared addresses the launch moved. A jail that shares the launcher's network
// namespace gets an ephemeral port for each, because two jails on one loopback would otherwise
// contend for the declared one; a jail with a namespace of its own moves nothing and carries no
// line.
//
// It travels in the per-entry channel section of yolo-user-env.sh, beside the caller tokens and
// for their reason: the daemons in a running jail bound these ports once, at boot, and an
// ATTACH has to compose its clients for the same ports, so it reads them back from the channel
// the launch wrote (internal/cli/run's runningServedAddresses). It is not a secret. A jail can
// rewrite the file, which lets it point its own next entry's clients elsewhere on a loopback it
// already reaches; the reader accepts only loopback addresses.
const ServedAddressesEnv = "YOLO_SERVED_ADDRESSES"
