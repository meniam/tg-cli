---
title: Check the history load queue and stop it
task: «что сейчас синхронизируется», «останови синхронизацию», «отмени всю очередь», «сколько осталось в очереди»
writes: true
access: [none]
commands: [chat sync status, chat sync cancel, status]
live_checked: no
tags: [history, loading, queue, cancel]
---

# Check the history load queue and stop it

## When to use

A load was started earlier and the user asks what is running, how far it is, or wants to stop one chat or everything.

## Prepare

The Daemon is running. Looking needs no login. Stopping changes only the queue; messages already saved stay.

## Commands

```bash
tg chat sync status --fields chat_id,state,source,pages,loaded,complete,depth_reached,wait_seconds,error_code,error
tg status                          # the HISTORY block shows the jobs with chat names, coverage and the next scheduled run
tg chat sync status -- <chat_id>   # one chat; a negative id goes after --
```

Stop one chat, or all, only when the user asks:

```bash
tg chat sync cancel @release_team   # one chat: a queued job is dropped, the running one is aborted
tg chat sync cancel --all           # every queued job and the running one; answers how many were cancelled
```

## Notes

- States: `queued` waits, `running` loads, `waiting_flood` waits out a Telegram pause by itself (`wait_seconds`), `done`, `failed` (`error_code`, `error`), `cancelled`. `source` is `manual` for a command and `background` for the schedule.
- `cancel --all` also cancels a scheduled job; the schedule goes on at its next run. It needs no login and no connection.
- The queue lives in Daemon memory: a Daemon restart empties it, and nothing resumes by itself.

## Pitfalls

- A cancel does not lift a `FLOOD_WAIT` pause that the account already has.
- `cancelled` and `failed` are not finished loads. To continue, run `start` or `queue` again: it goes on from the saved `oldest_id`.
- Chat names are not in the JSON of `chat sync status`; the human `tg status` shows them.

## Verify

`tg chat sync status` shows the cancelled jobs as `cancelled`, or an empty list when nothing is left; `cancel --all` answers `cancelled: N`.
