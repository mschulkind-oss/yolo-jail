---
title: "Host wrapper auto-apply and tiered consent — implementation sketch"
date: 2026-09-18
status: draft
tags: [sketch, host, apply, wrappers, implementation]
summary: "Implementation sketch for coupling host_apply_on_launch with host_wrappers and introducing tiered auto-apply in hostApplyGate."
---

# Host wrapper auto-apply and tiered consent — implementation sketch

**Status:** SKETCH, 2026-09-18 — incomplete, and unstable while questions are open.

> **Precedence:** [`host-wrapper-auto-apply.md`](host-wrapper-auto-apply.md) leads on all behavioral
> and architectural decisions.

## 1. File and component map

| File | Change |
| :--- | :--- |
| `internal/config/hostapplyonlaunch.go` | Update default resolution: if `host_apply_on_launch` is unset, default to `host_wrappers` value. |
| `internal/cli/hostapplysurvey.go` | Add classification to survey results: distinguish Tier 1/2 (pure additions, managed updates) from Tier 3 (destructive collisions/losses). |
| `internal/cli/hostapplygate.go` | Update `hostApplyGate`: auto-apply Tier 1/2 changes silently; prompt only on Tier 3 collisions. |
| `internal/cli/packupdate.go` | In host context, trigger `applyHostSurveyed` after pack updates when `host_management` != `"none"`. |
| `internal/cli/config_ref.txt` | Update documentation for `host_wrappers` and `host_apply_on_launch`. |

## 2. Blocked decisions

- Blocked on [OQ-1](host-wrapper-auto-apply.md#oq-1--should-host_wrappers-true-imply-host_apply_on_launch-by-default) — resolution default vs enum replacement.
- Blocked on [OQ-2](host-wrapper-auto-apply.md#oq-2--what-should-the-default-auto-apply-posture-be) — the exact tier boundary for auto-apply vs prompt.
- Blocked on [OQ-3](host-wrapper-auto-apply.md#oq-3--should-yolo-pack-update-also-trigger-host-apply) — whether `yolo pack update` runs `host apply` directly.

## 3. Migration and backwards compatibility

- Users with `"host_wrappers": true` and `"host_apply_on_launch": false` explicitly configured retain their explicit opt-out.
- Users with only `"host_wrappers": true` gain auto-apply on launch without having had to configure the second key.
