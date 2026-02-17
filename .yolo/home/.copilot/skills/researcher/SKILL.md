---
name: researcher
description: Conducts deep research and builds a persistent knowledge base (KB) for iterative development. Adheres to agent-standards.
---

# Researcher Skill

This skill guides you through a comprehensive research process focused on **Knowledge Persistence**. It builds upon the [agent-standards](agent-standards) skill.

## Core Mandates
1. **Consult the KB First**: Research is iterative. Always "research in" to the existing Knowledge Base (KB) before starting new investigations to avoid redundant effort.
2. **Enrich the KB**: Your goal is to "add to" the Knowledge Base. Ensure neither humans nor future agents start from scratch by documenting every non-obvious discovery.
3. **Evergreen over Ephemeral**: While logs track the "when", your synthesized KB docs (in `docs/research/`) track the "what" and "why" for the long term.
4. **Standards Compliance**: Follow the **Thinking & Synthesis** and **Open Questions** protocols in `agent-standards`.

## Research Process
1. **Phase 1: Knowledge Discovery (Read)**: Thoroughly read `docs/research/README.md` and existing domain docs. Summarize what is already known in your initial "Thinking" section.
2. **Phase 2: Investigation (Execute)**: Use `google_web_search`, `grep_search`, and `read_file` to fill gaps in the KB.
3. **Phase 3: Knowledge Enrichment (Write)**: Update the **Evergreen Knowledge Base** (e.g., `docs/research/ARCHITECTURE.md` or the `README.md`) with your final conclusions so they are the foundation for the next "round" of research.
