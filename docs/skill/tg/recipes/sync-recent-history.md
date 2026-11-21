---
title: Load only the recent part of a chat history
task: «синхронизируй последний месяц», «подгрузи последние 100 сообщений», «загрузи переписку за неделю», «нужна история с октября»
writes: true
access: [none]
commands: [chat sync start, chat sync status, chat sync cancel, chat overview]
live_checked: no
tags: [history, loading, depth, background]
---

# Load only the recent part of a chat history

## When to use

The task needs a recent slice of one chat: the last 100 messages, the last week or month, or everything since a date. The whole history is not needed, and loading it would take much longer. For the whole history use [load-full-history](load-full-history.md); for many chats at once use [sync-active-chats](sync-active-chats.md).

## Prepare

The Daemon is running and the login exists. Tell the user that the database will grow by the loaded messages. Run it only on request.

## Commands

Pick one limit. They exclude each other (exit code 2):

```bash
tg chat sync start @release_team --messages 100   # at least 100 saved messages in the chat
tg chat sync start @release_team --days 7         # back to a week ago
tg chat sync start @release_team --months 1       # back to a calendar month ago
tg chat sync start @release_team --since 2026-10-01   # back to a date: RFC 3339 or YYYY-MM-DD
tg chat sync status --fields state,pages,loaded,oldest_id,complete,depth_reached,error_code,error -- <chat_id>
# repeat the status call with a pause of tens of seconds until state is done (see Notes)
tg chat overview --limit 50   # what the database holds per chat now, no Telegram
```

Stop procedure, only when the user wants it; what is already saved stays:

```bash
tg chat sync cancel @release_team
```

## Notes

- The load goes from the newest message back in pages of 100, with a 2 second pause between pages. It stops at a page boundary once the limit is met, so it can save up to 99 messages more than asked. `--messages N` counts the messages of the chat that are saved already, not only the new ones.
- A load that stopped on its limit ends `state: done` with `depth_reached: true` and `complete: false`. `complete` is `true` only when Telegram has no older message, so a limited load never says that the whole history is there.
- When the chat holds enough messages for the limit already, the job is `done` at once with `pages: 0` and no request to Telegram.
- The echo of the limit is in the job: `messages` for `--messages`, `since` (RFC 3339) for the other three.
- To go deeper later, run `tg chat sync start <chat>` again with a bigger limit or without one: it continues from the saved `oldest_id`, nothing is loaded twice.

## Pitfalls

- Poll `status` by the numeric `chat_id` from the `start` response, with a pause of tens of seconds, not in a tight loop.
- Do not call the result the full history: it is `depth_reached`, not `complete`.
- `--since` in the future, a value out of range, or two limits together is exit code 2 and no job.
- After `daemon stop` jobs vanish from memory; start again, the saved pages stay.

## Verify

`tg chat sync status -- <chat_id>` shows `state: done` and `depth_reached: true`. `oldest_id` and the date of the oldest saved message are at or beyond your limit (`tg chat overview` lists the first and the last saved message of each chat without Telegram).
