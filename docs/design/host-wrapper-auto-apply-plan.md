---
title: "Host wrapper auto-apply and zero-prompt launch — implementation sketch"
date: 2026-09-18
status: accepted
tags: [sketch, host, apply, wrappers, implementation]
summary: "Implementation sketch for coupling host_apply_on_launch with host_wrappers and enabling zero-prompt auto-apply in hostApplyGate."
vantage:
  status-chip: true
---

# Host wrapper auto-apply and zero-prompt launch — implementation sketch

**Status:** IMPLEMENTED, 2026-09-18 — settled following rulings on all three open questions ([`host-wrapper-auto-apply.md#decision-ledger`](host-wrapper-auto-apply.md#decision-ledger)). Fully built and tested.

> **Precedence:** [`host-wrapper-auto-apply.md`](host-wrapper-auto-apply.md) leads on all behavioral
> and architectural decisions.

## 1. File and component map

| File | Change |
| :--- | :--- |
| `internal/config/hostapplyonlaunch.go` | Update default resolution: if `host_apply_on_launch` is unset, default to `host_wrappers` value. |
| `internal/cli/hostapplygate.go` | Update `hostApplyGate`: auto-apply changes silently and exec agent immediately, bypassing prompt. |
| `internal/cli/packupdate.go` | In host context, trigger `applyHost` after pack updates when `host_management` != `"none"`. |
| `internal/cli/config_ref.txt` | Update documentation for `host_wrappers` and `host_apply_on_launch`. |

## 2. Settled decisions

- [`OQ-1`](host-wrapper-auto-apply.md#decision-ledger): `host_wrappers: true` implies `host_apply_on_launch: true` by default; `"host_apply_on_launch": false` is the escape hatch ([§3.1](host-wrapper-auto-apply.md#31-implied-auto-check-when-host_wrappers-true)).
- [`OQ-2`](host-wrapper-auto-apply.md#decision-ledger): Zero-prompt auto-apply on launch: yolo synchronizes declared pack updates silently without interactive confirmation ([§3.2](host-wrapper-auto-apply.md#32-zero-prompt-auto-apply-on-launch)).
- [`OQ-3`](host-wrapper-auto-apply.md#decision-ledger): `yolo pack update` on the host automatically triggers `host apply --assert` when `host_management` is `"assert"` or `"own"` ([§3.3](host-wrapper-auto-apply.md#33-coupling-with-yolo-pack-update)).

## 3. Migration and backwards compatibility

- Users with `"host_wrappers": true` and `"host_apply_on_launch": false` explicitly configured retain their explicit opt-out.
- Users with only `"host_wrappers": true` gain auto-apply on launch without having had to configure the second key.
