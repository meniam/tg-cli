---
title: Send a message or report from a script
task: «отправь в чат», «пришли результат в телегу», «уведоми меня о X», «отправь отчёт в чат»
writes: true
access: [write]
commands: [message send, chat resolve]
live_checked: 2026-11-14
tags: [writing, sending, notifications]
---

# Send a message or report from a script

## When to use

The user directly asks to deliver text to Telegram (to a chat the user names). This is a write: other participants see it as an action of a live person. Send files with [send-media](send-media.md).

## Prepare

- The chat is found ([find-chat](find-chat.md)) and enabled by the owner in `access.write`.
- The user confirmed the recipient and the text in the current task. Permission for one send does not extend to the next.

## Commands

Preview first. This block sends nothing:

```bash
tg chat resolve @release_team
tg message send @release_team --dry-run --text "Deploy 42 is green" --idempotency-key <fresh-key>
```

After the user confirms the recipient and the text, send once, with a key:

```bash
tg message send @release_team --text "Deploy 42 is green" --idempotency-key <fresh-key>
```

Variant, long text from stdin. Preview it with `--dry-run` and the same recipient and options, get the confirmation, then send once:

```bash
printf '%s' "$report" | tg message send @release_team --idempotency-key <fresh-key>
```

Variant, formatted text. Preview it with `--dry-run`, get the confirmation, then send once:

```bash
tg message send @release_team --parse markdown --text '**Deploy** _is green_' --idempotency-key <fresh-key>
```

## Notes

- `--dry-run` checks access, length and mode and returns `dry_run: true`. Telegram does not change.
- Use one fresh `--idempotency-key` per intended delivery (`<fresh-key>` is a placeholder: replace it with a new value; a reused key replays an earlier result or fails with exit 2), show it in the preview, and reuse it only to retry that same delivery. `--idempotency-key` (1–64 characters `A-Za-z0-9._:-`) makes a retry safe: the same key with the same parameters returns the saved result with `replayed: true`.
- Text from stdin does not appear in `ps` or in shell history. The limit is 4096 code points.
- The response is `{chat_id, id, date}`.

## Pitfalls

- Exit code 9: the chat is not in `access.write`. Tell the user to ask the owner; do not ask for the passphrase.
- Exit code 6: `FLOOD_WAIT`, the tool does not retry writes, do not spin retries.
- Timeout or network error: the message may have gone out. Read `message history <chat> --limit 3`, then decide about a retry (or retry with the same key).
- Put a negative id after `--`.

## Verify

`tg message get <chat> <id>` with the id from the response returns the sent text.
