---
title: "One OpenAI refresh owner for Codex, Pi, hosts, and jails — graduated"
date: 2026-09-14
status: accepted
stage: GRADUATED
next: "Nothing is owed by this file. The human checks a ChatGPT account needs are owned by openai-auth-broker-plan.md; delete this pointer once the roadmap's link to it moves"
tags: [authentication, codex, pi, opencode, oauth, design, graduated]
summary: "A pointer. The OpenAI subscription credential service graduated on 2026-10-01 into docs/reference/agent-credentials.md, whose OpenAI section now states the build: one host service as the only refresh writer, a view per agent (Codex, Pi, opencode), the login the host service runs, failure and recovery, the route on each backend, and OQ-OA1 to OQ-OA7 and OA-D1 in its why-appendix. The design's argument is in git history."
---

# One OpenAI refresh owner for Codex, Pi, hosts, and jails — graduated

**Status:** 2026-10-01 — every part of this design is built, and its settled content is now
[`agent-credentials.md`'s OpenAI section](../reference/agent-credentials.md#the-openai-subscription-credential-service),
which is the authority. Two places where the build parted from this design are stated there as
built: the host service runs the whole browser login, so no callback relay exists, and the Claude
and OpenAI brokers keep two lock transactions rather than one shared engine. MEASURED: a brokered
refresh on rootless podman on both architectures, and the `macos-user` doorway on a hosted Mac
(2026-09-30). UNMEASURED: the completion criteria that need a ChatGPT account, a browser login end
to end and agents crossing a real expiry, which
[`openai-auth-broker-plan.md`](openai-auth-broker-plan.md) owns as checks for a person. The design's
argument and its open-question text are in git history
(`git log --follow -- docs/design/openai-auth-broker.md`).

Each section and id of this design now resolves here:

| Was | Now |
| :--- | :--- |
| <a id="1-user-experience"></a>User experience | [Login, and what a user sees](../reference/agent-credentials.md#openai-login), and the managed host launch under [Import and logout](../reference/agent-credentials.md#import-and-logout--the-hosts-two-verbs) |
| <a id="2-one-writer-and-two-views"></a>One writer and two views | [One writer, and a view per agent](../reference/agent-credentials.md#openai-one-writer) |
| <a id="3-browser-callback-relay"></a>The browser callback relay (not built: the host service runs the login) | [Login, and what a user sees](../reference/agent-credentials.md#openai-login), and [`OQ-OA4`](../reference/agent-credentials.md#oq-oa4) |
| <a id="4-backend-transport"></a>Backend transport | [How each backend reaches it](../reference/agent-credentials.md#openai-backends) |
| <a id="5-failure-and-recovery"></a>Failure and recovery, and <a id="6-security-and-observability"></a>security and observability | [Failure and recovery](../reference/agent-credentials.md#openai-failure-and-recovery) |
| <a id="7-completion-criteria"></a>Completion criteria | [What has been watched running](../reference/agent-credentials.md#openai-measured) |
| <a id="8-decision-ledger"></a>The decision ledger | [`OQ-OA1`](../reference/agent-credentials.md#oq-oa1), [`OQ-OA2`](../reference/agent-credentials.md#oq-oa2), [`OQ-OA3`](../reference/agent-credentials.md#oq-oa3), [`OQ-OA4`](../reference/agent-credentials.md#oq-oa4), [`OQ-OA5`](../reference/agent-credentials.md#oq-oa5) |
| <a id="OQ-OA6"></a>The `macos-user` doorway question | [`OQ-OA6`](../reference/agent-credentials.md#oq-oa6) |
| <a id="OQ-OA7"></a>The question of Pi's refresh after an unauthorized response | [`OQ-OA7`](../reference/agent-credentials.md#oq-oa7) |
| <a id="OA-D1"></a>opencode on Pi's view | [`OA-D1`](../reference/agent-credentials.md#oa-d1) |
