---
name: qa-autonomous
description: Full-cycle QA workflow without human intervention. Agent performs discovery, execution, fixing, and synthesis autonomously.
---

# QA Autonomous (Human-Out-Of-The-Loop)

This skill orchestrates a complete QA cycle where the agent acts alone to approach a project specification.

## Workflow

1. **Discovery**: Analyze the codebase to map user flows. Create/Update `docs/qa/user-flow-tracker.md`.
2. **Autonomous Execution**: Using `qa-core` logic, iterate through the tracker from P0 to P3.
3. **Surgical Fixing**: When a bug is found, investigate, fix, verify, and commit immediately.
4. **Synthesis**: Update the tracker with final statuses and provide a project readiness report.

## Usage
Trigger this when you want the agent to independently verify a project or feature-set and provide a "done" state.
