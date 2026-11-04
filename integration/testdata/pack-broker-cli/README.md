# Local CLI broker fixture input

This directory holds an independently named third-party fixture pack input for the future
`call_broker` loader. Its `call_broker` block is deliberately future-facing: current core does not
recognize that block, and these pack files are not yet runtime-loadable. The cases in
[`fixture-cases.v1.json`](fixture-cases.v1.json) are versioned fixture examples, not an invented
core classifier wire protocol. Task E must define and validate that actual program interface.

The runnable local service is `tools/broker-fixture`; it binds only host loopback, generates
synthetic private credentials for each run, and accepts host operations through a separate admin
token. The `fixturectl` case names describe client semantics; there is no jail-side broker forwarder
until the later core tasks land.
