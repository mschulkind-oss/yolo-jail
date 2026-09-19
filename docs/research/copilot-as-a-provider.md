---
title: "A Copilot subscription cannot legitimately back Claude Code"
date: 2026-09-17
status: accepted
tags: [providers, profiles, copilot, github, terms, claude]
summary: "The 2026 move to AI Credits made Copilot's BILLING look like API billing, but it opened no inference endpoint; GitHub Models — the one documented one — was retired six weeks later. No published GitHub term forbids a third-party client in so many words, and two do forbid what reaching Copilot actually requires."
vantage:
  status-chip: true
---

# A Copilot subscription cannot legitimately back Claude Code

**Status:** CURRENT — every claim below verified from the linked primary source on
**2026-09-17**. Nothing here is a legal opinion; it is a reading of published terms with
the text quoted so a lawyer can check it. The call belongs to counsel.

**The ask** *(the maintainer's words, 2026-09-17)*: "what's the prospect of hooking up a
copilot subscription to claude through our profiles? … when it was prompt based pricing, it
was restricted, but now it's fully API pricing, so is it more open now? is there still an LM
API?"

**Reads with:** [`providers.md`](../reference/providers.md) (what a provider must declare),
[`gateway-providers.md`](gateway-providers.md) (the OpenRouter/Kilo precedent this would
follow), and
[`cerebras-pack-and-copilot-delivery.md`](../reference/cerebras-pack-and-copilot-delivery.md)
(the *opposite* direction — Copilot as a provider CONSUMER — which already shipped).

---

## 1. The short answer

The pricing premise is right and the conclusion does not follow. Copilot's billing did
become token-metered at list API rates on 2026-06-01, but **billing and access are
independent axes, and only the billing one moved.** On the access axis 2026 was a year of
*narrowing*: GitHub retired the one documented inference API it had on 2026-07-30.

| | Under premium requests | Today (AI Credits) |
| :--- | :--- | :--- |
| Unit of charge | one "premium request", token-blind | input/output/cached tokens at each model's listed API rate |
| Documented inference endpoint for your own client | GitHub Models (`models.github.ai`) | **none** — retired 2026-07-30 |
| Programmatic surface | Copilot Extensions, VS Code `vscode.lm` | Copilot SDK (agent runtime), `vscode.lm` |
| Anthropic-compatible route | none | none |

## 2. What actually changed in the billing model

GitHub moved every Copilot plan to usage-based billing on **2026-06-01**. Premium Request
Units were replaced by **GitHub AI Credits**, where `1 AI credit = $0.01 USD`, and usage is
"calculated based on token consumption, including input, output, and cached tokens, using
the listed API rates for each model"
([models-and-pricing](https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing)).
Code completions and next edit suggestions stay unmetered.

So the maintainer's read is correct: it is now, functionally, API pricing.

**And that is exactly what removes the reason to want this.** Under premium requests, one
long agentic Claude Code turn cost the same as one chat question — a large, real arbitrage.
Metered at list rates there is nothing left to arbitrage on the token price itself:

| Model | Copilot's listed rate, $/MTok in→out | Anthropic first-party list |
| :--- | :--- | :--- |
| Claude Sonnet 5 | $2.00 → $10.00 | $2.00 → $10.00 |

The only remaining edge is the plan's **included** credit allowance, and it is small and
disappears at the tier a company would actually buy:

| Plan | Price/user/mo | Included credits | Effective multiple |
| :--- | :--- | :--- | :--- |
| Pro | $10 | $15 | 1.5× |
| Pro+ | $39 | $70 | 1.8× |
| Max | $100 | $200 | 2.0× |
| **Business** | **$19** | **1,900 ($19)** | **1.0× — break-even** |
| Enterprise | (sales) | 3,900 ($39) | — |

Individual figures from [the plans page](https://github.com/features/copilot/plans);
Business/Enterprise from
[usage-based billing for organizations](https://docs.github.com/en/copilot/concepts/billing/usage-based-billing-for-organizations-and-enterprises),
which also notes credits are "pooled at the billing entity level", do not carry over, and
can be capped by admin budget policy.

> [!NOTE]
> Hyperscience seats are Copilot **Business**, the break-even row. Even if every access
> question below resolved favourably, the saving over buying Anthropic tokens directly is
> zero, before the unmetered completions are priced in.

## 3. The four access surfaces, and what each one is

### 3.1 GitHub Models — retired, and it was the only documented one

> "The playground, model catalog, inference API, and bring your own key (BYOK) are no
> longer available to any customer, including existing customers with active usage."
> — [GitHub Models is now retired](https://github.blog/changelog/2026-07-30-github-models-is-now-retired/), 2026-07-30

This is the finding that settles the question. GitHub Models was a documented,
PAT-authenticated inference API; it is the surface a `packs/github-models` provider would
have declared. It is gone, for everyone, with GitHub pointing at Microsoft Foundry or
Copilot instead. **Verdict: unavailable.**

### 3.2 The Copilot SDK — real, sanctioned, and the wrong shape

GA'd **2026-06-02**
([changelog](https://github.blog/changelog/2026-06-02-copilot-sdk-is-now-generally-available/)).
It gives "direct, programmatic access to the same agent runtime behind GitHub Copilot —
planning, tool invocation, file edits, streaming, and multi-turn sessions", authenticates
with GitHub OAuth / GitHub Apps / environment tokens, and is "available to all existing
GitHub Copilot subscribers, including Copilot Free".

It exposes an **agent runtime**, not an inference endpoint. A yolo provider declares
`endpoints.<dialect>.base_url` + a wire protocol; there is no chat-completions or Messages
route here to declare. Handing Claude Code an agent that already plans and edits files is
not a model backend — it is a second agent.

**Verdict: legitimate, but it is `packs/copilot` (the agent), not a provider.** yolo
already ships that pack.

### 3.3 The VS Code LM API — yes, it still exists; no, it does not reach us

`vscode.lm` is alive
([Language Model API](https://code.visualstudio.com/api/extension-guides/ai/language-model)).
The constraints are structural, not merely restrictive: "Copilot's language models require
consent from the user before an extension can use them", `selectChatModels` "should be
called as part of a user-initiated action", and extensions "should not use the Language
Model API for integration tests due to rate-limitations."

It is an **in-process VS Code extension API**. Reaching it from Claude Code in a container
means writing an extension that re-exports Copilot inference over a socket — which is the
proxy of [§3.4](#34-reverse-engineered-proxies--this-is-what-hooking-it-up-would-mean) wearing a badge, and lands on the Extension Developer Policy quoted below.
OpenCode's [issue #15243](https://github.com/anomalyco/opencode/issues/15243) is the same
idea from the same motive (dodging Copilot rate limits) and is still open.

**Verdict: exists, unreachable from a jail without building the thing [§3.4](#34-reverse-engineered-proxies--this-is-what-hooking-it-up-would-mean) describes.**

### 3.4 Reverse-engineered proxies — this is what "hooking it up" would mean

[`ericc-ch/copilot-api`](https://github.com/ericc-ch/copilot-api) is the state of the art
and is explicit about the target: *"Turn GitHub Copilot into OpenAI/Anthropic API compatible
server. Usable with Claude Code!"* It serves `/v1/chat/completions`, `/v1/models`,
`/v1/embeddings`, and — the part that matters — **`/v1/messages` and
`/v1/messages/count_tokens`**, against `api.githubcopilot.com` with a token minted from a
Copilot session.

Technically this works and would slot into yolo cleanly: a `kind: "service"` pack in the
shape of [`wire-bridge`](../reference/wire-bridge.md), publishing a jail-local Anthropic
endpoint that `packs/claude`'s derive already knows how to consume. **The engineering is
the easy half.** The README's own warning is the hard half, quoted in [§4.3](#43-what-the-terms-do-say-and-it-is-enough).

`api.githubcopilot.com` is not in GitHub's public API. The documented Copilot REST surface
is seat management and usage metrics under `/orgs/{org}/copilot` — no inference.

## 4. The terms, quoted

### 4.1 Which document governs depends on how the seat was bought

[GitHub Terms for Additional Products and Features](https://docs.github.com/en/site-policy/github-terms/github-terms-for-additional-products-and-features)
routes three ways: Business/Enterprise bought direct from GitHub → the
[GitHub Generative AI Services Terms](https://github.com/customer-terms/github-generative-ai-services-terms);
bought through Microsoft → Microsoft Product Terms; everyone else → Section J of the
[GitHub ToS](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service).
**Establish which applies to Hyperscience before relying on any of this.**

### 4.2 What the terms do NOT say

This is worth stating plainly, because the popular claim overstates it. The widely repeated
line that "Copilot may only be used through officially supported clients" is **not a quote
from any GitHub document I could find.** It propagates from third-party proxy READMEs and
from LLM summaries of them.

Specifically:

- The **Copilot Product Specific Terms** (deprecated 2026-03-05, superseded by the
  Generative AI Services Terms — extracted from
  [the March 2026 PDF](https://assets.ctfassets.net/8aevphvgewt8/1Y0gmEkMnAs8W6N4ai2R1g/694c0ae359902dc0700454333ad15c44/GitHub_Copilot_Product_Specific_Terms_-_2026_03_05_-_FINAL.pdf))
  contain **no** permitted-client clause, **no** reverse-engineering clause, and **no**
  competing-product clause. Section 1 merely observes that Copilot "includes tools for your code
  editor, as well as optional tools that can be used through a command-line interface, web
  browser, or mobile device." Section 5 delegates the whole restriction question outward: "Your use
  of GitHub Copilot is subject to the Acceptable Use Policies, the AI Code of Conduct, and
  the Required Mitigations."
- The **Microsoft AI Code of Conduct** ([aka.ms/AIcode](https://learn.microsoft.com/legal/ai-code-of-conduct),
  v4.0, 2026-05-01) is entirely about *what you generate* — harm, content categories,
  autonomy, oversight. Its "Usage restrictions" list has **no** clause on access channel,
  reverse engineering, or quota circumvention.
- The **Generative AI Services Terms** likewise turn out to be about data and
  responsibility, not access channel.

So: nobody should claim GitHub has written down "one client only". They have not.

### 4.3 What the terms DO say, and it is enough

Two clauses bear directly, plus one policy that reads on the mechanism.

**GitHub Acceptable Use Policies, Section 6, "Services Usage Limits"**
([source](https://docs.github.com/en/site-policy/acceptable-use-policies/github-acceptable-use-policies)):

> "You will not reproduce, duplicate, copy, sell, resell or exploit any portion of the
> Service, use of the Service, or access to the Service without our express written
> permission."

Re-exposing Copilot inference through a local server so a different vendor's agent can spend
the seat is squarely "exploit … access to the Service." This is the load-bearing clause.

**GitHub AUP Section 4** prohibits "using our servers for any form of excessive automated bulk
activity" — and an always-on coding agent is, by volume, exactly the profile abuse detection
is tuned for.

**GitHub Copilot Extension Developer Policy, Section 2 (Security)**
([source](https://docs.github.com/en/site-policy/github-terms/github-copilot-extension-developer-policy),
last updated 2025-10-20) prohibits, among other things:

> - Bypassing or circumventing protocols and access controls
> - Using unpublished APIs
> - Reverse engineering the Platform or deriving source code or trade secrets

and grants only "a limited … license … to access and use the Platform for the purpose of
publishing, developing, demonstrating, testing and supporting interoperability and
integrations", adding: "Other than the rights we expressly give you in this Agreement or the
TOS, We don't grant you any rights or licenses to GitHub Copilot."

⚠ **This policy binds Extension developers, not every user** — a private in-jail proxy is
arguably outside its scope. Cite it as GitHub's stated posture on unpublished APIs, not as a
clause that automatically governs us.

**And the project's own warning**, which is the most honest source in the set
([`ericc-ch/copilot-api`](https://github.com/ericc-ch/copilot-api)):

> "Excessive automated or scripted use of Copilot (including rapid or bulk requests, such as
> via automated tools) may trigger GitHub's abuse-detection systems. You may receive a
> warning from GitHub Security, and further anomalous activity could result in temporary
> suspension of your Copilot access."

### 4.4 Enforcement is real and the blast radius is the GitHub account

The community forum carries a steady stream of Copilot suspensions — e.g.
[#174325](https://github.com/orgs/community/discussions/174325),
[#196203](https://github.com/orgs/community/discussions/196203),
[#192097](https://github.com/orgs/community/discussions/192097) — and reports that the
restriction is treated as permanent for Copilot on that account, with the underlying GitHub
account sometimes suspended alongside it.

That last part is the real risk and it is not a licensing risk. **A Hyperscience engineer's
GitHub identity is their push access.** Trading a break-even token discount for a
non-zero chance of losing it is not a trade worth constructing, independent of how the legal
question resolves.

## 5. Verdicts

| Option | Verdict |
| :--- | :--- |
| `packs/github-models` provider (documented API) | **Dead.** Surface retired 2026-07-30. |
| Copilot SDK as a Claude Code backend | **Rejected: wrong shape.** Agent runtime, no inference endpoint to declare. |
| VS Code `vscode.lm` bridge out of the jail | **Rejected.** In-process extension API; bridging it reduces to the proxy below and reads on the Extension Developer Policy. |
| `kind: "service"` pack wrapping `copilot-api` | **Buildable in ~a day, and not recommended.** Trips AUP Section 6; risks the engineer's GitHub account; saves nothing at Business rates. |
| Keep Copilot as an agent (`packs/copilot`) | **Already shipped.** The sanctioned direction. |
| Claude Code on a first-party Anthropic credential | **Status quo, and it is the cheap option.** Same list rates, no third-party term in the path. |

**The one-line answer to "what can we do?":** run Copilot as Copilot and Claude as Claude —
which is what yolo already does — and note that `packs/copilot`'s BYOK derive already lets
the Copilot CLI ride *our* providers, which is the same integration in the direction GitHub
documents and supports.

## 6. Open questions

### ✅ [OQ-GC1](#-oq-gc1--is-the-copilot-as-provider-thread-closed-or-parked-pending-counsel--resolved-2026-09-17) — is the Copilot-as-provider thread closed, or parked pending counsel? — RESOLVED (2026-09-17)

**Stakes.** [§4.2](#42-what-the-terms-do-not-say) found that GitHub has *not* written the prohibition everyone quotes. The
case against rests on AUP Section 6 plus enforcement behaviour, which is strong enough to act on
but is a reading, not a citation of an on-point clause. If the maintainer wants the thread
genuinely closed, this doc is the record; if the economics ever change (a Copilot plan whose
included credits materially beat list rates), the question reopens and only the legal half
would need re-checking.

_Leaning:_ close it. The economics at Business rates are break-even, so even a favourable
legal reading buys nothing — which makes the legal question moot rather than pending.

> **Answer:** Closed, 2026-09-17 — *"forget that, seems like a dead end."* No counsel review
> was sought and none is needed: the economics ruling ([§2](#2-what-actually-changed-in-the-billing-model), break-even at Business rates)
> disposes of the thread on its own, so the legal reading in [§4](#4-the-terms-quoted) never becomes load-bearing.
> **What would reopen it:** a Copilot plan whose included credits materially beat Anthropic
> list, or a documented GitHub inference endpoint returning. Either one reopens the legal
> half too — re-verify [§4](#4-the-terms-quoted) before acting, since the Generative AI Services Terms are new.

## 7. Fast-moving — verify before building

Everything in this section was true on 2026-09-17 and is the kind of fact that moves:

- Copilot's per-model listed rates and per-plan credit allowances ([§2](#2-what-actually-changed-in-the-billing-model)).
- Whether GitHub reintroduces a documented inference API — Copilot is now the only
  model-access story GitHub tells, so pressure exists.
- The Copilot SDK's surface: if it ever grows a raw-inference route, [§3.2](#32-the-copilot-sdk--real-sanctioned-and-the-wrong-shape)'s verdict flips
  from "wrong shape" to a live option with no ToS problem.
- `vscode.lm`'s model list — the VS Code doc page still names `gpt-4o` and
  `claude-3.5-sonnet` and is visibly stale.
- The Generative AI Services Terms (effective 2026-03-05) are new; re-read before relying on
  [§4.2](#42-what-the-terms-do-not-say)'s negative finding.

## Sources

Primary, in the order they decide the question:

- [GitHub Models is now retired](https://github.blog/changelog/2026-07-30-github-models-is-now-retired/) — kills the only documented inference API; the single most decisive source here.
- [GitHub Acceptable Use Policies](https://docs.github.com/en/site-policy/acceptable-use-policies/github-acceptable-use-policies) — Section 6 "Services Usage Limits" is the load-bearing clause; Section 4 covers bulk automation.
- [Copilot Product Specific Terms, March 2026 (PDF)](https://assets.ctfassets.net/8aevphvgewt8/1Y0gmEkMnAs8W6N4ai2R1g/694c0ae359902dc0700454333ad15c44/GitHub_Copilot_Product_Specific_Terms_-_2026_03_05_-_FINAL.pdf) — deprecated, and notable for what it does *not* restrict; Section 5 delegates to the AUP.
- [GitHub Terms for Additional Products and Features](https://docs.github.com/en/site-policy/github-terms/github-terms-for-additional-products-and-features) — the three-way routing that decides which document governs a given seat.
- [GitHub Copilot Extension Developer Policy](https://docs.github.com/en/site-policy/github-terms/github-copilot-extension-developer-policy) — "unpublished APIs" and "bypassing access controls"; binds Extension developers, cite with care.
- [Microsoft AI Code of Conduct](https://learn.microsoft.com/legal/ai-code-of-conduct) — v4.0, 2026-05-01; content policy only, no access-channel clause.
- [Models and pricing for GitHub Copilot](https://docs.github.com/en/copilot/reference/copilot-billing/models-and-pricing) and [usage-based billing for orgs](https://docs.github.com/en/copilot/concepts/billing/usage-based-billing-for-organizations-and-enterprises) — the AI Credits mechanics and the Business/Enterprise allowances.
- [Copilot SDK GA](https://github.blog/changelog/2026-06-02-copilot-sdk-is-now-generally-available/) — agent runtime, auth options, plan eligibility.
- [VS Code Language Model API](https://code.visualstudio.com/api/extension-guides/ai/language-model) — consent, rate limits, and the extensibility policy it defers to.

Prior art and observed behaviour:

- [`ericc-ch/copilot-api`](https://github.com/ericc-ch/copilot-api) — the working `/v1/messages` proxy, and the clearest statement of the abuse-detection risk.
- [`caozhiyuan/copilot-api`](https://github.com/caozhiyuan/copilot-api) — a second implementation, same approach.
- [OpenCode #15243](https://github.com/anomalyco/opencode/issues/15243) — another agent wanting `vscode.lm` for the same rate-limit reason; still open.
- Community suspension reports [#174325](https://github.com/orgs/community/discussions/174325), [#196203](https://github.com/orgs/community/discussions/196203), [#192097](https://github.com/orgs/community/discussions/192097) — what enforcement looks like from the receiving end.
