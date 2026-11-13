# Python Rules

Python projects on uv. Applies to `pyproject.toml`, project layout, tooling.

**A recommended starting point, not a mandate.** This is what a new project
gets on day one; a project adjusts it as it learns what it needs, and records
a lasting deviation in the project decision journal, following `decisions.md`
(normally a separate file in `.agents/decisions/`).

## Scope

Project requirements below apply to uv projects with `pyproject.toml`.
A temporary standalone file outside a project may use Ruff core via `uvx`
with an explicitly selected tool version; do not create a project, dev group,
lockfile, CI or decision journal solely for that file. Pass the applicable
core rules and target Python explicitly when no project config exists.
A scratch uv project still uses its existing dev group and lockfile.
Files inside an existing project retain that project's checks.

## Linting

Ruff is the only linter and formatter. Its config, rule selection, docstring
policy, suppression and adoption path live in `ruff.md`; this file only fixes
where its cache goes. Ruff and `mypy --strict` are two separate gates: ruff
does not check types, mypy does not check style.

## Tool caches

Project root stays clean. Tool caches live in `var/cache/<tool>/`.
`var/` is gitignored in every project.

- Set supported cache paths in `pyproject.toml`, so project commands and IDEs
  share the default. Use a temporary CLI or environment override only when
  the execution environment requires it, for example a read-only checkout.
- No config key for a cache → keep the default, do not fight it.
- Do not set `RUFF_CACHE_DIR` or `MYPY_CACHE_DIR` globally. Their precedence
  differs: Ruff's configured `cache-dir` overrides its environment variable;
  `MYPY_CACHE_DIR` overrides mypy's config.
- Regular project tools belong in `dev`; configure their cache when supported.
  One-off tools may run through `uvx`: pass a supported cache option if needed,
  otherwise keep the default. Do not invent options or add a dependency just
  to control its cache.
- `PYTHONPYCACHEPREFIX` is not used: `__pycache__` sits next to modules in
  `src/`, not in the root, so it is not what makes the root dirty.
- `.venv` stays in the root. Moving it needs `UV_PROJECT_ENVIRONMENT`, and any
  run without that variable silently creates a second environment; IDEs also
  look for `./.venv`.
- Build artifacts (`build/`, `dist/`, `*.egg-info`) are not caches. Not moved.
- The uv package cache (`~/.cache/uv`) is shared across projects. Not moved.
- Agent tooling is not project tooling: `playwright-cli` output goes to
  `~/.cache/playwright-cli` via `PLAYWRIGHT_MCP_OUTPUT_DIR` in
  `~/.claude/settings.json`, set once globally, not per project.

| Tool     | Key                                   | Value                          |
| -------- | ------------------------------------- | ------------------------------ |
| ruff     | `[tool.ruff] cache-dir`               | `var/cache/ruff` (`ruff.md`)   |
| pytest   | `[tool.pytest.ini_options] cache_dir` | `var/cache/pytest`             |
| mypy     | `[tool.mypy] cache_dir`               | `var/cache/mypy`               |
| coverage | `[tool.coverage.run] data_file`       | `var/cache/coverage/.coverage` |
| ty       | —                                     | keeps no on-disk cache         |

## Python version

- New projects default to uv-managed Python 3.14. Existing projects retain
  their supported version unless an upgrade is part of the task.
- `[tool.uv] python-preference = "only-managed"` keeps project environments
  independent of Homebrew interpreter upgrades and PATH differences.
- `.python-version` records the chosen minor version (new-project default `3.14`), written by
  `uv python pin 3.14`. The patch level is managed centrally by
  `uv python upgrade`, never pinned per project.
- `requires-python` matches the supported floor; new-project default: `">=3.14"`.
- Diagnose environment failures before recreating `.venv`: inspect the error,
  interpreter path/version, dependency constraints and lockfile consistency.
  Recreate a confirmed broken environment with `rm -rf .venv` followed by
  `uv sync --locked`, after preserving any useful diagnostics. Do not promise
  a fixed recovery time or use recreation as the first response to any error.

## Dev tooling

- Dev tools live in `[dependency-groups] dev`, never in
  `[project.optional-dependencies]`. `uv sync` installs `dev` by default;
  extras need an explicit `--extra` and silently stay uninstalled — that is
  how a project ends up linted by another project's ruff.
- One group. Splitting into `test` / `docs` costs a flag or a
  `default-groups` entry and is forgotten once. Split only when something
  actually installs a subset.
- Baseline for maintained projects: `ruff`, `mypy`, `pytest`. A scratch script
  may use Ruff core alone; add pytest when there are tests or doctests to run.
- `dev` holds what runs on every change. What runs on a schedule goes through
  `uvx` — for example `pip-audit` for dependency CVEs. Do not add Bandit by
  default: its checks overlap Ruff `S`, but coverage is not guaranteed identical.
  Add a separate scanner when it provides needed coverage.
