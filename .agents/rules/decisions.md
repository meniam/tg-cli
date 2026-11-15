# Decision Rules

How agents record project decisions. A record says what was chosen, what was
rejected and what it costs, so another agent can keep the choice or revise it
knowingly.

**Defaults, not law.** A project's `AGENTS.md` and docs win over this file; a
project file with the same name replaces it entirely. See `AGENTS.md`,
"Precedence".

## Reading

Read this before changing code or choosing between alternatives, not only when
writing a record.

- Before changing files covered by a decision's `paths`, find the decisions and
  read them. Use a navigation skill first, if the project has one (`pathfinder`,
  `scout`, `atlas`). If there is none or it finds nothing, search the file and
  each of its parent folders, not `_TOC.md`:

```sh
f=src/Order/Create/OrderCreator.php
while [ "$f" != . ]; do grep -l -E "^paths:.*[[, ]$f/?[],]" .agents/decisions/2*.md; f=$(dirname "$f"); done | sort -u
```

- A decision without `paths`, or about a topic rather than a file, is found by
  keyword: `grep -il '<word>' .agents/decisions/2*.md`, then the titles in
  `_TOC.md`.
- Before relying on a decision, check that no later record changed it. The old
  record does not say so; search for a back-reference and read every record
  found (`supersedes`: the old decision no longer applies; `revises`: it
  applies with the change):

```sh
grep -l -E '^(revises|supersedes):.*<name>' .agents/decisions/2*.md
```

- Check what changed under its `paths` since it was made:

```sh
git log --oneline --since=<decided> -- <paths>
```

  Changes do not prove the decision wrong; compare the code with `Decision`. If
  the code no longer follows it, do not rely on it: write a record that
  revises or supersedes it.
- Do not read the whole journal.

## Location and Naming

- `.agents/decisions/` holds decision files, `_TOC.md`, `AGENTS.md` /
  `CLAUDE.md`. Nothing else.
- File: `YYYYMMDD_HHMM_short_statement.md`, as for plans, latin `snake_case`, a
  statement, not a topic. Never changes. Frontmatter fields name files without
  `.md`. Example: `20260831_1554_time_is_moscow_without_zone`.
- One decision, one file. Records reference each other by link with the
  statement, no ordinals.
- Size guideline: 20–80 lines. Longer means two decisions or a document.

## What Is Recorded

A decision that had an alternative: choice of technology, schema, storage,
module boundary, convention, data format; rejection of the obvious option;
deviation from a project rule with its reason. A choice that is cheap to
reverse and visible in the code stays in the plan's `Decision log` with no
file; a costly one (schema, contract, storage, module boundary, manual steps
for operators) gets a file.

