---
name: qa-collaborative
description: Human-in-the-loop QA and coordination workflow. Processes user answers to open questions, implements changes, and memorializes feedback.
---

# QA Collaborative (Human-In-The-Loop)

This skill is designed for iterative, human-guided development. It focuses on processing user answers to "Open Questions" and moving the project forward in batches.

## 1. Processing User Answers
When a user indicates they have answered questions (or you notice `<PLACEHOLDER>` has been replaced):
1. **Read**: Scan all project docs and `OPEN_QUESTIONS.md` for user answers.
2. **Analyze**: Determine the impact on the Research, Plan, or Task List.
3. **Act**: 
   - **Update**: Adjust the relevant plans or code to reflect the user's decision.
   - **Memorialize**: Move the question/answer pair from "Open Questions" to the "Answered Questions (Memorialized)" section in the relevant document.
   - **Sync**: Remove the question from the project-root `OPEN_QUESTIONS.md`.
4. **Report**: Inform the user what has been updated based on their feedback.

## 2. Batching QA Workflows
- **Chunking**: Break large verification tasks into batches (`docs/qa/batch-N.md`).
- **Handoff**: Present the batch, wait for human review, and then fix/process the results.

## Implementation
Follow the standards in [agent-standards](agent-standards) for question templates and memorialization structure.
