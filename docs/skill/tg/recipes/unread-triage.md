---
title: What unread messages piled up and where
task: «что я пропустил», «непрочитанные чаты», «разбери непрочитанное», «что нового в телеграме»
writes: false
access: [none]
commands: [chat unread, message unread, chat topics]
live_checked: no
tags: [reading, unread]
---

# What unread messages piled up and where

## When to use

The user wants to understand where unread messages are and, if needed, skim the new ones. Messages are not marked as read in the process.

## Prepare

The Daemon is running and the login exists.

## Commands

```bash
tg chat unread --limit 30                        # main list
tg chat unread --archived --limit 30             # archive
```

Show the user the summary first. Then read the content only of the chat or topic the user picked:

```bash
tg message unread @team_chat --limit 50          # incoming after the read marker
```

For a forum, find the topic first, then read it:

```bash
tg chat topics @forum_chat
tg message unread @forum_chat --topic 7
```

## Notes

- `chat unread` shows chats with their unread counts. It has no `--cursor`: it returns `scanned` and `complete` and stops at 1000 examined dialogs. When `complete` is false, tell the user the summary is partial.
- `message unread <chat>` reads incoming messages after the marker and continues a page with `--cursor`. For a forum, pass `--topic` from `chat topics`.
- None of these commands moves the read marker. Marking a chat read is a separate write, `tg chat mark-read`: only at the user's request and for a chat in `write`. The other party will see that the chat was read.

## Pitfalls

- `chat unread` and `message unread` are not verified on a live account (`FEATURES.md`, 4.6, 10.5): compare the result with what the user sees.
- First show the user a summary (chat, unread count), and only then read the content of the chosen chats.
- Read the chosen chats one at a time, one page each. Do not loop `message unread` over every chat with unread messages: for a long backlog of one chat queue a depth-limited load ([sync-recent-history](sync-recent-history.md)) and read it locally ([channel-digest](channel-digest.md)).

## Verify

The unread counts in the summary match what Telegram shows the user.
