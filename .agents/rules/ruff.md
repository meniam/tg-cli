# Ruff Rules

Ruff is the only linter and formatter. Applies to `[tool.ruff]` in
`pyproject.toml`, rule selection, suppression, CI.

**This file is a recommended starting point, not a mandate.** It is what a new
project gets on day one, when nothing is known about it yet. A service, a
library and a throwaway script do not need the same rules, and a project learns
what it needs by running. Narrowing or widening this config in a project is
expected, not a violation — see `Adapting`.

## Config

- In uv projects, all Ruff config lives in `pyproject.toml`. No `ruff.toml`, no
  `.ruff.toml`: one file per project already holds `uv`, `pytest` and
  `mypy` config, and a second file drifts from it.
- `cache-dir = "var/cache/ruff"`, per `python.md`.
- Normally omit `target-version`: Ruff infers it from project `requires-python`;
  writing it twice means one copy is eventually wrong.
- Keep the default line length initially; `E501` is off by default.
  The formatter uses line length as a wrapping target, not a hard limit.
  A project with type-heavy signatures may raise it to 100.
- `src = ["src"]` so isort resolves first-party imports.
- `extend-exclude = [".agents"]`. Skill and agent scripts live next to the
  project but are not the project; they should not answer to its config.
- Set `force-exclude = true` so exclusions also apply to explicit filenames,
  including files passed by pre-commit.
- Full uses `preview = true` and `explicit-preview-rules = true` under
  `[tool.ruff.lint]` for the individually selected preview `DOC` rules.
  Do not enable preview globally: that also opts the formatter into preview.
  Core does not need preview unless it explicitly selects a preview rule.

```toml
[tool.ruff]
cache-dir = "var/cache/ruff"
src = ["src"]
force-exclude = true
extend-exclude = [".agents"]

[tool.ruff.format]
docstring-code-format = true
```

## Version

- Full pins Ruff exactly in `[dependency-groups] dev` (current preference:
  `ruff==0.16.6`) because it selects preview rules. Check rule availability
  against the project's pinned version when adopting or upgrading the config.
  Core may use a lower bound with the exact version held in `uv.lock`.
- `uv sync` and `uv add` prefer locked versions when constraints permit;
  changed constraints can require another version. Do not rely on them never
  upgrading a package.
- Keep Ruff upgrades and resulting code/config changes separate from features.
  Change the exact pin to the chosen version first, then run
  `uv lock --upgrade-package ruff`, `uv run --locked ruff check --fix --no-unsafe-fixes .`,
  `uv run --locked ruff format .`, and the project checks. Read the diff.

## Rule Selection

- Select rule sets explicitly. Never `select = ["ALL"]`: it enables new
  rules on every upgrade, and it contains mutually exclusive rules
  (`D203`/`D211`, `D212`/`D213`) that cannot both be satisfied.
- Every selected set answers "would this failing check be worth reading?".
  If no, it is not selected — the formatter handles it silently instead.
- Complexity and refactor rules (`PLR`, `C90`) are not selected. An agent
  answers `PLR0915` by cutting a function into `_helper_1`/`_helper_2`,
  which passes the check and reads worse. Use review for that, not ruff.
- `FA` is not selected. It exists to require `from __future__ import
  annotations`, which `requires-python = ">=3.14"` makes unnecessary; on
  3.14 it never fires, so it only looks like a check.

Two tiers. **Core** is every piece of Python, including a 40-line script.
**Full** adds what only pays off in code that will be read again.

