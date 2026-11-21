---
title: Queue the history load of a batch of chats
task: «синхронизируй чаты людей, с кем я общался», «подгрузи последние сообщения всех активных чатов», «поставь в очередь 50 чатов», «загрузи чаты за последние 60 дней»
writes: true
access: [none]
commands: [chat sync queue, chat sync status, chat sync cancel, chat list, status]
live_checked: no
tags: [history, loading, batch, queue]
---

# Queue the history load of a batch of chats

## When to use

The user wants many chats loaded: people they wrote to in the last two months, all unsynced chats, the last 100 messages of everything. One chat is [sync-recent-history](sync-recent-history.md) or [load-full-history](load-full-history.md).

## Prepare

The Daemon is running and the login exists. This is a bulk operation: run it only on request, and show the user the dry run first. Tell the user that the database grows and the Daemon makes many requests to Telegram, one chat at a time.

## Commands

Look first, nothing is queued:

```bash
tg chat sync queue --type user --active-days 60 --unsynced --messages 100 --dry-run
```

After the user agrees, run the same command without `--dry-run`:

```bash
tg chat sync queue --type user --active-days 60 --unsynced --messages 100
tg chat sync status --fields chat_id,state,pages,loaded,complete   # every job of this Daemon run
tg status                                                          # the HISTORY block lists the jobs with chat names
```

See which people the account lists and when each last wrote (`last_message_date`), with no load:

```bash
tg chat list --type user --limit 500 --fields chats.id,chats.title,chats.last_message_date
```

Other batches, same flags:

```bash
tg chat sync queue --months 1 --max-chats 200          # any chat type, a month of messages each
tg chat sync queue --type group --unsynced --max-chats 50   # whole history of unsynced groups
```

Stop procedure, only when the user wants it; what is saved stays:

```bash
tg chat sync cancel --all
```

## Notes

- The Daemon reads the dialog list from the newest dialog to the oldest and keeps the chats that match. `--active-days N` keeps chats whose last message is not older than N days and ends the walk at the first older dialog; a pinned dialog is out of date order and is skipped, not a reason to stop.
- `--unsynced` keeps only chats that were never loaded. Without it a partly loaded chat continues from its saved `oldest_id`. A chat that is loaded completely or has a job already is always skipped; the answer counts them in `skipped_loaded` and `skipped_active`.
- The depth flags (`--messages`, `--days`, `--months`, `--since`) work as in [sync-recent-history](sync-recent-history.md). Without one each chat loads to its first message, which is long.
- `--max-chats` is 50 by default and 500 at most. The answer lists the chats it picked (`chats`, with `title` as untrusted text, `last_message_date` and `action`: `queued` or `would_queue`).
- The Daemon loads one chat at a time and the rest wait (`queued`). A page is 100 messages with a 2 second pause. Jobs live in Daemon memory.
- Closed chats are never picked or shown.

## Pitfalls

- After `daemon stop` and `daemon start` the queue is gone. Run the same `queue` command again: loaded chats are skipped, partly loaded chats continue from `oldest_id`.
- `chat list` returns at most 500 chats per call. For a long list pass `data.next_cursor` to `--cursor` with the same `--type`, as in [find-chat](find-chat.md). A chat the session does not know gives `not_found` at `start`; read it through `tg chat list` first.
- A `FLOOD_WAIT` while reading the dialog list ends the call before anything is queued: wait and run it again. While a longer pause holds the account, other commands that use Telegram get exit code 6.
- Do not poll in a tight loop: tens of seconds between status calls.
- A limited load is `depth_reached`, not `complete`: do not call it the whole history.

## Verify

`tg chat sync status` lists the jobs, and `tg status` shows `History sync · N jobs` with the chat names. When all are `done`, the HISTORY coverage grows (`tg status`), and `tg chat overview` shows the saved span of each chat.
