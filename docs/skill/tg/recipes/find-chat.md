---
title: Find a chat by name and get its id
task: «найди чат», «как называется канал», «дай id чата», «найди чат с Иваном»
writes: false
access: [none]
commands: [chat list, chat resolve, chat info, search]
live_checked: no
tags: [reading, chats, addressing]
---

# Find a chat by name and get its id

## When to use

The user named a chat in words ("the releases chat", "the Go channel"), but commands need a `@username`, an id or a link. Do this before any other work with this chat, and especially before writing.

## Prepare

The Daemon is running and the login exists (`tg daemon status`).

## Commands

```bash
tg chat list --limit 100 --fields chats.id,chats.title,chats.type,chats.username
tg search "release" --scope chats                  # in the local database, no Telegram
tg chat resolve @release_team                    # id, type and title by @username or link
tg chat info -- -1001234567890                   # description, size, settings
```

## Notes

- `chat list` returns new chats first, `--limit` is 1 to 500. Chats closed by access lists are not in it.
- `tg search --scope chats` searches only among already saved chats. If a chat was never read, find it through `chat list` or `chat resolve`.
- Put a negative id after `--`.

## Pitfalls

- Several similar names: do not choose yourself. Show the user the found options (id, type, title) and wait for the choice.
- The chat is not found anywhere: it may be closed by access lists. Tell the user, do not work around it.
- Do not use a found id for writing until the chat is in `write`: the response will be exit code 9.

## Verify

`tg chat info <id>` returns the title the user named.
