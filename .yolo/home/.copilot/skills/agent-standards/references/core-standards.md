# Core Agent Standards

## 1. Open Questions Protocol
Every research log, plan, or task list must maintain an "Open Questions" section.
- **Project Root Tracker**: Sync all active questions to a project-root `OPEN_QUESTIONS.md` file.
- **Template**: Use the following structure to make it clear where the user should respond:
  ```markdown
  ### [Question Title]
  - **Question**: [Your clear question here]
  - **Context**: [Why this matters]
  - **User Answer**: <PLACEHOLDER> (Please replace this text with your answer)
  ```
- **Memorialization**: Once answered, the agent must read the response, move the entry to "Answered Questions (Memorialized)", and hide the raw conversation by incorporating the answer into the relevant KB docs or implementation plans.

## 2. Iterative Knowledge Base (KB)
Research is a cumulative process. Agents must build a KB that prevents starting from scratch in future rounds.
- **Evergreen Documentation**: Beyond timestamped logs, maintain/update "Domain Docs" (e.g., `docs/research/README.md` or `docs/research/DOMAIN_NAME.md`) that provide high-level, up-to-date summaries of findings.
- **Synthesis**: Every research phase should conclude by updating the relevant Knowledge Base entry.

## 3. Thinking & Synthesis
For complex reasoning, research, or architecture decisions, agents must include a "Thinking & Synthesis" section.
- **Summarized Transcripts**: Provide a transcript of your internal monologue.

## 4. Progressive Documentation
Document work **as it happens**, not at the end.
- **Shell Execution**: Use `uvx showboat exec` for complex or critical shell commands to capture verifiable output.

## 5. Documentation Structure
Standardize projects with the following directory structure:
```
docs/
  research/      # Investigation logs AND Evergreen KB docs
  plans/         # RFCs and design docs
  tasks/         # Progress trackers and checklists
  implementation/# Execution logs (Showboat files)
  qa/            # Trackers and reports
```