Not recorded: implementation with no choice, text edits, reformatting, one-off
debugging, an open question (it belongs in the plan's "Open questions"), a fact
found by experience with no choice involved (a learning, see `learning.md`), a
rule for agents by itself (on the user's request it goes to `AGENTS.md` as one
line that links here).

Test: "we chose X over Y because Z" completes without guessing.

## When

- A record is written in the same commit as the code; without code or plan
  (debugging, review, operations), the same day.
- Decisions are noticed at checkpoints, not by feel: the user picks an option;
  the agent weighs two or more options and picks one; before the commit the
  diff is checked against "What Is Recorded" and against the `paths` of
  existing decisions. If the diff changes code covered by a decision's `paths`
  against that decision, the commit includes a record that revises or
  supersedes it.
- With a plan, every such choice goes to the plan's `Decision log` first. A
  decision that outlives the plan gets a file before the plan reaches `done`;
  the plan row links to the file, the file does not link back to the plan.
- A costly choice is confirmed by the user before the work. If asking was
  impossible, the record says `confirmed: no` and the agent names it in the
  final report. When the user confirms, `confirmed` becomes `yes`.

## Structure

`Decision`, `Why` and `Cost` are required. `Context` is required when `revises`
or `supersedes` is set; otherwise `Context` and `Trap` are omitted when empty.

```markdown
---
decided: YYYY-MM-DD
decided_by: {name: "<name>", email: <email>, github: <login>}
confirmed: yes
paths: [<path>, <path>]
---
# <Statement in one line>

## Context

What forced the choice, with measured facts: "Seq Scan, 1750 of 1816 rows
discarded by filter", not "the query was slow".

## Decision

What was chosen, enough to repeat without reading the code: table names,
paths, config keys, exact SQL, values.

## Why

Why this and not the alternatives; every rejected option is named with the
reason it lost.

## Cost

What we pay: manual sync, duplication, broken clients, extra query on start;
the condition for revisiting, if known. `None.` is allowed and deliberate; a
decision with no cost is suspicious.

## Trap

A learning found on the way to this decision: behaviour that contradicts the
docs or the name. A standalone learning goes to `learning.md`.
```

- `decided`: the date from `date` (see `AGENTS.md`, "Дата и время"). Never
  changes.
- `decided_by`: name, email and GitHub login of the one person accountable, who
  chose or confirmed the decision (see "Who Decided").
- `confirmed`: `yes` if the user chose or confirmed it, `no` if the agent
  decided alone (see "When").
- `revises`: the old decision still stands with a change. `supersedes`: it no
  longer applies. Both are lists of the file names of the records this one
  changes or replaces, for example `supersedes: [20260901_1010_keys_in_env,
  20260901_1020_keys_in_file]`. Set at creation, not written when empty.
- `paths`: the real files or folders the decision concerns; a folder covers
  everything under it. Required for a decision about code, optional otherwise.
  Keep them narrow, a file or a small folder: a broad path changes daily and
  makes the staleness check in "Reading" meaningless.
- The time of day is not in the file: see git history.

## Examples

A confirmed decision, in full:

```markdown
---
decided: 2026-08-31
decided_by: {name: "Ivan Petrov", email: ivan@example.com, github: ipetrov}
confirmed: yes            # `no` if the agent decided alone
paths: [config/packages/doctrine.yaml, migrations/]
---
# Time is stored as Moscow wall time, without zone

## Context

All orders come from one timezone and are read by one team.

## Decision

`created_at`, `updated_at` and every other `_at` column are `timestamp`
without zone. The DB timezone is `Europe/Moscow`
(`alter database <db> set timezone = 'Europe/Moscow'`); the app never
overrides the session timezone.

## Why

`timestamptz` lost: it adds a zone nobody reads and makes reports depend on
the client zone. UTC lost: every report would convert back to Moscow.

## Cost

Several timezones later means a migration of every `_at` column.
```

A revision, in full. The new record points back with `supersedes`, and its
`Context` says what changed:

```markdown
---
decided: 2026-08-31
decided_by: {name: "Anna Sidorova", email: anna@example.com, github: asidorova}
confirmed: yes
supersedes: [20260831_1748_meetings_table]
paths: [migrations/]
---
# Tables are singular

## Context

Replaces [Table is called `meetings`](20260831_1748_meetings_table.md): the
plural name broke the naming rule and `meeting` joins read backwards.

## Decision

The table is `meeting`. Every table name is a singular noun.

## Why

Plural lost: one row is one meeting, and the rule must hold for every table.

## Cost

One rename migration and a changed foreign key name.
```

## Revisions

- Records are never rewritten. A changed or wrong decision gets a new record
  whose `Context` links the old one and says what changed. Files are not
  deleted.
- The link is stored once, in the new record's `revises` or `supersedes`; the
  old record is not edited. Before saving, check that every name exists:
  `ls .agents/decisions/<name>.md`.
- After creation only three things change: `confirmed` (`no` to `yes`),
  `decided_by` (together with `confirmed`, when someone else confirms, in one
  commit) and `paths` (to follow a move of the files).

## Who Decided

The agent fills `decided_by` at creation from the current git and GitHub
identity, not from memory:

```sh
git config user.name      # name
git config user.email     # email
gh api user --jq .login   # GitHub login
```

- It describes the person the agent works for; with `confirmed: no`, the one
  who will be asked to confirm. Without `gh`, write `github: unknown`.
- One person only. If several decided together, name the one who confirmed and
  say in `Context` who else took part: "decided together with Anna Sidorova".
- Git history is the cross-check and the source of the time of day:

```sh
git log --diff-filter=A --follow --format='%H %an <%ae> %as' -- <file>
gh api repos/{owner}/{repo}/commits/<sha> --jq '.author.login'
```

  Use `--diff-filter=A --follow`, not `git blame`: a bulk edit would show its
  author. The login resolves only if the commit email is attached to a GitHub
  account; otherwise it is `null`.

## `_TOC.md`

The only place where the whole list is visible. Every file has a line.

```markdown
# Decision Log

Index of `.agents/decisions/`. Rules: `decisions.md`.

- 2026-08-31 — [Time is stored as Moscow wall time, without zone](20260831_1554_time_is_moscow_without_zone.md)
- 2026-08-31 — [Tables are singular](20260831_1804_tables_are_singular.md)
```

- A line is the date, the statement verbatim as in the file, and a link. Order
  by file name, so a new line goes in its place, normally at the end. A line
  never changes after it is added.
- No revision marks: links between records live only in `revises` and
  `supersedes`.
- No open questions: they stay in the plan's "Open questions" table. A decided
  question gets a record if it passes the test, and the plan row links to it.

## Checklist

1. Title is a statement; the file name follows the plan scheme and never
   changes.
2. `Decision` reproduces without reading the code, `Why` names alternatives,
   `Cost` is written (`None.` is deliberate).
3. Frontmatter has `decided` (from `date`), `decided_by` (from `git config` and
   `gh api user`), `confirmed`; `revises` or `supersedes` only for a revision;
   no empty fields.
4. Every name in `revises` and `supersedes` exists (`ls`); the old record was
   not edited.
5. A decision about code has narrow `paths` that point to real files or
   folders.
6. References are links with statements, not ordinals; `_TOC.md` has the line.
7. A rule for agents is added to `AGENTS.md` only on the user's request, as one
   line linking here.
8. A costly choice was confirmed before the work, or `confirmed: no` is named
   in the final report.