- A dependency audit must explicitly target project dependencies. For example,
  from the project root, export the locked production and dev dependencies:

  ```sh
  (
    set -eu
    audit_requirements=$(mktemp)
    trap 'rm -f "$audit_requirements"' EXIT
    uv export --locked --format requirements-txt --all-groups --no-emit-project --output-file "$audit_requirements"
    uvx pip-audit -r "$audit_requirements"
  )
  ```

  This includes all dependency groups, but no optional extras; add the extras
  used in deployment with `--extra NAME` (or `--all-extras` if compatible).
  Local/workspace packages need a project-specific audit scope. Do not run bare
  `uvx pip-audit`: its default target is the tool environment. Do not assume
  `pip-audit --locked` reads `uv.lock`; the export above is the audit input.
- Versions: lower bound in `pyproject.toml`, exact version in `uv.lock`.
  Reproducibility comes from the lock, not from `==`. An exact pin is for a
  tool whose config names internals of one version — ruff, because the config
  lists preview rule codes. Not mypy.
- Keep tool upgrades separate from feature changes: update constraints when
  needed, run `uv lock --upgrade-package <tool>`, then the relevant checks.
  An exact pin must change before the lock can select a newer version.
- Declare regular tools in `dev`, install that group, and run them through
  `uv run`. This combination selects the project version. `uv run` alone can
  resolve a missing executable from external PATH; do not treat it as proof
  that Ruff/mypy/pytest is installed in the project environment.
- CI and verification recipes use `uv run --locked <tool>` so stale project
  metadata fails instead of rewriting `uv.lock`. Dependency updates are explicit.
- Type stubs are dev dependencies (`uv add --dev types-PyYAML`), never a
  reason to widen mypy's import handling.

## Type checking

Maintained projects use mypy strict as part of lint. A scratch script may
start with Ruff core; retain any existing type-check gate.

```toml
[dependency-groups]
dev = ["mypy>=2.3"]

[tool.mypy]
python_version = "3.14"          # floor supported, not the interpreter running mypy
files = ["src", "tests"]         # example: list only existing code/test directories
cache_dir = "var/cache/mypy"
strict = true
warn_unreachable = true          # not part of strict: dead branches from bad narrowing
warn_unused_configs = true       # requires --no-incremental to check unused overrides
enable_error_code = [
    "ignore-without-code",       # every `# type: ignore` names what it silences
    "redundant-expr",
    "truthy-bool",
]
plugins = ["pydantic.mypy"]      # where pydantic is used
```

- Locally run `uv run --locked mypy` in the same `lint` recipe as Ruff
  (`ruff.md`). Do not pass positional paths:
  `files` is the target: include existing application, script and test paths.
  Do not create an empty `tests/` directory just to satisfy this example.
- CI runs `uv run --locked mypy --no-incremental` to also check unused
  overrides. Local incremental runs are allowed for speed.
- `ignore_missing_imports` is **per-module, never global**. Global means the day
  a real import breaks, mypy stays silent about it.

```toml
[[tool.mypy.overrides]]
module = ["untyped_lib.*"]
ignore_missing_imports = true    # ships no types and has no stub package
```

- A missing stub is a dev dependency (`uv add --dev types-PyYAML`), not an
  override. Add a narrow override when no usable typing exists; explain it
  and remove it when the library gains types or stubs become available.
- `strict = false` in an override does not work — global `strict` expands into
  individual flags. Name the flag (`disallow_untyped_defs = false`) or use
  `ignore_errors = true`.
- Suppress with `# type: ignore[code]`, never bare. `ignore-without-code` above
  makes a bare one an error.
- `ty` is not used while it is pre-1.0 (`0.0.x`, no compatibility guarantees),
  even though it is fast and Astral-made. Revisit after 1.0.
- A project already at zero under strict stays there: the check is a gate, not
  a report.

## Checklist

Apply project-only checks to uv projects; use the standalone-file exception
in `Scope` for temporary files outside projects.

1. Python version and `requires-python` match the project's supported floor.
2. Regular tools are in `dev`; versions are locked in committed `uv.lock`.
3. Supported cache paths use `var/cache/`; `var/` is gitignored.
4. Missing cache configuration support is allowed; `.venv` stays in the root.
5. CI uses `uv run --locked`; tool upgrades are separate from feature changes.
6. Maintained projects run mypy strict against existing code and test paths.
7. CI checks unused mypy overrides with `--no-incremental`.
8. Missing stubs are installed when available; overrides and ignores are narrow.
9. Environment problems are diagnosed before `.venv` is recreated.
10. Project-specific defaults and exceptions are documented without routine approval stops.
11. Dependency audits receive an explicit export of the intended locked dependencies.
