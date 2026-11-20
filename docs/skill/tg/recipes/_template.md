---
title: Recipe title
task: «фраза пользователя», «ещё одна фраза»
writes: false
access: [none]
commands: [chat list]
live_checked: no
tags: [topic]
---

# Recipe title

## When to use

Two or three sentences: which user request this recipe matches and how it differs from the neighboring ones.

## Prepare

What must be true before the run: the Daemon is running, the login exists, the chat is found, the needed lists in `access.json` are filled in by the human.

## Commands

```bash
# Full commands, ready to copy. Show a concrete argument form (`@release_team`, `<chat_id>`) and mark every placeholder in the text.
```

## Notes

- Why these flags were chosen.
- What the response contains and how to read it.

## Pitfalls

- Pagination, limits, partial results, exit codes likely to appear here.

## Verify

How to confirm the recipe worked correctly.

---

Frontmatter fields (in a finished recipe, delete this block after the line):

- `title`: recipe title.
- `task`: user phrases in «», which open the recipe.
- `writes`: `true` if the recipe deliberately changes Telegram, a file, or the local database (`chat sync`, an export file). The automatic saving of what a read returns does not count. `just skill-check` requires it when `commands` has a write command.
- `access`: what the human must add to `access.json` beforehand: `none`, `write`, `files`, `downloads`, `admin`, `create`, `dialogs`, `sessions`, `profile`. `none` stands alone. The needed entry depends on the command: `message send*` needs `write`; sending files also `files`; downloads, `export-month` and `export-verify` need `downloads`; `account terminate` needs `sessions`; chat moderation needs `admin`. A recipe that only reads, runs login commands, or writes only to the local database (`chat sync`) needs no entry, so `none` fits it. `just skill-check` checks the common cases.
- `commands`: the `tg` commands the recipe uses, as paths from `tg schema` (`message history`). `just skill-check` compares them with the schema and with the code in the text.
- `live_checked`: a date if the recipe's main commands were checked on a live account and this is recorded in the reference; `partial` or `no` otherwise.
- `tags`: topics for search.