| Tier | Set                        | Why                                     |
| ---- | -------------------------- | --------------------------------------- |
| core | `F`, `E4`, `E7`, `E9`      | Real errors, not style                  |
| core | `B`, `RUF`, `PIE`          | Bugs: mutable defaults, dead branches   |
| core | `PGH004`                   | Reject blanket `noqa` comments           |
| core | `PLE`                      | Pylint errors only                      |
| core | `I`, `UP`, `C4`, `SIM`     | Import hygiene and simplification |
| core | `PERF`, `FURB`             | Performance idioms and modernization                                    |
| core | `S`                        | Injection, subprocess, weak crypto      |
| core | `DTZ`                      | Naive datetime                          |
| full | `ASYNC`                    | Missing `await`, blocking call in async |
| full | `BLE`, `TRY`, `RSE`, `RET` | Swallowed and mishandled exceptions     |
| full | `LOG`, `G`                 | Logging misuse                          |
| full | `ARG`, `SLF`, `A`, `N`     | Unused args, private access, shadowing  |
| full | `PLW`                      | Pylint warnings                         |
| full | `PT`                       | pytest structure                        |
| full | `PTH`, `T20`               | `os.path` → `pathlib`, no stray `print` |
| full | `D`, `DOC*`, `ANN`         | Docstrings and annotations, below       |
| full | `ERA`, `TD`                | Dead code, undescribed TODO             |
| full | `TC`                       | Typing import hygiene                   |

```toml
[tool.ruff.lint]
preview = true
explicit-preview-rules = true
select = [
    "F", "E4", "E7", "E9",
    "B", "RUF", "PIE", "PGH004",
    "S", "ASYNC", "DTZ",
    "BLE", "TRY", "RSE", "RET",
    "LOG", "G",
    "ARG", "SLF", "A", "N",
    "PLE", "PLW",
    "PT", "PTH", "T20",
    "D", "ANN",
    "DOC102", "DOC201", "DOC202", "DOC402", "DOC403", "DOC501", "DOC502",
    "ERA", "TD",
    "I", "UP", "C4", "SIM", "PERF", "FURB", "TC",
]
ignore = [
    "D105",    # magic methods: the name is the contract
    "D107",    # __init__: documented in the class docstring
    "TRY003",  # long messages in raise are fine without EM selected
    "RUF001",  # ambiguous unicode: comments and docstrings are in Russian,
    "RUF002",  # so Cyrillic letters are the norm, not a homoglyph attack
    "RUF003",
    "TD002",   # TODO author: these projects have one
    "TD003",   # TODO issue link: there is no tracker to link to
]

[tool.ruff.lint.pydocstyle]
convention = "google"

[tool.ruff.lint.pydoclint]
ignore-one-line-docstrings = true

[tool.ruff.lint.flake8-annotations]
mypy-init-return = true

[tool.ruff.lint.flake8-type-checking]
runtime-evaluated-base-classes = ["pydantic.BaseModel"]

[tool.ruff.lint.per-file-ignores]
"tests/**/*.py"      = ["D", "DOC", "ANN", "S101", "ARG", "SLF"]
"scripts/**/*.py"    = ["D", "DOC", "ANN", "T20"]
"src/**/__init__.py" = ["D104"]
```

Notes on entries that look arbitrary:

- `D203`/`D213` are **not** in `ignore`. `convention = "google"` already
  disables the conflicting `D` rules; listing them again is a second copy
  of the same decision.
- `RUF001`-`RUF003` are disabled for Russian text to avoid homoglyph noise.
- `TD002`/`TD003` are disabled: TODOs need no author or issue link. Other
  selected `TD` rules still apply, including the colon (`TD004`) and
  description (`TD005`): `# TODO: handle empty input`. `FIX` is not selected
  because the presence of a described TODO is not itself a failure.
- Keep `F401` in `__init__.py`. Declare re-exports with `__all__` or
  `from .module import Name as Name`; accidental unused imports stay visible.

## Library-aware settings

Rules that reason about imports and defaults need to know about the
frameworks in use, or they produce fixes that break at runtime.

- Configure runtime annotation handling for models actually defined in the
  project. For Pydantic subclasses use
  `runtime-evaluated-base-classes = ["pydantic.BaseModel"]`; a transitive
  dependency alone does not require a setting. For other frameworks select
  the appropriate base-class or decorator setting using its qualified name.
  Verify runtime model construction after moving annotation imports.
- FastAPI `B008` findings depend on the call, annotation and Ruff version.
  Use `Annotated` metadata where supported, or configure known framework
  factories through `flake8-bugbear.extend-immutable-calls`, e.g.
  `["fastapi.Depends", "fastapi.Query"]`. Check the actual finding before
  removing an existing exception; do not disable `B008` globally.
- `A002` flags `id` and `type` as parameter names — exactly what external
  APIs (marketplaces, ERP, payment gateways) hand you. Rename at the adapter boundary
  where the payload becomes a domain object, rather than suppressing the
  rule in every client module.
