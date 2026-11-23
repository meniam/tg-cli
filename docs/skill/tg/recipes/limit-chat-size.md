---
title: Cap how many messages the database keeps for a chat
task: «ограничь чат N сообщениями», «не храни больше N сообщений этого чата», «подчисти старые сообщения чата», «убери лимит чата»
writes: true
access: [none]
commands: [chat limit set, chat limit list, chat limit off, chat overview]
live_checked: no
tags: [history, limit, cleanup, database]
---

# Cap how many messages the database keeps for a chat

## When to use

The owner wants the local database to keep at most N messages of one chat, for example a noisy channel. A chat has no limit by default. This deletes saved messages for good, so run it only when the owner asked for the limit and named the chat. For loading history use [sync-recent-history](sync-recent-history.md) and [sync-active-chats](sync-active-chats.md); the global stop at 2022-02-02 or 200000 messages is the settings `history_limit.since` and `history_limit.messages`, not this command.

## Prepare

The Daemon is running. No login is needed: the command reads and writes only the local database. The chat is found ([find-chat](find-chat.md)) and open in the access lists. Look at what the chat holds now:

```bash
tg chat overview --limit 50
tg chat limit list
```

## Commands

Set the limit (100 to 100000000). When the chat holds more messages than N, the command refuses with exit code 2 and reports `saved` and `will_delete` in the error. Tell the owner these numbers and add `--yes` only after the owner agrees:

```bash
tg chat limit set @release_team 50000
tg chat limit set @release_team 50000 --yes   # only after the owner confirmed will_delete
tg chat limit list                            # limit and saved messages per chat
```

Remove the limit:

```bash
tg chat limit off @release_team
```

## Notes

- Once a chat holds more than N messages, the Daemon deletes the oldest ones and leaves 95% of N, so it does not clean after every new message. It runs about once an hour, at start and right after `set`.
- The oldest messages go first, with their versions, transcriptions and finished voice jobs. Notes stay. Telegram is never changed.
- The chat is marked `complete: true` with `first_message_date` null, so a history load does not fetch the deleted messages again. Removing the limit does not reopen the chat.
- `saved` counts message rows, messages marked deleted included. A negative chat id goes after `--`.

## Pitfalls

- Never add `--yes` to get past the refusal on your own: the deleted messages do not come back.
- A limit below 100 or above 100000000 is exit code 2.
- A closed or unknown chat is exit code 7.
- `chat limit off` does not restore deleted messages and does not make the chat load deeper.

## Verify

`tg chat limit list` shows the chat with `messages` equal to N and `saved` at most N soon after the cleanup (about 95% of N once it ran). `tg chat overview` shows the oldest saved message of the chat.
