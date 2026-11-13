# Project Rules

Shared rules of 4Partners projects by Eugene Myazin. One file, one topic.

**Portable.** This folder is copied into a project's `.agents/rules/` as is
and never edited there: a change goes to the master copy
(`~/.agents/rules`) and is copied again. The rules name no project, path,
decision or entity of a particular project; their examples come from the
4Partners world (order, product, partner, supplier, shipment). What only a
project knows — its journals, its entity icons, places not yet aligned —
lives in that project's `.agents/rules/project/`, which is never copied.

**Defaults, not law.** These files are what a project gets before anything is
known about it. A project that knows better wins — see `Precedence`.

| File            | Topic                                                                          |
| --------------- | ------------------------------------------------------------------------------ |
| `db.md`         | PostgreSQL naming: schemas, tables, columns, constraints, indexes; migrations  |
| `plans.md`      | Task plans in `.agents/plans/`: names, checkboxes, statuses, branch, structure |
| `decisions.md`  | Decision log in `.agents/decisions/`: what counts, record format, `_TOC.md`    |
| `learning.md`   | Learnings in `.agents/learnings/`: facts found by experience, `_TOC.md`        |
| `changelog.md`  | `CHANGELOG.md` in the project root: changes visible outside the code, by date  |
| `justfile.md`   | `justfile`: recipes, help screen                                               |
| `python.md`     | Python projects on uv: tool caches, layout, tooling                            |
| `ruff.md`       | Ruff as linter and formatter: config, rule selection, suppression, CI          |
| `testing.md`    | Verification for code changes and bug fixes; threshold for permanent tests   |
| `response.md`   | Chat responses: direct answers, evidence, length and language                  |
| `docs_chain.md` | Texts and docs: author writes, glavred reviews; review package, limits         |
| `ui/`           | UI from the kit `@/shared/ui`: index `ui/AGENTS.md`, one file per topic        |

Read `testing.md` when changing code or configuration, fixing bugs, or adding,
reviewing or removing tests.

## Precedence

Order, strongest first:

1. The project's own `AGENTS.md`, `.agents/rules/project/` and its docs
   (`docs/`, `.agents/<journal>/AGENTS.md`).
2. A decision recorded in the project's `.agents/decisions/`.
3. These shared rules.
4. The tool's own defaults.

A project rule wins over a shared rule. It needs no permission and it is not a
violation: these files describe a good starting point, and a project learns
things the starting point could not know — an external data source, a platform
limit, a team convention.

What a deviation does need is a record. A lasting one lives in the project: a
line in `.agents/rules/project/` or the project `AGENTS.md`, a section in the
project docs, or a file in
`.agents/decisions/` when the choice had alternatives worth naming
(`decisions.md`).

The real problem is the undocumented deviation. Code that silently contradicts
a rule leaves the next agent to guess which of the two is current, and that
guess is re-made on every task — usually as an apology in a plan, sometimes as
a change back to the rule that breaks something.

Reading the tone: rules here are written as statements ("table is singular"),
not as preferences. That is for checkability, not force. Read a categorical
line as "this is the default answer", not "no other answer exists". Where a
question has legitimate project-specific answers, the rule says so on the spot.

## Journals

Four journals, one question each. The reader picks the file by the question.

| Question                | Journal   | Unit                          | Rules          |
| ----------------------- | --------- | ----------------------------- | -------------- |
| How do we do it?        | Plan      | file per task                 | `plans.md`     |
| Why is it like this?    | Decision  | file per decision + `_TOC.md` | `decisions.md` |
| What did we find out?   | Learning  | file per fact + `_TOC.md`     | `learning.md`  |
| What changed, and when? | Changelog | section per date              | `changelog.md` |

- Plan, decision, learning share the file name scheme
  `YYYYMMDD_HHMM_short_statement.md`: creation time, latin `snake_case`.
  Name never changes after creation.
- Every journal is live: the record goes in the same commit as the change.
- Plan → decision: a decision that outlives the plan gets a file in
  `.agents/decisions/` before the plan reaches `done`. Plan open questions
  that remain move to `_TOC.md` "Open questions".
- Plan → changelog: every change visible outside from "Definition of done"
  leaves a line under the date of the change.
- Decision vs learning: a choice between options is a decision; a verified
  fact with no alternative is a learning. A learning found on the way to a
  decision lives in that decision's `Trap` section.
- Learning never goes to the changelog.
- A rule for agents lives in the project `AGENTS.md` and links to the
  decision that introduced it. The decision is not retold there.

## How Rules Are Written

- A rule, not a discussion: "table is singular", no "preferably". The tone is
  categorical so the rule can be checked; precedence is handled by `Precedence`
  above, not by hedging every line.
- Every rule has an example. Every ban says what to use instead.
- A default that projects legitimately answer differently says so where it
  stands, in one line: what makes a project a fair exception, and that the
  exception is recorded. Better one honest line than a rule everyone quietly
  breaks.
- Every rule file ends with a `## Checklist`: numbered, one check per line.
- A rule that needs explaining is explained in one line. Longer means a
  document, not a rule.
- The reason lives in the rule itself, in one phrase. A shared rule never
  links to a project decision, file or path: the copy must read the same in
  every project.
