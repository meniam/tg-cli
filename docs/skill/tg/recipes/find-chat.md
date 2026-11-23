---
title: Find a chat by name and get its id
task: «найди чат», «как называется канал», «дай id чата», «найди чат с Иваном», «найди Ивана в контактах», «найди переписку с Ивановым»
writes: false
access: [none]
commands: [search, contact search, chat list, chat resolve, chat info]
live_checked: partial
tags: [reading, chats, addressing]
---

# Find a chat by name and get its id

## When to use

The user named a chat or a person in words ("the releases chat", "the Go channel", "Anna from the team"), but commands need a `@username`, an id or a link. Do this before any other work with this chat, and especially before writing.

## Prepare

The Daemon is running. The local lookups need no login; `chat resolve` and the dialog list need it (`tg daemon status`).

## Commands

Ask the local database first: it is free and sends nothing to Telegram. Then widen only if it found nothing:

```bash
tg search "Anna" --scope chats,people --limit 10                   # chats and people the Daemon already knows
tg contact search --local Anna --limit 10 --fields contacts.id,contacts.first_name,contacts.last_name,contacts.username   # the contacts, without phone numbers
tg chat resolve @release_team                                      # id, type and title by @username or link
tg chat list --limit 100 --fields next_cursor,chats.id,chats.title,chats.type,chats.username   # the dialog list, a last resort
tg chat info -- -1001234567890                                     # description, size, settings
```

## Notes

- A hit in `scope: chats` is a chat you can use at once. A hit in `people` or `contacts` is a user; the private chat with a user has the same positive id.
- `tg search --scope chats` and `contact search --local` read only what is saved. A chat that was never read is found by `chat resolve @username` or in `chat list`.
- `chat list` returns new chats first, `--limit` is 1 to 500. Only visible chats count. Pass `next_cursor` to `--cursor` with the same `--type` until the cursor is absent. It is a request to Telegram for every page: do not page through the whole list to look for one name, and do not call it in a loop.
- Put a negative id after `--`.
- `contact search` returns phone numbers; keep them out of your answer and use `--fields` to drop them.

## Pitfalls

- Several similar names (two people called Anna, a person and a group "Eugene & Anna"): do not choose yourself. Show the user the found options (id, type, title) and wait for the choice.
- The chat is not found anywhere: it may be closed by access lists. Tell the user, do not work around it.
- Do not use a found id for writing until the chat is in `write`: the response will be exit code 9.

## Verify

`tg chat info <id>` returns the title the user named.