- `S607` concerns partial executable paths: resolve a trusted executable with
  `shutil.which` and handle `None`, or use a known absolute path. This does
  not establish argument safety and does not necessarily eliminate `S603`.
  Validate arguments and use a list without a shell; a remaining false positive
  may get a narrow `# noqa: S603` explaining why the call is safe.
- For a SHA1 cache key outside security-sensitive use, express intent with
  `hashlib.sha1(data, usedforsecurity=False)` instead of suppressing `S324`.

## Docstrings

Full uses `D` for docstring presence/style and selected `DOC` checks for
some mismatches with the implementation. Neither guarantees useful prose.

- `DOC102` flags extra documented parameters; it does not require every
  parameter to be documented. `D417` can check an existing `Args` section,
  but does not guarantee that the section exists. Review completeness,
  return/exception contracts and meaning; do not treat lint success as proof.
- Private functions need no docstring. `D103` covers public functions
  only, so a leading `_` is already exempt — do not add an ignore for it.
- A docstring never restates the signature. Names and types are already
  visible. It states, in this order: why the function exists and who
  calls it; assumptions and invariants that must hold on input; side
  effects — I/O, argument mutation, global state; why this approach, if
  the obvious one failed; edge cases. Nothing to add beyond the
  signature → one line, and no more. `ignore-one-line-docstrings = true`
  exempts short docstrings from `DOC` checks; review still checks whether a
  short description is sufficient for the contract.
- Verify non-trivial contracts with focused tests or executable examples.
  Use doctests for deterministic pure functions; use ordinary pytest tests
  for async, network, filesystem and fixture-heavy behaviour. Enable
  `--doctest-modules` only when the project has suitable doctests, and scope
  collection to intended paths. A doctest example (implementation omitted):

```python
def parse_range(spec: str) -> tuple[int, int]:
    """Parse a "start-end" range specification.

    Bounds are inclusive. Open ranges are rejected rather than clamped,
    because a caller that omits a bound is always a config typo here.

    Args:
        spec: Two integer bounds separated by a hyphen.

    Returns:
        Inclusive lower and upper bounds.

    Raises:
        ValueError: The syntax is invalid or the end precedes the start.

    >>> parse_range("3-7")
    (3, 7)
    >>> parse_range("7-3")
    Traceback (most recent call last):
    ValueError: end must be >= start
    """
```

Example for a project whose doctests live in `src/example/parsing/`:
keep its normal test recipe (`uv run --locked pytest tests`) and add a
separate, explicitly scoped doctest command:

```sh
uv run --locked pytest --doctest-modules src/example/parsing
```

Replace the example path with an existing import-safe module directory.
Do not set `--doctest-modules` globally when only a subset is suitable.

- An unexplained decision inside a body is marked `# WHY:` and says what
  breaks without it. Greppable, and read first during review.

```python
# WHY: the driver returns naive UTC; comparing it to an aware value
# raises TypeError. The driver contract guarantees these values are UTC.
ts = ts.replace(tzinfo=UTC)
```

## Suppression

- `noqa` always carries the rule code and a reason. Bare `# noqa` is
  banned — it hides the next, unrelated violation on that line.

```python
subprocess.run(cmd, check=True)  # noqa: S603  # cmd is a literal list
```

- Select `PGH004` to reject blanket `noqa`. `RUF100` reports unused
  suppressions and removes them when fixes are applied; review the diff.
- Fix real defects. An agent may add a narrow suppression for a verified
  false positive or unavoidable external API constraint without asking first.
  Include the code and a concrete reason, verify affected behaviour, and
  report the exception. Do not suppress an unexplained failure to get green.
- Ask only when a choice requires missing user intent or changes an agreed
  requirement. Routine local exceptions are within the task's scope;
  record lasting policy changes using the project's decision-log convention.

## Fixes

- Use `ruff check --fix --no-unsafe-fixes` for automatic fixes: `--fix` alone
  can apply unsafe fixes when `unsafe-fixes = true` is set in configuration.
  Do not promote unsafe fixes through `lint.extend-safe-fixes`. Safe is Ruff's
  classification, not a guarantee; review changes and run relevant checks.
  `--unsafe-fixes` may change runtime behaviour: it is run interactively,
  with the diff read, never in CI and never by an agent.
