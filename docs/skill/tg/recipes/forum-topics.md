---
title: Forum topics and reading one topic
task: «темы форума», «что в теме X», «прочитай топик», «список тем в группе»
writes: false
access: [none]
commands: [chat topics, message history, message unread, chat info]
live_checked: partial
tags: [forum, topics, reading]
---

# Forum topics and reading one topic

## When to use

The group works as a forum (it has topics), and you need to see the topics or read one of them. Read a regular chat without topics with `message history`.

## Prepare

The Daemon is running and the login exists. Check that the chat is a forum: the flag is visible in `tg chat info <chat>`. For a non-forum chat, `chat topics` gives exit code 2.

## Commands

```bash
tg chat info @forum_chat                           # forum flag
tg chat topics @forum_chat --limit 50
tg chat topics @forum_chat --cursor <next_cursor>        # next page of topics
tg message history @forum_chat --topic 7 --limit 50
tg message unread @forum_chat --topic 7
```

## Notes

- `data.topics`: `{id, title, closed, pinned, hidden, unread, top_message, created}`. `--limit` is 1 to 100, continue with `next_cursor`.
- A cursor belongs to one chat and is longer than a history cursor. Do not edit or truncate it.
- A topic `title` is `untrusted`.

## Pitfalls

- Each topic arrives once with any `--limit`: Telegram repeats pinned ones, the Daemon filters them out.
- A private chat and a bot give exit code 2 in `chat topics`.
- `--topic` on `message watch` keeps only the `message.new` and `message.edit` events of the topic.

## Verify

The number of topics matches what the user sees in Telegram. `message history --topic` returns messages of that topic only.
