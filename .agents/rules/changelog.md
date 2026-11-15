# Changelog Rules

How agents record what changed in the project. The changelog answers "what
changed and when", the plan "how", the decision "why", the learning "what we
learned". The changelog reader is whoever updates the project or looks for
the day something broke. The agent is mostly its writer: it records cheaply
and looks things up by search, it does not read the whole history.

**Defaults, not law.** A project's `AGENTS.md` and docs win over this file; a
project file with the same name replaces it entirely. See `AGENTS.md`,
"Precedence".

## Location

- Folder `.agents/changelog/` in the project. One change, one file. There is
  no `CHANGELOG.md` and no `_TOC.md`: concurrent work adds different files, so
  merges do not conflict.
- File name as for plans and decisions: `YYYYMMDD_HHMM_short_statement.md`,
  latin `snake_case`, creation time. Never changes after creation.
- The current month lies in the folder root. A past month moves by `mv` to a
  subfolder `YYYY-MM/` once, at the start of the next month, in a separate
  commit. Files are never edited or merged during the move.
- **No releases and no `Unreleased`.** A release ritual assumes someone deploys
  and decides when a batch is cut. That decision is not made here, so the date
  of the record is the date of the change.
- The folder has its own short `AGENTS.md`: what lies here, that the files are
  read through the project's recipe, and a link to this file.

## What Is Recorded

Test: a reader outside the development team must either do something or see
the system work differently. A record exists only if one of the two is true.

Recorded:

- Product behaviour a user notices: a new capability, a changed result, a fix
  of a visible symptom that was in a released version.
- Contracts: API, data format, DB schema, events.
- Operator actions: environment variables, config, commands, deploy order,
  a migration with a manual step, a dependency with incompatibilities.
- Security.

Not recorded:

- Refactoring with no behaviour change, tests, documentation, plans and
  journals, text and typo edits, internal renames.
- Developer tooling and agent skills or instructions.
- Log events, metrics, log levels and alert texts, unless the operator must
  act on them.
- Changes of internal libraries or a UI kit with no visible effect.
- Intermediate steps of a feature: one record per feature, written when it
  becomes visible outside.
- A fix of an error that was never in a released version.
- A repeat edit of the same place: the existing record is edited, not a new
  one added.

## When

- The record is added in the same commit as the change.
- A change that continues an earlier record edits that file. The name stays.
- A plan that reaches `done` leaves at least one record, or an explicit note in
  the plan that nothing visible outside changed.

## Format

```markdown
---
date: 2026-09-05T14:30:00+03:00
type: Изменено
breaking: false
decision: 20260831_1554_short_statement
---
API заказов возвращает московское время без смещения: `2026-06-25T12:05:00`.
```

- `date` is required: the moment of the change, ISO 8601 with time and UTC
  offset. The recipe fills it, an agent does not type it. The recipe writes
  Moscow time (`Europe/Moscow`, `+03:00`) unless the project sets another
  timezone in its `AGENTS.md`. It never changes, not even when the record is
  edited later.
- `type` is required, one of six: `Добавлено`, `Изменено`, `Устарело`,
  `Удалено`, `Исправлено`, `Безопасность`.
- `breaking` is optional, `true` only for an incompatible change. The line
  then says what to do: "ключи партнёров переехали в `public.partner_api_key`,
  перевыпустите командой `just keys migrate`".
- `decision` is optional: the name of the decision file without extension.
  There is no link to the plan.
- The file name carries the same moment to the minute. If the two differ,
  `date` wins.
- The body is one line, at most 200 characters. It is the result as the reader
  outside sees it: "Индекс снова используется", not "Починили индекс". What
  does not fit goes to the decision or the plan; the line keeps the outcome.
- `Устарело` names the replacement and the removal date.

## Recipes

A project provides recipes with these names; the implementation is its own
(`justfile.md`).

- `just changelog add`: creates the file with the current `date`, checks `type`
  and the 200-character limit, takes the optional fields. The agent does not write the file by hand
  and does not read the folder to add a record.
- `just changelog list`: prints the records newest first under a heading per day,
  `## 5 сентября 2026, Суббота`, and filters by days, type, `breaking`.
- `just changelog archive`: moves past months to `YYYY-MM/`.

To find when something changed, search with `just changelog list`, `grep` over the
folder, or `git log -S`. Do not read the folder in full.

## Relation to Journals

- Plan: one record per change visible outside from the plan's
  "Definition of done".
- Decision: the record names it in `decision` when the change follows from it.
  The decision is not retold in the record.
- Learning never goes to the changelog: it is not a project change.

## Checklist

1. The record is in the same commit as the change.
2. The reader outside must act or sees different behaviour; otherwise no
   record.
3. `date` is set by the recipe with time and offset, and never changes.
4. `type` is one of the six and matches what happened.
5. The body is one line, at most 200 characters, a result.
6. `breaking` is set for an incompatible change and the line says what to do.
7. `decision` names the decision file, if any; there is no link to the plan.
8. A continuation edits the existing file; the name does not change.
9. The record is added by `just changelog add`, not by hand.
