---
title: Queue the history load of several chats
task: «синхронизируй эти чаты», «загрузи историю этих чатов», «синхронизируй чаты людей, с кем я общался», «подгрузи последние сообщения всех активных чатов», «поставь в очередь 50 чатов», «загрузи чаты за последние 60 дней»
writes: true
access: [none]
commands: [chat sync queue, chat sync status, chat sync cancel, chat list, status]
live_checked: no
tags: [history, loading, batch, queue]
---

# Queue the history load of several chats

## When to use

The user wants two or more chats loaded: the chats they named, people they wrote to in the last two months, all unsynced chats, the last 100 messages of everything. `tg chat sync queue` is the only way to load several chats, in one call. Do not write a script, a shell loop or a Python snippet that runs `chat sync start` or `message history` per chat: the Daemon queue loads one chat at a time with pauses between pages, and a loop of yours has neither. Exactly one chat is [sync-recent-history](sync-recent-history.md) or [load-full-history](load-full-history.md).

## Prepare

The Daemon is running and the login exists. This is a bulk operation: run it only on request, and show the user the dry run first. Tell the user that the database grows and the Daemon makes many requests to Telegram, one chat at a time.

Choose how the chats are given. They exclude each other (exit code 2):

- **Named chats:** the chats the user listed, as arguments, up to 100. A negative id goes after `--`.
- **A filter:** `--type`, `--active-days`, `--unsynced`, `--max-chats`, for "all active people", "all unsynced groups".

Add a depth flag (`--messages`, `--days`, `--months`, `--since`) unless the user wants the whole history of each chat.

## Commands

Named chats. Look first, nothing is queued:

```bash
tg chat sync queue @anna @boris @release_team --months 1 --dry-run
```

After the user agrees, run the same command without `--dry-run`:

```bash
tg chat sync queue @anna @boris @release_team --months 1
tg chat sync status --fields chat_id,state,pages,loaded,complete   # every job of this Daemon run
tg status                                                          # the HISTORY block lists the jobs with chat names
```

A filter instead of names, the same way (dry run first):

```bash
tg chat sync queue --type user --active-days 60 --unsynced --messages 100 --dry-run
tg chat sync queue --type user --active-days 60 --unsynced --messages 100
```

See which people the account lists and when each last wrote (`last_message_date`), with no load:

```bash
tg chat list --type user --limit 500 --fields chats.id,chats.title,chats.last_message_date
```

Other batches by filter:

```bash
tg chat sync queue --months 1 --max-chats 200          # any chat type, a month of messages each
tg chat sync queue --type group --unsynced --max-chats 50   # whole history of unsynced groups
```

Stop procedure, only when the user wants it; what is saved stays:

```bash
tg chat sync cancel --all
```

## Notes

- Named chats are resolved and checked first. A closed or unknown chat fails the whole call (exit code 7) and nothing is queued; fix the list and run it again. A repeated chat counts once. The answer lists every named chat with `action`: `queued` (or `would_queue` with `--dry-run`), `skipped_loaded` (loaded enough already) or `skipped_active` (it has a job).
- With a filter the Daemon reads the dialog list from the newest dialog to the oldest and keeps the chats that match. `--active-days N` keeps chats whose last message is not older than N days and ends the walk at the first older dialog; a pinned dialog is out of date order and is skipped, not a reason to stop. `--unsynced` keeps only chats that were never loaded; without it a partly loaded chat continues from its saved `oldest_id`. `--max-chats` is 50 by default and 500 at most. A filter run lists only the chats it picked and counts the skipped ones in `skipped_loaded` and `skipped_active`.
- The depth flags work as in [sync-recent-history](sync-recent-history.md). Without one each chat loads to its first message, which is long.
- The Daemon loads one chat at a time and the rest wait (`queued`). A page is 100 messages with a 2 second pause. Jobs live in Daemon memory. A new chat gets its latest 100 messages queued by the Daemon itself ([sync-queue-control](sync-queue-control.md)); do not queue those yourself.
- Closed chats are never picked or shown with a filter.

## Pitfalls

- A loop of `tg chat sync start` or `message history` over many chats is not a substitute for this command, even a short one: it skips the queue and ends in `FLOOD_WAIT`.
- After `daemon stop` and `daemon start` the queue is gone. Run the same `queue` command again: loaded chats are skipped, partly loaded chats continue from `oldest_id`.
- `chat list` returns at most 500 chats per call. For a long list pass `data.next_cursor` to `--cursor` with the same `--type`, as in [find-chat](find-chat.md). Name a chat only after `find-chat` or `chat list` has shown that it exists.
- A `FLOOD_WAIT` while the Daemon resolves a chat or reads the dialog list ends the call before anything is queued: wait and run it again. While a longer pause holds the account, other commands that use Telegram get exit code 6.
- Do not poll in a tight loop: tens of seconds between status calls.
- A limited load is `depth_reached`, not `complete`: do not call it the whole history.

## Verify

`tg chat sync status` lists the jobs, and `tg status` shows `History sync · N jobs` with the chat names. When all are `done`, the HISTORY coverage grows (`tg status`), and `tg chat overview` shows the saved span of each chat.
