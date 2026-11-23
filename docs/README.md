# Documentation

An index of the documentation of `tg`, a console client for a personal Telegram account. Read the row that matches your task; the rest is not needed.

## For people and agents who use the tool

| You want | Read |
| --- | --- |
| to install, run and understand the common rules: profiles, the Daemon, global flags, output formats, the answer envelope, exit codes, safety classes | [cli/overview.md](cli/overview.md) |
| to start the Daemon, log in, read the Daemon journal | [cli/daemon-and-auth.md](cli/daemon-and-auth.md) |
| to transfer a session between profiles | [cli/session.md](cli/session.md) |
| to inspect, show, or migrate configuration | [cli/config.md](cli/config.md) |
| to inspect or change profile settings and access permissions | [cli/settings.md](cli/settings.md) |
| to list, resolve and describe chats, read members, topics and common chats, mark a chat read, load a whole chat history, cap the saved messages of a chat | [cli/chats.md](cli/chats.md) |
| to read folders, account sessions, blocked peers and notification defaults | [cli/folders-and-account.md](cli/folders-and-account.md) |
| to read messages, search Telegram, read the context of a message or the comments of a post, build a message link, export saved messages, follow live events | [cli/messages-read.md](cli/messages-read.md) |
| to inspect and press buttons or set chat auto-delete | [cli/buttons-stickers-auto-delete.md](cli/buttons-stickers-auto-delete.md) |
| to send, edit, delete and forward messages, send files, react, pin and unpin, create polls, format text | [cli/messages-write.md](cli/messages-write.md) |
| to keep notes, search the local database, print the version, read the machine description of all commands | [cli/notes-and-search.md](cli/notes-and-search.md) |
| to turn voice messages, video notes and videos into text with the local engine: install the engine, watch the queue, mark chats, retry, backfill | [cli/voice.md](cli/voice.md) |
| to browse local chats, search messages and manage settings in a browser | [cli/web.md](cli/web.md) |
| to decide which chats the agent sees, where it may write and which directories it may send files from | [cli/access.md](cli/access.md) |
| an agent skill with the working rules for an AI agent (English, Russian trigger phrases) | [skill/tg/SKILL.md](skill/tg/SKILL.md) |

Every command of `tg schema` has its own section in `docs/cli/` with a heading `tg <command path>`: purpose, syntax, flags, safety class, preconditions, an example, an example answer and exit codes. The authoritative machine-readable contract is `tg schema`; verification status is tracked in [FEATURES.md](../FEATURES.md).

## For people who build and maintain the tool

| You want | Read |
| --- | --- |
| the feature list as an FRD with tags, acceptance lines and the implementation status of every requirement, including what is checked on a live account (Russian) | [FEATURES.md](../FEATURES.md) |
| detailed contracts of the behavior of shipped features: request journal, throttle, deep history load, local export, access lists, live event stream, message writing and the rest | [contracts.md](contracts.md) |
| the decisions with their alternatives (Russian) | [.agents/decisions/_TOC.md](../.agents/decisions/_TOC.md) |
| the plans, their steps, status and open questions (Russian) | [.agents/plans/](../.agents/plans/) |
| the instructions for agents that work in this repository | [AGENTS.md](../AGENTS.md) |
| the public face of the project: what it is, why, a quick start, status | [README.md](../README.md) |

## Languages

`docs/cli/` and the contracts are in English, the same as the help of the tool, the schema and `AGENTS.md`. `docs/skill/` is an agent skill in English; only its trigger phrases (the `description` and the `task` field of recipes) are Russian. `FEATURES.md`, the plans and the decisions are in Russian. Commands, flags and identifiers stay in the original in every document.

## Keeping the pages true

A page of `docs/cli/` must not disagree with the built tool. When you change a command, change its section in the same commit and compare it with `tg schema` and `tg <command> --help`. Keep `docs/skill/tg/` in step with `tg schema` too (`just skill-check` compares the skill with the schema).

## Generated tables

Command and global flag tables and the exit-code table in `docs/cli/` come from the Cobra command tree and `tg schema`. They are enclosed by `<!-- schema:flags ... -->`, `<!-- schema:global-flags -->` or `<!-- schema:exit-codes -->` markers. Run `just docs` to rebuild them. Do not edit the marked blocks by hand.

Saved Messages commands: [saved.md](cli/saved.md).
