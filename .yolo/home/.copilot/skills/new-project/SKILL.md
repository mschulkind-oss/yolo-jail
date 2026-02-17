---
name: new-project
description: Guide for scaffolding new software projects with established conventions (mise, uv, FastAPI, DDD/Standard profiles).
---
# New Project Scaffolding Skill

This skill guides the creation of new software projects following the user's established engineering conventions (as of early 2026). These conventions prioritize **`mise` + `uv`** as the default standard, with **`pixi`** for complex/GPU environments.

## Decision Matrix: Tool Selection

| Feature Needed | Tool/Stack Choice |
| :--- | :--- |
| **Standard Defaults** | **`mise` (env) + `uv` (deps)** |
| **Node.js / Web** | `mise` (env) + `pnpm` |
| **Complex / Conda / GPU** | `pixi` (Alternative to mise/uv) |
| **Rust / High-Perf** | **`cargo`** (managed via `mise`) |
| **Web Framework** | `FastAPI` (Backend), `Next.js` or `Expo` (Frontend) |
| **Process Mgr** | `hivemind` (via `Procfile.dev`) |
| **Architecture** | **Standard** (Flat), **DDD** (Nested), or **Systems** (Rust) |

## Workflow

When the user asks to "start a new project" or "scaffold a project":

1.  **Gather Information:**
    *   **Name**: Project name (kebab-case).
    *   **Type/Profile**:
        *   **Standard (Default):** Flat structure (`src/routers`, `src/services`). Best for CLIs, simple APIs.
        *   **Domain-Driven (Complex):** Nested structure (`src/api/app/domain/...`). Best for large systems (e.g., `kitchen`).
        *   **Systems (Rust):** Modular crate structure (`src/bin`, `src/domain`, `src/engine`). Best for high-perf tools (e.g., `genius`).
    *   **Components**: Need a frontend? DB?

2.  **Create Directory Structure:**

    *   **Common Folders:**
        ```text
        project-name/
        ├── trash/            # Safety: "Delete" files here instead of `rm`
        ├── docs/
        │   ├── design/       # Architecture decisions
        │   ├── plans/        # Future roadmap
        │   └── guides/       # How-to docs
        ├── context/          # Gitignored data/logs
        ├── scratch/          # Git-tracked notes
        └── .gemini/          # Config
        ```

    *   **Standard Profile (Python):**
        ```text
        src/
        ├── main.py
        ├── config.py
        ├── dependencies.py   # Singleton providers
        ├── routers/          # Thin API endpoints
        ├── services/         # Business logic
        └── models/           # Pydantic models
        ```

    *   **Domain-Driven Profile (DDD - Python):**
        ```text
        src/api/app/
        ├── core/             # Config, logging
        ├── db/               # Session management
        ├── routes/           # Thin routers (FastAPI)
        └── domain/
            └── [entity]/
                ├── models.py      # DTOs (Pydantic)
                ├── service.py     # Business logic
                └── repository.py  # DB Access (CRUD)
        ```

    *   **Systems Profile (Rust):**
        ```text
        src/
        ├── main.rs           # Binary entry point
        ├── lib.rs            # Library entry point
        ├── bin/              # Extra binaries/benchmarks
        ├── core/             # Constants, basic types
        ├── domain/           # Business logic (Pure)
        ├── engine/           # Complex logic/Algorithms
        └── ui/               # CLI/TUI adapters
        ```

