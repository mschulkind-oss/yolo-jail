---
name: project-setup
description: Standards for project structure, tooling, and configuration (justfile, uv, ruff, etc).
---

# Project Setup & Configuration Skill

This skill outlines the standard procedure for setting up and maintaining the `kitchen` project environment. It mandates specific tools for dependency management, task running, and code quality.

## 1. Directory Structure

- **`docs/`**: Documentation root.
    - **`docs/plans/`**: Active planning documents and specifications.
    - **`docs/plans/decisions/`**: Architecture Decision Records (ADRs).
- **`src/`**: Source code.
    - **`src/api/`**: Python FastAPI backend.
    - **`src/mobile/`**: React Native (Expo) frontend.
- **`tests/`**: Test suites.
    - **`tests/api/`**: Backend unit/integration tests (pytest).
    - **`tests/web/`**: Frontend E2E tests (Playwright).
- **`infra/docker/`**: Docker Compose configuration and volume templates.
- **`phase0_flow/`**: Legacy recipe data and PDF generation (Phase 0).
- **`scripts/`**: Utility scripts (e.g., mermaid linting).

## 2. Tooling Stack

- **Task Runner**: [`just`](https://github.com/casey/just) - The primary entry point for all developer tasks.
- **Python Management**: [`uv`](https://github.com/astral-sh/uv) - Fast package installer and resolver.
- **Node/JS Management**: `npm` - Used for frontend dependencies and documentation tools.
- **Process Manager**: [`hivemind`](https://github.com/DarthSim/hivemind) - For running full-stack development environment (via `Procfile.dev`).
- **Environment Management**: [`.mise.toml`](https://mise.jdx.dev) - Manages tool versions (python, node, uv).

## 3. Configuration Files

### `pyproject.toml` (Python)
Configures `ruff` (linting/formatting), `pytest` (testing), and `mypy` (type checking).

```toml
[tool.pytest.ini_options]
testpaths = ["tests"]
pythonpath = ["."]
asyncio_mode = "auto"
# Strict warnings policy
filterwarnings = ["error", "ignore::DeprecationWarning:hypothesis.*"]

[tool.ruff]
line-length = 100
target-version = "py313"

[tool.ruff.lint]
select = ["E", "W", "F", "I", "B", "C4", "UP", "ARG", "SIM"]
ignore = ["E501", "B008", "ARG002"] # ARG002 allows unused args in interfaces

[tool.mypy]
python_version = "3.13"
plugins = ["pydantic.mypy"]
disallow_untyped_defs = true # Strict typing
```

### `package.json` (Root)
Handles markdown linting and diagram validation.

```json
{
  "scripts": {
    "lint": "markdownlint '**/*.md' --ignore '**/node_modules/**'"
  }
}
```

### `justfile` (Key Commands)
Use `just <command>` for all common tasks.

*   **Setup**: `just setup` (installs all python/js dependencies).
*   **Dev (Full Stack)**: `just dev-all` (starts API, DB, and Frontend via Hivemind).
*   **Dev (Frontend)**: `just web` (starts Expo web).
*   **Dev (Backend)**: `just dev-api` (starts FastAPI with reload).
*   **Infrastructure**: `just up` (starts Docker stack), `just down` (stops it).
*   **Quality**: `just check` (runs lint, test, and typecheck).
*   **Testing**: `just test` (all), `just test-py` (backend), `just test-e2e` (frontend).

## 4. Workflow Standards

### Development Loop
1.  **Start Infra**: `just up-infra` (or `just dev-all` for full stack).
2.  **Code**: Make changes in `src/`.
3.  **Verify**: Run `just check` before committing. This enforces:
    *   **Linting**: `ruff` (Python), `markdownlint` (Docs), `lint_mermaid.py` (Diagrams).
    *   **Formatting**: `ruff format`.
    *   **Type Checking**: `mypy` (Strict mode).
    *   **Tests**: `pytest` (Backend).

### Database Management
*   **Supabase Studio**: Access local admin UI via `just studio` (http://localhost:5303).
*   **Reset**: `just reset-stack` wipes the database and restarts containers (Use with caution).

### Documentation
*   **Linting**: All markdown files are linted. Mermaid diagrams within markdown are validated.
*   **Structure**: Keep the root clean. New documentation goes into `docs/`.
