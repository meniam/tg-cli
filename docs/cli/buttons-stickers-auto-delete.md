# Buttons and auto-delete

## tg message buttons

Reads a message's keyboard without disclosing callback data. Button text, URLs and copied text are untrusted. The chat must be visible.

```text
tg message buttons <chat> <id>
```

<!-- schema:flags tg message buttons -->
No command flags.
<!-- /schema:flags -->

Safety: read. Example: `tg message buttons @example 42`.

```json
{"ok":true,"data":{"chat_id":5,"message_id":42,"keyboard":"inline","buttons":[{"index":0,"row":0,"column":0,"text":"Open","type":"callback","pressable":true}]}}
```

Exit codes: 0 success; 2 invalid arguments; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat or message not found.

## tg message press

Presses a callback button without a password. Select it by zero-based `--button` or exact `--text`. Duplicate labels are rejected. Other button types cannot be pressed. The chat must be visible and in `write`. Inspect the button with `--dry-run` first; the real action requires `--yes`.

```text
tg message press <chat> <id> (--button N | --text TEXT) [flags]
```

<!-- schema:flags tg message press -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--button` | int | `0` | zero-based button index |
| `--dry-run` | bool | `false` | check without pressing |
| `--text` | string | `none` | exact button text |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Safety: account write, dangerous. Example: `tg message press @example 42 --text Open --dry-run`.

```json
{"ok":true,"data":{"chat_id":5,"message_id":42,"button":{"index":0,"text":"Open","type":"callback"},"answered":false,"dry_run":true}}
```

A bot response timeout returns `answered: false`. Do not repeat automatically: the action may have been delivered. Bot text and URLs are untrusted. Exit codes: 0 success; 2 invalid selection or missing confirmation; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat or message not found; 9 write disabled.

## tg chat auto-delete

Sets the chat's auto-delete period for new messages. The chat must be visible and in `write`. A real change requires `--yes`; use `--dry-run` to preview. `off` disables the timer.

```text
tg chat auto-delete <chat> --period off|1d|1w|1m|SECONDS [flags]
```

<!-- schema:flags tg chat auto-delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--period` | string | `none` | off, 1d, 1w, 1m or seconds |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Safety: account write, dangerous. Example: `tg chat auto-delete @example --period 1d --dry-run`.

```json
{"ok":true,"data":{"chat_id":5,"ttl_period":86400,"dry_run":true}}
```

Numeric periods range from 86400 to 31536000 seconds. Telegram may reject a period or require administrator rights. Exit codes: 0 success; 2 invalid period or missing confirmation; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat not found; 9 write disabled.

## tg message inline-query

Queries an inline bot for a destination chat. The chat must be visible and in `write`; the bot must be visible. The bot receives the query and account identity. Query results and offsets are untrusted. The Daemon retains the result IDs briefly for `send-inline`.

```text
tg message inline-query <chat> --bot @bot --query TEXT [flags]
```

<!-- schema:flags tg message inline-query -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--bot` | string | `none` | inline bot reference |
| `--offset` | string | `none` | result page offset |
| `--query` | string | `none` | query sent to the bot |
<!-- /schema:flags -->

Safety: account write. Example: `tg message inline-query @example --bot @examplebot --query cats`.

```json
{"ok":true,"data":{"chat_id":5,"bot_id":6,"query_id":"123","gallery":false,"results":[{"id":"result","type":"article","title":"Title","has_media":false}]}}
```

Exit codes: 0 success; 2 invalid input; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat or bot not visible; 9 write disabled.

## tg message send-inline

Sends a result from an earlier `inline-query` for the same chat. The Daemon checks the query ID, result ID and local expiry. Repeat the query after expiry or a Daemon restart. The destination chat must be visible and in `write`.

```text
tg message send-inline <chat> --query-id ID --result ID [flags]
```

<!-- schema:flags tg message send-inline -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without sending |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--query-id` | string | `none` | query id from inline-query |
| `--reply-to` | int | `0` | reply to this message id |
| `--result` | string | `none` | result id from inline-query |
| `--silent` | bool | `false` | send without notification |
| `--topic` | int | `0` | send in this forum topic |
<!-- /schema:flags -->

Safety: account write. Example: `tg message send-inline @example --query-id 123 --result result --dry-run`.