3.  **Generate Configuration Files:**

    *   **`AGENTS.md`** (Mandatory System Prompt):
        *   **Template:**
            ```markdown
            # General Development Instructions

            Welcome to [Project Name].

            ## Project Structure
            - `src/`: Source code.
            - `tests/`: Test suite.
            - `docs/`: Documentation (Plans, Design, Guides).
            - `trash/`: Deleted files (Safety net).

            ## Core Tools
            - **mise**: Tool manager.
            - **uv** (Python) / **cargo** (Rust): Package manager.
            - **just**: Command runner.

            ## Best Practices
            - **Verification**: Always run `just check` after changes. This runs format, lint, type-check, and tests.
            - **Safety**: **NEVER** use `rm`. Move files to `trash/` instead.
            - **Regression Testing**: Always keep tests used to fix bugs. Integrate them into the permanent suite.
            - **Clean Code**: Follow PEP 8 (Python) or Rust idioms.

            ## TDD Workflow
            1. **Red**: Write a failing test for the new functionality.
            2. **Green**: Write the minimum code to pass.
            3. **Refactor**: Clean up while keeping tests green.

            **Bug Fixing:**
            1. **Reproduce**: Write a failing test case.
            2. **Fix**: Modify code until it passes.
            3. **Persist**: NEVER delete the reproduction test.
            ```

    *   **`Justfile`** (Universal Task Runner):
        *   **Standard Commands:**
            *   `setup` / `install`: Install all dependencies (`uv sync`, `cargo fetch`).
            *   `dev` / `run`: Run the app/dev server (`uvicorn`, `cargo run`).
            *   `check`: **The Universal Gate**. Must run `format`, `lint`, and `test`.
            *   `format`: Fix formatting (`ruff format`, `cargo fmt`).
            *   `lint`: Check quality (`ruff check`, `cargo clippy`).
            *   `test`: Run fast tests (`pytest`, `cargo test`).
            *   `test-all` (Optional): Run slow/integration tests.

        *   **Rust Template:**
            ```just
            default:
                @just --list

            run *args:
                cargo run -- {{args}}

            run-release *args:
                cargo run --release -- {{args}}

            check: format lint test

            format:
                cargo fmt

            lint:
                cargo clippy -- -D warnings

            test:
                cargo test --release
            ```

    *   **`mise.toml`** (Standard):
        ```toml
        [tools]
        node = "lts"
        python = "3.13"
        # rust = "stable"  # Uncomment for Rust projects
        hivemind = "latest"

        [env]
        _.file = ".env"
        _.python.venv = { path = ".venv", create = true }
        ```

    *   **`pyproject.toml`** (Standard Python):
        ```toml
        [project]
        name = "[name]"
        version = "0.1.0"
        requires-python = ">=3.13"
        dependencies = [
            "fastapi",
            "uvicorn[standard]",
            "pydantic-settings",
            "structlog",
            "httpx",
        ]

        [dependency-groups]
        dev = [
            "basedpyright",
            "pytest",
            "pytest-asyncio",
            "pytest-cov",
            "ruff",
        ]

        [tool.ruff]
        line-length = 100
        target-version = "py313"
        [tool.ruff.lint]
        select = ["E", "W", "F", "I", "B", "C4", "UP", "ARG", "SIM"]
        ignore = ["E501", "B008"]
        [tool.ruff.lint.per-file-ignores]
        "tests/*" = ["ARG001", "ARG002"]

        [tool.basedpyright]
        typeCheckingMode = "standard"
        ```

    *   **`.gemini/settings.json`**:
        ```json
        {
          "mcpServers": {
            "chrome-devtools": {
              "command": "npx",
              "args": ["-y", "chrome-devtools-mcp@latest", "--headless"]
            }
          }
        }
        ```

4.  **Standard Code Patterns (To Implement):**

    *   **`src/config.py`** (Python):
        ```python
        from pydantic_settings import BaseSettings, SettingsConfigDict
        from functools import lru_cache

        class Settings(BaseSettings):
            model_config = SettingsConfigDict(env_file=".env", extra="ignore")
            debug: bool = False

        @lru_cache
        def get_settings() -> Settings:
            return Settings()
        ```

    *   **`tests/conftest.py`** (Python):
        ```python
        import pytest
        from unittest.mock import AsyncMock

        @pytest.fixture
        def mock_service():
            return AsyncMock()
        ```

5.  **Initialize & Install:**
    *   `git init`
    *   **Python:** `uv init`, `uv add ...`
    *   **Rust:** `cargo init`