- Order matters: `ruff check --fix --no-unsafe-fixes` first — it reorders imports and deletes
  code — then `ruff format`.
- Require a format check in project verification. Format-on-save and
  pre-commit are optional conveniences; configure them when used. Let the
  formatter handle mechanical formatting rather than debating it in review.

## Checks

- Lint and format run together, with no fixing, from one recipe:

```
uv run --locked ruff check --no-fix .
uv run --locked ruff format --check .
```

- Use the same lint/format policy in CI and `just lint`. Pre-commit may check
  only changed files; CI checks the full intended scope.
- Pre-commit and editor Ruff versions match the resolved version in `uv.lock`,
  not a lower-bound dependency constraint. Point the editor at the project
  executable and update any separate pre-commit pin when the lock changes.
- Ruff is not a type checker. It does not catch a hallucinated method or
  a wrong signature — the two things generated code gets wrong most
  often. `mypy --strict` is a separate gate, see `python.md`.

## Adopting on existing code

1. Choose core/full and domain-specific settings before fixing findings.
2. Run `uv run --locked ruff check --fix --no-unsafe-fixes .`, then format and review the diff.
3. Group remaining findings by rule; fix bugs before documentation/style debt.
4. A temporary baseline may use `uv run --locked ruff check --add-noqa .`
   when gradual adoption is justified. Inspect every generated suppression,
   add a reason or a reference to a documented baseline with removal scope,
   and keep this change separate from features. Do not baseline unexplained
   correctness or security findings. This is an explicit exception to the
   normal per-finding suppression policy, not a way to hide new failures.

Project-specific finding counts and incident narratives belong in project
learnings, not in these shared defaults.

## Adapting

The config is a default, and defaults get overridden with a reason:

- A throwaway script or a scratch project takes the **core** tier only.
  `D`/`DOC`/`ANN` on a file nobody will read again is cost without benefit.
  For a temporary standalone file outside a project, apply the `Scope`
  exception in `python.md`; do not scaffold project infrastructure for it.
  A temporary file inside a project retains the project's existing checks.
- A project may raise `line-length`, add a set, or drop one that fights its
  domain. The change goes in its `pyproject.toml` with a comment saying why.
- Do not use `ALL` or bare `# noqa`. Dropping a rule that is unsuitable for
  the domain is allowed with a reason; hiding a real defect is not. Agents
  can make routine adaptations within the task without requesting approval.
- Record lasting deviations in the project decision journal, following
  `decisions.md` (normally separate files in `.agents/decisions/`); do not
  append decisions to the shared rule file itself.

## Checklist

Project infrastructure checks apply to uv projects; temporary standalone
files outside projects follow `Scope` in `python.md`.

1. Ruff config is in `pyproject.toml`; core/full and domain adaptations are clear.
2. `cache-dir = "var/cache/ruff"`; `.agents` is excluded with `force-exclude = true`.
3. Target Python is inferred from `requires-python`; custom line length has a reason.
4. `select` is explicit and includes `PGH004`; `ALL` is not used.
5. `PLR`, `C90`, `FA` and `E501` stay off by default; adaptations are documented.
6. Preview, when needed, is scoped to lint with `explicit-preview-rules = true`.
7. Full pins Ruff exactly; core may use a lower bound and committed lockfile.
8. Full selects `D` and individual `DOC` rules with Google style; core need not.
9. Runtime annotation settings match models and frameworks actually used.
10. Re-exports are explicit; `F401` is not blanket-disabled in `__init__.py`.
11. Docstrings explain meaningful contracts; relevant tests or examples verify them.
12. Doctest collection is enabled and scoped only where appropriate.
13. Suppressions name codes and reasons; baseline debt has a removal scope.
14. CI and `just lint` run locked, non-mutating lint/format checks.
15. Maintained projects also run mypy strict; CI checks unused overrides.
16. Automatic fixes disable unsafe fixes explicitly and do not promote them to safe.
17. Editor and pre-commit Ruff versions match the resolved version in `uv.lock`.