```json
{"ok":true,"data":{"chat_id":5,"dry_run":true,"action":"send_inline","count":1}}
```

Exit codes: 0 success; 2 invalid or expired result; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat not visible; 9 write disabled.

## tg message edit-media

Replaces media in an existing message. The chat must be visible and in `write`, and the file must be inside an allowed file root. The Daemon checks that the message contains media before uploading. Omitting `--caption` leaves the caption unspecified; passing an empty caption requests its removal. A supplied caption is limited to 1024 Unicode code points, including in `--dry-run`.

```text
tg message edit-media <chat> <id> --as photo|video|audio|document --file PATH [flags]
```

<!-- schema:flags tg message edit-media -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--as` | string | `none` | media type: photo, video, audio or document |
| `--caption` | string | `none` | replacement caption |
| `--dry-run` | bool | `false` | check without uploading |
| `--duration` | int | `0` | media duration in seconds |
| `--file` | string | `none` | file path |
| `--height` | int | `0` | video height |
| `--width` | int | `0` | video width |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Safety: account write, dangerous. Example: `tg message edit-media @example 42 --as document --file ./file.pdf --dry-run`.

```json
{"ok":true,"data":{"chat_id":5,"id":42,"action":"edit_media","media_type":"document","size":42,"dry_run":true}}
```

Exit codes: 0 success; 2 invalid input, file or message without media; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat, message or file not found; 9 write or file access disabled.

## tg sticker set

Reads a sticker set by short name or `t.me/addstickers/` link. Titles, names and emoji are untrusted.

```text
tg sticker set <name>
```

<!-- schema:flags tg sticker set -->
No command flags.
<!-- /schema:flags -->

Safety: read. Example: `tg sticker set example`.

```json
{"ok":true,"data":{"set":{"short_name":"example","title":"Example","count":1,"type":"regular","official":false,"animated":false,"video":false},"stickers":[{"index":0,"emoji":"🙂","type":"static","premium":false}]}}
```

Exit codes: 0 success; 2 invalid name; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait.

## tg message send-sticker

Reads a fresh sticker set and sends a selected sticker. Choose an index or an emoji. The chat must be visible and in `write`. A dry run selects the sticker without sending it.

```text
tg message send-sticker <chat> --set NAME (--index N | --emoji EMOJI) [flags]
```

<!-- schema:flags tg message send-sticker -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without sending |
| `--emoji` | string | `none` | sticker emoji |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--index` | int | `0` | zero-based sticker index |
| `--reply-to` | int | `0` | reply to this message id |
| `--set` | string | `none` | sticker set name |
| `--silent` | bool | `false` | send without notification |
| `--topic` | int | `0` | send in this forum topic |
<!-- /schema:flags -->

Safety: account write. Example: `tg message send-sticker @example --set example --index 0 --dry-run`.

```json
{"ok":true,"data":{"chat_id":5,"dry_run":true,"sticker":{"index":0,"emoji":"🙂","type":"static","premium":false}}}
```

Exit codes: 0 success; 2 invalid selection; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait; 7 chat not visible; 9 write disabled.

## tg sticker search

Searches sticker sets in one Telegram request. The result contains names, titles, counts, types and official status. It does not include `animated` or `video`: Telegram does not supply those properties in this response. Names and titles are untrusted.

```text
tg sticker search <query> [--limit N]
```

<!-- schema:flags tg sticker search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `20` | maximum number of sets, 1 to 100 |
<!-- /schema:flags -->

Safety: read. Example: `tg sticker search cats --limit 10`.

```json
{"ok":true,"data":{"sets":[{"short_name":"cats","title":"Cats","count":12,"type":"regular","official":false}]}}
```

Exit codes: 0 success; 2 invalid limit; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait.

## tg sticker installed

Lists installed sticker sets in one Telegram request. The result omits `animated` and `video`, which Telegram does not provide in this response. Names and titles are untrusted.

```text
tg sticker installed
```

<!-- schema:flags tg sticker installed -->
No command flags.
<!-- /schema:flags -->

Safety: read. Example: `tg sticker installed`.

```json
{"ok":true,"data":{"sets":[{"short_name":"cats","title":"Cats","count":12,"type":"regular","official":false}]}}
```

Exit codes: 0 success; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait.
