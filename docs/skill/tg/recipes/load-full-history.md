---
title: Load the whole chat history into the local database
task: «загрузи историю чата», «скачай всю переписку в базу», «нужна вся история чата»
writes: true
access: [none]
commands: [chat sync start, chat sync status, chat sync cancel, chat overview]
live_checked: 2026-11-14
tags: [history, loading, background]
---

# Load the whole chat history into the local database

## When to use

The task needs the whole chat history or a long period: searching old messages, an export, a digest over months. For recent messages, `message history` with a cursor is enough: deep loading is slow. To save only a recent slice into the database use [sync-recent-history](sync-recent-history.md) (`--messages`, `--days`, `--months`, `--since`), and for two or more chats [sync-active-chats](sync-active-chats.md): never repeat `chat sync start` in a loop or a script.

## Prepare

The Daemon is running and the login exists. Tell the user that the database will grow and so will the number of requests to Telegram. Run it only on request.

## Commands

```bash
tg chat overview --limit 50                      # what is already in the database, no Telegram
tg chat sync start @release_team                 # queue it, returns immediately
tg chat sync status --fields state,pages,loaded,oldest_id,complete,wait_seconds,error_code,error -- <chat_id>   # numeric id from the start response; a negative id goes after --
# repeat the status call with a pause of tens of seconds until it is finished (see Notes)
```

Stop procedure. Run it only when the user wants the load to stop; what is already saved stays:

```bash
tg chat sync cancel @release_team
```

## Notes

- A page is 100 messages, with a 2 second pause between pages: 10,000 messages is 100 pages and at least 200 seconds of pauses. Chats go one at a time, the rest wait in the queue (`queued`).
- Loading is finished when `state` equals `done` and `complete` equals `true`. After a Daemon restart, `idle` with `complete: true` also means the chat is loaded. Report `failed` (its `error_code` and `error` say why) and `cancelled` to the user; do not treat them as finished. `error_code` and `error` appear only for `failed`. `idle` with `complete: false` is not finished either: stop polling and report it.
- `waiting_flood`: Telegram asks to wait, loading continues by itself. The remainder is in `wait_seconds`.

## Pitfalls

- `chat sync start` is for exactly one chat. Two or more chats (named by the user or picked by a filter) go in one `tg chat sync queue` ([sync-active-chats](sync-active-chats.md)); a script that calls `start` per chat bypasses the queue and ends in `FLOOD_WAIT`.
- Poll `status` with a pause of tens of seconds, not in a tight loop. Poll by the numeric `chat_id` from the `start` response.
- While a `FLOOD_WAIT` pause is longer than the auto-wait threshold (10 seconds by default), other commands that use Telegram get exit code 6 at once (unless the Daemon was started with `TG_CLI_FLOOD_AUTOWAIT=0`: then they queue and a waiting command is not hung). Work locally.
- After `daemon stop`, tasks vanish from memory. Only a new `start` can continue: it takes `oldest_id` from the database.
- Do not present the result as the full history until you have `complete: true` and a `first_message_date`. `complete: true` with `first_message_date: null` (and saved messages) means a history limit stopped the load (`history_limit.since`, by default 2022-02-02, `history_limit.messages`, 200000, or a limit of this chat): say so, and do not claim the older history is there.

## Verify

`tg chat sync status <chat>` shows `complete: true` with `state: done` (or `idle` after a Daemon restart) and `first_message_date` set: `oldest_id` is then the start of the chat. With `first_message_date: null` the load stopped at a limit. After stopping an active job, `status` shows `state: cancelled`, which is not a finished load. A cancel that races with completion can return `done`, and a cancel for a chat with no retained job returns `idle`, while a retained finished job keeps its own state (`done`, `failed` or `cancelled`).
