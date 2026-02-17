---
name: implement-project
description: Orchestrate a complete software implementation project. Use when you need to "implement a new round" or "implement a new feature where". Adheres to agent-standards.
---

# Implement Project Skill

Orchestrate the SDLC. Builds upon [agent-standards](agent-standards).

## Workflow

### Phase 0: Coordination & Answer Processing
1. **Open Questions**: Initialize and maintain the project-root `OPEN_QUESTIONS.md` as per `agent-standards`.
2. **Answer Check**: Periodically check for user answers. When found, use the `qa-collaborative` logic to:
   - Implement the decision in code or docs.
   - Move the question to the "Answered Questions (Memorialized)" section.
   - Clear it from the root tracker.

### Phase 1: Research
- Use the `researcher` skill. Output to `docs/research/` and update the KB.

### Phase 2: Plan (RFC)
- Synthesize research into an RFC in `docs/plans/`.
- Use the **Open Questions** template for any design ambiguities.

### Phase 3: Task List
- Create a checklist in `docs/tasks/`.

### Phase 4: Implementation
- Use `uvx showboat` for all shell executions.
- Log progress in `docs/implementation/`.

### Phase 5: QA
- Use `qa-autonomous` or `qa-collaborative` (for batch testing).
