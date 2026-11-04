# Local HTTP broker fixture inputs

These independently named third-party fixture pack inputs exercise the finite `/graphql` service.
The strict `call_broker` loader does not exist yet, so the included manifest blocks are not loaded by
core. `fixture-cases.v1.json` is a versioned fixture-case format only; it does not define or claim to
implement the future core classifier protocol. In particular, these files do not demonstrate broker
approval or end-to-end execution.

The optional signed-call case is a third fixture sharing the same synthetic service. Its classifier
input excludes the private per-run synthetic signing key. Core task E must establish the actual
classifier/executor program protocol before any fixture is claimed to execute through a broker.
Host-selected `lookup-failure`, `redirect`, `credential-echo` and `oversize` scenarios are synthetic
service behaviors, not jail-controlled endpoint or credential redirection options.
