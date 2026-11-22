# Live event stream

## Live stream

`tg message watch` prints events as they arrive and works only through the Daemon. Safety class: "read".

Use it to wait for an answer or a new message instead of a polling loop: repeating `message history` every few seconds is a request each time and ends in `FLOOD_WAIT`. Writing to someone and waiting for the reply: [recipes/talk-to-contact.md](../recipes/talk-to-contact.md).

```bash
tg message watch                                        # all visible chats, jsonl
tg message watch --chat @name --topic 7                 # one chat, one forum topic
tg message watch --types message.new,reaction --max-events 20
tg message watch --chat @name --topic 7 --after <cursor>   # resume: repeat the same filters, change only --after
```

- **Types.** `message.new`, `message.edit`, `message.delete`, `reaction` are stored in the journal and replayed. `read`, `typing`, `status` are live: they are not stored and not replayed. `gap` means the Daemon could not recover a missed span (or could not durably record the event, `reason: "store"`): read the history with `message history`. `lagged` means you are reading slower than the stream.
- **Format.** The default is `jsonl`: a whole event on one line (`id`, `type`, `chat_id`, `time`, `data`), without an envelope. `--format json` only together with `--max-events` (envelope `{"ok":true,"data":{"events":[...]}}`), otherwise code 2; `--format toon` is also code 2. `--fields` applies to each event. stderr gets `watching from cursor <cursor>` (`--quiet` removes it).
- **After `lagged`.** The cursor advances when events are delivered to the CLI. With `--format json` the CLI buffers the events and returns an error before printing them, so resuming from the cursor in the error hint can skip events you never processed. Resume from the last cursor you actually processed; prefer `jsonl` and checkpoint each event after you handle it.
- **Cursor.** For a stored event, the `id` field holds the string `<id>.<pts>.<qts>.<date>`; only the leading `id` matters for resuming, but pass the whole string. Remember the whole `id` string of the last event that has one (`read`, `typing`, `status` and `gap` have none; do not remove `id` with `--fields`) and pass it unchanged as `--after`: the Daemon returns what was stored after it, then live events, with no gap and no repeat. Without `--after` the stream starts from the current moment and does not load history. The journal keeps 7 days or 100,000 events; an `--after` older than the limit gives code 8.
- **Exit codes.** Ctrl-C, SIGTERM or reaching `--max-events`: 0. `lagged`: 1, stderr has the hint `--after <cursor>`; continue from the last cursor you actually processed (the stderr hint `--after <cursor>` is the last cursor delivered to the CLI, which with `--format json` can be ahead of what you saw). Daemon stopped or connection closed: 3. No login: 4. Closed or unknown chat in `--chat`: 7. `cursor_expired`: 8.
- **For a script**, set `--max-events`: the command finishes by itself once that many events have arrived. It limits the count, not the time, and the check runs only when an event arrives, so a quiet chat can keep it waiting. Without it, it runs until Ctrl-C, SIGTERM, `lagged` or the Daemon stops.
- **Filters.** `--chat` finds the chat only in the local database: read it first (`chat list`, `message history`), otherwise `not_found`. `--topic` keeps only `message.new` and `message.edit` of the topic. A `gap` without a chat arrives under any `--chat`, `--types`, `--topic`.
- **Closed chats** produce no events, including `typing`, `read`, `status`. `--chat` on a closed chat gives the same `not_found`.
- The stream saves messages to the local database, so `tg search` sees them (a deletion from the stream marks the message `deleted_at`, including in a private chat and a basic group, where the event arrives without a chat; verified by tests, not on a live account). An active `message watch` prevents `--idle-timeout` from stopping the Daemon.
- The stream was verified by tests and on a live account (read only, 2026-11-14): in 5.5 minutes 62 events arrived (`status`, `message.new`, `message.edit`, `message.delete`), cursors are monotonic, no repeats; after a Daemon restart `--after <last cursor>` returned 70 events without repeats; with one chat allowed by access policy, only events of that chat arrived. What was not verified live: see `FEATURES.md`, items 8.2, 8.3, 8.4. If a `gap` arrived, do not treat the feed as complete.
