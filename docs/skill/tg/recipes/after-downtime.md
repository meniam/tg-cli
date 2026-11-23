---
title: Check catchup after Daemon downtime
task: «что догнать после простоя», «догонка после простоя», «что пропущено пока демон не работал»
writes: true
access: [none]
commands: [chat sync catchup, chat sync status, chat sync start, chat overview]
live_checked: no
tags: [history, catchup, queue]
---

# Check catchup after Daemon downtime

## When to use

The user asks about missed messages after the Daemon was down. A qualifying catchup is queued automatically; do not start one just because the Daemon restarted.

## Prepare

The Daemon is running and the account is logged in. Catchup needs both `catchup.enabled` and `background_sync.enabled` enabled. Both default to `true`, require a restart when changed and may be changed only on the owner's request.

## Commands

Inspect the current jobs and saved gaps:

```bash
tg chat sync status
tg chat overview
```

If the user asks to repeat catchup, preview the selection first, then queue it:

```bash
tg chat sync catchup --dry-run
tg chat sync catchup
```

Only if the user asks to fill a particular gap, start its pass:

```bash
tg chat sync start <chat>
```

## Notes

- Catchup runs after downtime longer than 10 minutes and uses the heartbeat read at Daemon startup minus 10 minutes. Without a qualifying startup heartbeat, `since` is null and nothing is queued. The manual repeat uses the same boundary.
- It picks visible chats with stored messages and newer dialog activity, and loads to the time boundary or until it reaches 1000 loaded messages at a page boundary. A chat already in a job is `skipped_active`; a limit reached before the boundary leaves `gap_from` and `gap_to`.
- `source: catchup` jobs share a FIFO lane with new-chat loads, after manual jobs and before scheduled loads. A manual `start` does not cancel catchup.
- `chat sync start` passes a gap from `gap_to` even when `complete` is true. With `--messages N`, a chat without a gap counts N messages saved in total; during the gap pass and its continuation it counts N messages loaded by that task. A limit can narrow the gap and set `depth_reached: true`.
- See FEATURES.md item 10.21.

## Pitfalls

- A gap can coexist with `complete: true`: inspect `gap_from` and `gap_to` before claiming complete coverage.
- Do not page Telegram history in a loop. Check `chat sync status` with pauses of tens of seconds.
- Catchup does not restore every missed edit or deletion and does not detect a connection outage while the Daemon remained running.

## Verify

`chat sync status` shows catchup jobs with `source: catchup`. For a chat, the gap fields become null when its pass closes the gap; `chat overview` shows recorded gaps as `history_gap_from` and `history_gap_to`.
