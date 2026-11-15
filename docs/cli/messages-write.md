# Writing messages

Commands that change the Telegram account: send, edit, delete and forward messages, send files, set reactions, pin and unpin, and create polls. (`chat mark-read` also writes and follows the same rules, see [chats.md](chats.md#tg-chat-mark-read).) The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data, and the chat id `-1001234567890` is made up.

An agent runs these commands only when the user asks for exactly this write in the current task, in a chat the owner listed in `write`, and starts with `--dry-run`. Do not use writing for trial runs.

## Writing is off until the owner allows it

Writing is off by default. The owner adds chat ids (Bot API form, nonzero integers without duplicates, like `allow` and `block`) to the list `write` in `<profile>/access.json`:

```json
{"write": [-1001234567890]}
```

- **Rule.** A chat accepts writes only when it is in `write`, is visible by the read rules and is not in `block`. No file or no `write` list means writing is off everywhere. `write` alone does not turn the read filter on. Only a human edits the file, and the agent never does. See [access.md](access.md).
- **No permission.** The command exits with code 9 `write_disabled` (JSON-RPC `-32008`) and a hint to add the chat to `write`. A chat closed for reading gives the same exit code 7 `not_found` as an unknown chat.
- **Confirmation.** `message edit` and `message delete` need `--yes`; without `--yes` and without `--dry-run` they exit with code 2 and never call the Daemon. All other write commands need no `--yes`.
- **Dry run.** `--dry-run` runs every applicable Daemon check, the write permission included, and returns `dry_run: true` with the fields of that command's result. It calls no Telegram write method, writes nothing to the database and creates no journal. Run it first, then run the real call.
- **Negative chat ids.** The id goes after `--` and the flags before it: `tg message send --text hi -- -1001234567890`.
- **No retries.** Without an idempotency key, a write is never repeated automatically, neither after `FLOOD_WAIT` (exit code 6) nor after a network error. The agent decides: after a network error read the history first, the message may have gone out. Reads keep their usual retry. An idempotency-keyed write has its documented one transient retry. The write rate is limited by the request throttle only (the send buckets), see [overview.md](overview.md#request-throttle-and-flood_wait).
- **Saved locally.** Ordinary sent and edited messages are saved to the local database (`out: true`, an edit keeps the previous version), deleted ones get `deleted_at`, a forward is not saved. A scheduled send is not saved until it is delivered.
- **Who can bypass it.** An agent running as the same user can edit `write` in `access.json`: the list protects against accidental writes and against writes induced through a chat, not against an agent that deliberately bypasses it.
- **Not done.** None of the commands on this page.

Verification and remaining work for account writes are tracked in [FEATURES.md](../../FEATURES.md), items 5.1, 5.2 and 9.1.

Common errors of this page: 2 for bad arguments (checked before the Daemon where possible), 3 no Daemon, 4 no login, 5 Telegram rejected the write, 6 `FLOOD_WAIT`, 7 an unknown or closed chat (or a message that is not found), 9 the chat is not in `write`.

## Scheduling messages

### Scheduling a text message

`message send` schedules a text message with exactly one of `--at` and `--in`. Other write commands do not support scheduled delivery.

```text
tg message send <chat> [flags]
```

**Time.** `--at` is an RFC 3339 time with an explicit offset, such as `2026-11-15T09:00:00+03:00`. `--in` is a Go duration, such as `90m` or `2h`. The flags exclude each other and the selected time must be at least 10 seconds ahead. Without an idempotency key, a bad time is exit code 2 before the Daemon call. With a key, the Daemon first checks for a completed replay; without one, the same bad time is exit code 2.

**How it works.** Telegram receives `messages.sendMessage` with `schedule_date`. The answer contains `chat_id`, a scheduled-message `id`, `date`, and `scheduled: true`. A scheduled id is not an ordinary message id, so it is not written to the local `messages` table. The ordinary delivered message follows the normal update path.

`--dry-run` checks input and access but makes no Telegram write or database change. The chat must be visible and in `write`; a non-numeric reference may be resolved through Telegram during that check. The request is not automatically retried after `FLOOD_WAIT`.

```sh
tg message send @release_team --at 2026-11-15T09:00:00+03:00 --text 'Deploy begins'
tg message send @release_team --in 90m --text 'Deploy begins'
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":17,"date":"2026-11-15T06:00:00Z","scheduled":true}}
```

**Exit codes.** 0; 2 invalid schedule time or send input; 3 no Daemon; 4 no login; 5 Telegram rejected the schedule; 6 `FLOOD_WAIT` (not repeated); 7 unknown or closed chat; 9 chat is not in `write`.

### tg message scheduled list

Lists scheduled messages for a visible chat, ordered by scheduled date.

```text
tg message scheduled list <chat> [flags]
```

<!-- schema:flags tg message scheduled list -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. **Output formats:** `json`, `jsonl`, `toon`, `human`.

The command reads Telegram each time and creates no local scheduled-message storage. Returned text has the same untrusted-data treatment as other message text.

```sh
tg message scheduled list @release_team
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"messages":[{"id":17,"chat_id":-1001234567890,"date":"2026-11-15T06:00:00Z","text":"Deploy begins"}]}}
```

**Exit codes.** 0; 3 no Daemon; 4 no login; 5 Telegram rejected the read; 6 `FLOOD_WAIT`; 7 unknown or closed chat.

### tg message scheduled delete

Cancels scheduled messages that have not been delivered.

```text
tg message scheduled delete <chat> <id>... [flags]
```

<!-- schema:flags tg message scheduled delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

The chat must be visible and in `write`; every id is positive. `--dry-run` performs these checks without a Telegram write. No deletion record is added locally because the messages were not delivered.

At most 1000 IDs are accepted. Requests are sent in groups of at most 100; a partial result follows the batch envelope described in the [overview](overview.md#the-answer-envelope).

```sh
tg message scheduled delete @release_team 17 18 --dry-run
tg message scheduled delete @release_team 17 18
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"messages":null,"deleted":[17,18]}}
```

**Exit codes.** 0; 2 invalid id; 3 no Daemon; 4 no login; 5 Telegram rejected the deletion; 6 `FLOOD_WAIT` (not repeated); 7 unknown or closed chat; 9 chat is not in `write`.

### tg message scheduled send-now

Sends scheduled messages now.

```text
tg message scheduled send-now <chat> <id>... [flags]
```

<!-- schema:flags tg message scheduled send-now -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

The chat must be visible and in `write`; every id is positive. `--dry-run` makes no Telegram write and returns `messages: null`. A real call returns a `messages` array. If Telegram returns complete delivered message objects, the Daemon marks them `out: true` and saves them through the normal message store. If it returns only delivered ids, the items contain `chat_id`, `id`, `date: ""`, `text: ""`, and `out: true`; they are not saved locally.

At most 1000 IDs are accepted. Requests are sent in groups of at most 100; a partial result follows the batch envelope described in the [overview](overview.md#the-answer-envelope).

```sh
tg message scheduled send-now @release_team 17
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"messages":[{"id":41,"chat_id":-1001234567890,"date":"2026-11-15T06:00:03Z","text":"Deploy begins","out":true}]}}
```

**Exit codes.** 0; 2 invalid id; 3 no Daemon; 4 no login; 5 Telegram rejected the request; 6 `FLOOD_WAIT` (not repeated); 7 unknown or closed chat; 9 chat is not in `write`.

Live-account verification is tracked only in [FEATURES.md](../../FEATURES.md), item 5.6.

## Idempotent writes

`--idempotency-key <key>` is available on `message send`, `message send-media`, `message poll` and `message forward`. The key contains 1 to 64 characters from `A-Z`, `a-z`, `0-9`, `.`, `_`, `:` and `-`; another value is exit code 2. Use it when a caller may repeat one of these writes after a timeout or a transient network error. Do not invent a key for deliberately separate, identical messages.

For `message send --at` or `--in`, the key covers the scheduled send as well. `--at` must be repeated with the same value. For `--in`, the first reservation fixes the delivery time; a later retry that is already fewer than 10 seconds from that time fails before another Telegram request. A completed repeat returns the saved scheduled result and never turns the scheduled id into a locally saved ordinary message.

The key belongs to the destination chat (`<chat>` for send, media and poll, `<to>` for forward). A repeated call with the same method and parameters returns the saved result with `replayed: true`; the same key with different parameters is exit code 2. `--dry-run` does not reserve a key or write to Telegram; its `replay_available: true` means a completed saved result exists. For media, the comparison includes the content of each selected file.

Without a key the tool keeps its ordinary random Telegram identifier and has no application-level retry. With a key it derives the identifiers from the account, destination chat, key and item position, then retries one transient network failure or timeout. Telegram transport retries are outside this rule. If a response is lost before the Daemon saves it, Telegram can return `RANDOM_ID_DUPLICATE` instead of the original result; the tool cannot recover that result automatically.

## Formatting text

`message send` and `message edit` format the text with `--parse` or `--entities`. They exclude each other (both together is exit code 2: `--parse and --entities cannot be used together`). Without either one the text goes as plain text. A formatted write follows every rule above: the chat must be in `write`, `edit` needs `--yes`, and a send retries only under the idempotency-key rule.

| Option | The text is | What Telegram gets |
| --- | --- | --- |
| `--parse html` | HTML with Telegram tags | an ordinary message with entities |
| `--parse markdown` | Markdown | an ordinary message with entities |
| `--parse rich-html` | HTML | a rich message, parsed by Telegram |
| `--parse rich-markdown` | Rich Markdown | a rich message, parsed by Telegram |
| `--entities FILE` (or `-`) | plain text, entities in a JSON array | an ordinary message with entities |

Use `html`, `markdown` or `--entities` for bold, italic, links, quotes, code and spoilers in an ordinary message. Use `rich-html` or `rich-markdown` when the message needs headings, lists, tables or formulas. A rich message is a newer kind of Telegram message, and how clients show it, old ones included, has not been checked: appearance in clients is not confirmed by the owner.

```sh
# html
tg message send --parse html --text '<b>Deploy</b> <i>green</i>, <a href="https://example.com/run/42">run 42</a>' -- -1001234567890

# markdown
tg message send --parse markdown --text '**Deploy** _green_, [run 42](https://example.com/run/42)' -- -1001234567890

# rich-markdown, text from stdin
tg message send --parse rich-markdown -- -1001234567890 <<'EOF'
# Release 42

**Green.** Details: ||secret||

- build ok
- tests ok

| Step  | Result |
|:------|-------:|
| build | ok     |
EOF

# rich-html
tg message send --parse rich-html --text '<h1>Release 42</h1><p><b>Green.</b></p><ul><li>build ok</li><li>tests ok</li></ul>' -- -1001234567890

# entities: bold "Hello" (offset 0, length 5) and the link "link" (offset 6, length 4)
echo '[{"type":"bold","offset":0,"length":5},{"type":"text_url","offset":6,"length":4,"url":"https://example.com"}]' > entities.json
tg message send --text 'Hello link' --entities entities.json -- -1001234567890

# edit with formatting
tg message edit --yes --parse markdown --text '**Fixed** text' -- -1001234567890 4103
```

The syntax of the rich modes is Telegram's own and is parsed by the server; the Go library keeps a copy of its description in `telegram/message/rich/spec.md` of `gotd/td`. The rich examples above follow that description; they were not all sent verbatim.

- **HTML.** Tags: `b` or `strong`, `i` or `em`, `u` or `ins`, `s`, `strike` or `del`, `a href`, `code`, `pre` (the language comes from `<code class="language-go">`), `tg-spoiler` and `blockquote` (`<blockquote expandable>` makes it collapsible). The parser of the library also reads `<span class="tg-spoiler">`, `tg-emoji` and `tg-time`; the tests of this tool do not cover those three. Other tags, such as headings, lists, `p` and `div`, are dropped without an error and their text stays. An unclosed or unpaired `span`, `tg-emoji`, `tg-time` or listed tag and a link without a valid `href` are exit code 2 with the byte position.
- **Markdown.** `**bold**` or `__bold__`, `*italic*` or `_italic_`, `~~strikethrough~~`, `||spoiler||`, `` `code` ``, fenced code with a language, `> quote` and `[text](url)`. There is no underline, and headings, lists and `<u>` stay as plain characters: use `html` or a rich mode for those.
- **Entities.** One JSON array, at most 256 KiB. Each item has `type`, `offset` and `length`, and these fields where the type needs them:

  | `type` | Extra fields |
  | --- | --- |
  | `bold`, `italic`, `underline`, `strikethrough`, `spoiler`, `code` | none |
  | `pre` | `language` (optional) |
  | `blockquote` | `collapsed` (optional, `true` makes it collapsible) |
  | `text_url` | `url` (required) |
  | `url`, `email`, `phone`, `mention`, `hashtag`, `cashtag`, `bot_command` | none: the entity marks text that already looks like one |
  | `mention_name` | `user_id` |
  | `custom_emoji` | `custom_emoji_id` |
  | `date_time` | `date_unix` and `format` (`r` alone, or letters from `t`, `T`, `d`, `D`, `w`, `W`) |

  With `--entities -` the standard input holds the JSON, so the text must come from `--text`. With a file path the text comes from `--text` or stdin as usual.
- **UTF-16 offsets.** `offset` and `length` count UTF-16 units, as Telegram does: a character outside the Basic Multilingual Plane, an emoji for example, takes 2. In `😀 bold` the word `bold` starts at offset 3. A boundary inside such a pair, a zero length and a range outside the text are exit code 2.
- **Mention by id.** `<a href="tg://user?id=N">name</a>`, `[name](tg://user?id=N)` and the `mention_name` entity need a user that the session knows: the Daemon resolves the id through the peer cache of the session, as it does for a numeric chat reference, and on a miss loads the dialog list once. The local database stores no access hashes and takes no part. Any other id is exit code 2 `unknown user id for this session`. A `--dry-run` with such a mention may read the user from Telegram; it still writes nothing.
- **Length.** A plain text, an `--entities` text and the source of a rich mode are at most 4096 characters. For `html` and `markdown` the limit applies to the text left after parsing, and a text read from stdin is at most 64 KiB. The limit of a rich message has not been measured, so 4096 characters of its source is a guard of this tool, not a known limit.
- **Errors.** Bad flags and input, HTML, Markdown and entity errors and an unknown mention id are exit code 2, raised before any Telegram write method is called (a mention lookup may read from Telegram). A refusal of a rich message by Telegram is exit code 5 `Telegram rejected rich message`. `FLOOD_WAIT` is exit code 6 and the write is not repeated.
- **Dry run.** `--dry-run` parses the text without writing and adds the mode, the length of the text without markup, the number of entities and whether the message is rich:

  ```sh
  tg message send --dry-run --parse markdown --text '**Deploy** green' -- -1001234567890
  ```

  ```json
  {"ok":true,"data":{"chat_id":-1001234567890,"dry_run":true,"action":"send","count":1,"text_length":16,"mode":"markdown","plain_length":12,"entities":1,"rich":false}}
  ```

  `mode` is `html`, `markdown`, `rich-html`, `rich-markdown` or `entities`. A rich mode reports `plain_length` and `entities` as 0, because Telegram parses the text.
- **What is saved locally.** After `message send` or `message edit`, the database gets the source text supplied to `--text` or stdin, including HTML, Markdown and rich markup. A rich message is saved even if Telegram's response carries no message. A later read from Telegram replaces `text` with Telegram's plain text; when that text differs, the source text becomes a prior version in `message_versions`.
- **Reading a rich message.** `message get`, history, pinned messages, context, search and the live stream return `text` built from rich blocks; see [messages-read.md](messages-read.md#rules-shared-by-message-reads) and [FEATURES.md](../../FEATURES.md), item 4.10.
- **Editing.** `message edit` can change the kind of a message: plain to rich, rich to plain, and between the formatting modes.

Verification and remaining work for text formatting are tracked in [FEATURES.md](../../FEATURES.md), item 5.4.

## Text messages

### tg message send

Sends a text message, plain or formatted.

```text
tg message send <chat> [flags]
```

<!-- schema:flags tg message send -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--at` | string | `none` | schedule delivery at an RFC 3339 time |
| `--dry-run` | bool | `false` | describe the action without sending |
| `--entities` | string | `none` | JSON entity array file, or - for stdin |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--in` | string | `none` | schedule delivery after a Go duration |
| `--parse` | string | `none` | format: html, markdown, rich-html or rich-markdown |
| `--reply-to` | int | `0` | reply to this message id |
| `--silent` | bool | `false` | send without a notification |
| `--text` | string | `none` | message text; reads stdin when absent (stdin is ignored when --text is given) |
| `--topic` | int | `0` | send in this forum topic |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, and the chat in `write` (see above). No `--yes`.

**Text.** `--text` gives the text. Without `--text` the text is read from stdin; with `--text` stdin is never read (an open pipe would block the command). A terminal or an empty stdin without `--text` (`message text is required: use --text or pipe it to stdin`), an empty text (`message text must not be empty`) and a text longer than 4096 characters (`message text must be at most 4096 characters`) are exit code 2, before the Daemon is called. Without `--parse` and `--entities` the text goes as it is. `--reply-to` replies to a message, `--topic` sends into a forum topic (a reply inside a forum topic goes as a direct request with `top_msg_id`), `--silent` sends without a notification.

**Example.**

```sh
tg message send --dry-run @release_team --text "Deploy is green"
tg message send --reply-to 4102 --text "Thanks" -- -1001234567890
echo "Fixed text" | tg message send @release_team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4104,"date":"2026-11-14T09:30:00Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `date` string (optional), `dry_run` boolean (optional), `action` string (optional), `count` integer (optional), `text_length` integer (optional), `mode` string (optional), `plain_length` integer (optional), `entities` integer (optional), `rich` boolean (optional).

With `--idempotency-key`, `replayed` is an optional boolean. With `--dry-run`, `replay_available` is an optional boolean.

With `--dry-run`: `{"chat_id":-1001234567890,"dry_run":true,"action":"send","count":1,"text_length":15}` (plus `mode`, `plain_length`, `entities` and `rich` when the text is formatted).

**Exit codes.** 0; 2 empty or too long text, no text on a terminal, `--parse` with `--entities`, bad formatting; 3, 4; 5; 6 not repeated; 7 an unknown or closed chat; 9 the chat is not in `write`.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.1.

### tg message edit

Edits the text of a message.

```text
tg message edit <chat> <id> [flags]
```

<!-- schema:flags tg message edit -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | describe the edit without changing Telegram |
| `--entities` | string | `none` | JSON entity array file, or - for stdin |
| `--parse` | string | `none` | format: html, markdown, rich-html or rich-markdown |
| `--text` | string | `none` | replacement text; reads stdin when absent |
| `--yes` | bool | `false` | confirm the edit |
<!-- /schema:flags -->

**Safety class:** `account_write`. Dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, and the chat in `write`. The command is dangerous: without `--yes` and without `--dry-run` it is exit code 2 (`add --yes to edit or --dry-run to preview`) and the Daemon is not called. The text is taken like in `send` (from `--text` or stdin, 4096 characters at most), formatted by `--parse` or `--entities`. The edit keeps the previous version of the text in the local database. The message can change kind, plain to rich and back.

**Example.**

```sh
tg message edit --dry-run @release_team 4103 --text "Fixed text"
echo "Fixed text" | tg message edit @release_team 4103 --yes
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4103,"date":"2026-11-14T09:31:00Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `date` string (optional), `dry_run` boolean (optional), `action` string (optional), `count` integer (optional), `text_length` integer (optional), `mode` string (optional), `plain_length` integer (optional), `entities` integer (optional), `rich` boolean (optional).

**Exit codes.** 0; 2 no `--yes` and no `--dry-run`, bad text or formatting; 3, 4; 5; 6; 7; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.2.

### tg message delete

Deletes messages.

```text
tg message delete <chat> <id>... [flags]
```

<!-- schema:flags tg message delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | describe the deletion without changing Telegram |
| `--for-me` | bool | `false` | delete only for this account |
| `--revoke` | bool | `false` | delete for everyone |
| `--yes` | bool | `false` | confirm the deletion |
<!-- /schema:flags -->

**Safety class:** `account_write`. Dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, and the chat in `write`. The command is dangerous: without `--yes` and without `--dry-run` it is exit code 2 (`add --yes to delete or --dry-run to preview`) and the Daemon is not called. Choose exactly one of `--revoke` (for everyone) and `--for-me` (only for this account); anything else is exit code 2 (`choose exactly one of --revoke or --for-me`). In a channel `--for-me` is rejected, because a channel deletion is always for everyone.

**Delete guard.** For private chats and basic groups Telegram deletes by message id without naming the chat, and ids are unique per account. So before deleting, the Daemon checks in the local database that every id belongs to the chosen chat. An id that is unknown locally or belongs to another chat is exit code 7 `not_found`: read the message first (`message get` or `message history`). Channels need no check.

**Deletion journal.** Up to 1000 IDs are accepted and sent in groups of at most 100. `<profile>/deletions.jsonl` (mode `0600`) gets an `intent` and `result` line for each sent group: `time`, `account`, `tool` (`tg`), `phase`, `chat_id`, `ids`, `revoke` and `outcome`. It holds no message text. If an `intent` cannot be written, that group and the remaining groups are not sent. The file is not rotated. IDs in successful groups get `deleted_at` in the local database; the list represents accepted requests, not proof that every ID existed.

**Example.**

```sh
tg message delete --dry-run --revoke @release_team 4103
tg message delete @release_team 4103 4104 --revoke --yes
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"deleted":[4103,4104]}}
```

**Answer fields** (`data`): `chat_id` integer, `deleted` array, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional).

**Exit codes.** 0; 2 not exactly one of `--revoke` and `--for-me`, no `--yes` and no `--dry-run`, `--for-me` in a channel; 3, 4; 5; 6; 7 an unknown or closed chat, or an id the guard does not know; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.2 and 9.3.

### tg message forward

Forwards messages from one chat to another. `--copy` omits the original author; `--no-captions` also omits media captions and requires `--copy`. Telegram rejects forwarding protected content.

```text
tg message forward <from> <to> <id>... [flags]
```

<!-- schema:flags tg message forward -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--copy` | bool | `false` | forward without the original author |
| `--dry-run` | bool | `false` | describe the action without forwarding |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--no-captions` | bool | `false` | omit media captions (requires --copy) |
| `--silent` | bool | `false` | forward without a notification |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, the source chat readable by the access lists and the target chat in `write`. No `--yes`. A forward is not saved to the local database.

At most 1000 IDs are sent in groups of at most 100. Each returned message has `source_id`, the original message ID; `date` is empty if Telegram returned no date. A missing correlated ID appears in `errors`; an uncorrelated response is an error. `FLOOD_WAIT` or loss of authorization stops later groups. Other failed groups do not stop them. A partial result saved under an idempotency key is replayed with `replayed: true` and exit 1; retry failed IDs in a new call with a new key.

A partial answer exits with code 1:

```json
{"ok":true,"data":{"partial":true,"errors":[{"id":4103,"code":"not_found","message":"message not found or not returned by Telegram"}],"chat_id":-1002345678901,"messages":[{"source_id":4102,"id":77,"date":"2026-11-14T09:40:00Z"}]}}
```

**Example.**

```sh
tg message forward --dry-run @release_team @archive_team 4102 4103
tg message forward --silent @release_team @archive_team 4102
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1002345678901,"messages":[{"source_id":4102,"id":77,"date":"2026-11-14T09:40:00Z"}]}}
```

**Answer fields** (`data`): `chat_id` integer, `messages` array, `messages.source_id` integer, `messages.id` integer, `messages.date` string, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional).

With `--idempotency-key`, `replayed` is an optional boolean. With `--dry-run`, `replay_available` is an optional boolean.

**Exit codes.** 0; 2 fewer than three arguments; 3, 4; 5; 6; 7 an unknown or closed source or target; 9 the target is not in `write`.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.2.

## Files

### tg message send-media

Sends one file or an album of 2 to 10 files as photos, videos, audio, voice messages or documents.

```text
tg message send-media <chat> [flags]
```

<!-- schema:flags tg message send-media -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--as` | string | `none` | required media type: photo, video, audio, voice or document |
| `--caption` | string | `none` | caption for the first file (up to 1024 characters) |
| `--dry-run` | bool | `false` | check files and describe the action without uploading |
| `--duration` | int | `0` | media duration in seconds; overrides detected duration |
| `--file` | stringArray | `[]` | file path; repeat for an album (1 to 10 files) |
| `--height` | int | `0` | video height; overrides detected height |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--reply-to` | int | `0` | reply to this message id |
| `--silent` | bool | `false` | send without a notification |
| `--topic` | int | `0` | send in this forum topic |
| `--width` | int | `0` | video width; overrides detected width |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, and two conditions that only the owner can set in `access.json`: the chat is in `write`, and the directory of every file is in `files`.

```json
{"write": [-1001234567890], "files": ["/Users/me/tg-outbox"]}
```

**The two conditions.** The chat rules are those of [Writing is off until the owner allows it](#writing-is-off-until-the-owner-allows-it): a closed or unknown chat is exit code 7, a chat outside `write` is exit code 9. `files` is an array of absolute directory paths without duplicates; a relative or empty path, a duplicate or another type makes the file invalid and closes everything, like the other keys. No `files` key or an empty list means sending files is off: exit code 9 `write_disabled` and a hint to ask the owner to add the directory. The Daemon rereads the file like the other lists. No command writes `files`: an agent cannot widen the roots and has to ask the owner. An agent running as the same user can edit `files`: the roots protect against accidental reading of a file and against reading induced through a chat, not against an agent that deliberately bypasses them.

**Paths.** The CLI turns a relative path into an absolute one from its working directory, because the Daemon opens the file. A path outside every root, a `..` that leaves a root, a symlink that leads out and a root that does not exist give the same answer, exit code 2 `file is outside the allowed roots`, so the answers do not show what lies outside. A missing file inside a root is exit code 7 `file not found`. The file must be a regular file (a directory is exit code 2) of 1 byte to 100 MiB. The Daemon opens it once and uploads through that same descriptor; a file replaced while it is being opened is exit code 2.

**The type is explicit.** `--as` is required (exit code 2 without it) and the type is never guessed from the extension. The Daemon reads the first bytes and checks them against the chosen type:

| `--as` | Accepted content | Files |
| --- | --- | --- |
| `photo` | JPEG, PNG | 1 to 10 |
| `video` | MP4 | 1 to 10 |
| `audio` | MP3, AAC (ADTS), MP4 audio with the brand M4A, M4B or M4P | 1 to 10 |
| `voice` | Ogg with Opus | exactly 1 |
| `document` | any regular file | 1 to 10 |

A mismatch is exit code 2 with the hint `--as document`. WebP, GIF, MOV, WAV, FLAC, Ogg Vorbis and everything else goes as `document`.

With `--idempotency-key`, the result can contain `replayed: true`; with `--dry-run`, it can contain `replay_available: true`.

**Limits.** 100 MiB per file and 100 MiB for all files of one call, over that is exit code 2 before any upload. One file goes as one message; 2 to 10 files go as one album of one type, and the caption goes with the first file. `voice` takes one file, two or more is exit code 2. The caption is at most 1024 characters (an empty one is fine); this is the limit of the project, the limit of your account was not read.

**Metadata flags.** For `video` the Daemon reads the duration and the size from the MP4 (`mvhd`, `tkhd`), for `voice` the duration from the last Ogg page. If that gives nothing, the command is exit code 2 and asks for `--duration --width --height` (video) or `--duration` (voice). The flags override what was read and apply to every file of an album. `audio` has no automatic duration: `--duration` sets it. `photo` and `document` ignore the flags.

**Deadline and retries.** The call waits for the upload and the send, up to 600 seconds in the Daemon and in the CLI; other methods keep 120 seconds. At the deadline the command ends with exit code 1 and the outcome is unknown. The upload runs in one stream and the parts wait in the lower queue of the throttle, so ordinary commands go first. A send is never repeated. `FLOOD_WAIT`, during the upload or on the send, is exit code 6 with the seconds in the message, and the whole account pauses for that time; when it comes during the upload, nothing has been sent yet. A lost answer while an album is being sent leaves the outcome unknown: look at `message history` before you send again.

**Dry run.** `--dry-run` runs every check, including the file content, and uploads nothing. The answer holds `dry_run: true`, `action: "send_media"`, `count`, `types`, `sizes`, `caption_length` and an empty `messages`. Use `--format json`, `toon` or `human` to see it: `jsonl` prints one line per sent message.

**Result.** Always `{chat_id, messages: [{id, date}]}`, also for one file. Sent messages are saved to the local database (`out: true`, `media_type` equal to the `--as` value, the caption as the text of the first message); the file itself is not stored. The Daemon journal gets the method and the result code, no paths and no captions.

**Example.**

```sh
tg message send-media --dry-run --as photo --file /Users/me/tg-outbox/chart.png -- -1001234567890
tg message send-media --as photo --caption "Weekly numbers" --file /Users/me/tg-outbox/a.png --file /Users/me/tg-outbox/b.png @release_team
tg message send-media --as video --duration 12 --width 1280 --height 720 --file /Users/me/tg-outbox/clip.mp4 @release_team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"messages":[{"id":4110,"date":"2026-11-14T09:50:00Z"},{"id":4111,"date":"2026-11-14T09:50:00Z"}]}}
```

**Answer fields** (`data`): `chat_id` integer, `messages` array, `messages.id` integer, `messages.date` string, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional), `types` array (optional), `sizes` array (optional), `caption_length` integer (optional).

**Exit codes.** 0; 2 wrong arguments, a path outside the roots, a file that does not match the type, a size over the limit, a missing `--as`; 5 Telegram rejected the upload or the send; 6 `FLOOD_WAIT`; 7 the chat or the file is not found; 9 the chat is not in `write` or `files` is empty; 1 the deadline or another failure; 3 and 4 as everywhere.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 6.1.

## Reactions, pins and polls

Four commands write to the account under the rules of [Writing is off until the owner allows it](#writing-is-off-until-the-owner-allows-it): the chat must be in `write` (exit code 9 otherwise), none needs `--yes` or is dangerous, `--dry-run` runs every check and calls no Telegram write method, and a write is never repeated (`FLOOD_WAIT` is exit code 6). The formats are `json`, `jsonl` (the one envelope as one line), `toon` and `human`. A negative chat id goes after `--`, the flags before it.

Verification and remaining work for reactions, pins and polls are tracked in [FEATURES.md](../../FEATURES.md), item 5.3.

### tg message react

Sets one emoji reaction on a message, or removes yours with `--remove`.

```text
tg message react <chat> <id> <emoji> [flags]
```

<!-- schema:flags tg message react -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check the action without changing Telegram |
| `--remove` | bool | `false` | remove your reaction from the message |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, the chat in `write`. Only a plain emoji works: custom and paid reactions are not supported. The tool checks the form only (not empty, no whitespace, at most 32 bytes; otherwise exit code 2: `emoji must be nonempty, contain no whitespace, and be at most 32 bytes`) and Telegram decides whether the emoji is allowed for the message: `REACTION_INVALID` is exit code 2 with the text `reaction is not available for this message`. `--remove` sends an empty list of reactions, so no emoji goes with the request, but the argument is still required. Nothing is saved to the database.

**Example.**

```sh
tg message react --dry-run @release_team 4102 👍
tg message react @release_team 4102 👍
tg message react @release_team 4102 👍 --remove
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4102,"emoji":"👍","removed":false}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `emoji` string (optional), `removed` boolean, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional).

**Exit codes.** 0; 2 a bad emoji or a reaction the message does not allow; 3, 4; 5 for example `MESSAGE_NOT_MODIFIED` when you remove a reaction that is not set; 6; 7; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.3.

### tg message pin

Pins one message for all chat participants.

```text
tg message pin <chat> <id> [flags]
```

<!-- schema:flags tg message pin -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check the action without changing Telegram |
| `--silent` | bool | `false` | pin without a notification |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, the chat in `write`. A pin is visible to every participant. Without `--silent` the request is sent without the silent flag, so Telegram notifies the participants (the notification itself was not observed live). After `pin`, also with `--dry-run`, the command prints a reminder on stderr that everyone sees the pin, with the `unpin` command (`--quiet` hides it). When Telegram wants admin rights for the pin, the answer is exit code 5 with `chat admin rights are required`. After a successful `pin` the Daemon sets `pinned` on the message that is already saved; it never creates a row, and if the database write fails the command still succeeds and the journal gets a record without parameters. The event stream does not process the pin updates of Telegram.

**Example.**

```sh
tg message pin @release_team 4102 --silent
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4102,"pinned":true}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `pinned` boolean, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional).

**Exit codes.** 0; 3, 4; 5 admin rights are required; 6; 7; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.3.

### tg message unpin

Unpins one message. There is no command to unpin everything.

```text
tg message unpin <chat> <id> [flags]
```

<!-- schema:flags tg message unpin -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check the action without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, the chat in `write`. After a successful `unpin` the Daemon updates `pinned` on the saved message (it never creates a row).

**Example.**

```sh
tg message unpin @release_team 4102
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4102,"pinned":false}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `pinned` boolean, `dry_run` boolean (optional), `action` string (optional), `count` integer (optional).

**Exit codes.** 0; 3, 4; 5; 6; 7; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.3.

### tg message poll

Sends a poll or a quiz.

```text
tg message poll <chat> [flags]
```

<!-- schema:flags tg message poll -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--correct` | int | `0` | correct option number, starting from 1; requires --quiz |
| `--dry-run` | bool | `false` | check the action without changing Telegram |
| `--explanation` | string | `none` | quiz explanation, up to 200 characters and two line feeds |
| `--idempotency-key` | string | `none` | idempotency key: 1 to 64 characters A-Za-z0-9._:- |
| `--multiple` | bool | `false` | allow multiple answers |
| `--option` | stringArray | `[]` | answer option, 1 to 100 characters; repeat 2 to 10 times in display order |
| `--public` | bool | `false` | show who voted |
| `--question` | string | `none` | poll question, 1 to 255 characters |
| `--quiz` | bool | `false` | make a quiz with one correct answer |
| `--reply-to` | int | `0` | reply to this message id |
| `--silent` | bool | `false` | send without a notification |
| `--topic` | int | `0` | send in this forum topic |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, the chat in `write`. Repeat `--option` once per answer, in display order. The poll is anonymous with one answer by default: `--public` shows who voted, `--multiple` allows several answers. `--quiz --correct N` makes a quiz with one correct answer (`N` counts from 1); `--explanation` sets the quiz explanation and only works with `--quiz`. A quiz cannot be combined with `--multiple`. `--reply-to`, `--topic` and `--silent` work as in `message send`.

**Limits.** The tool checks them before it calls Telegram and breaks them with exit code 2: a question of 1 to 255 characters, 2 to 10 options that differ after trimming spaces, each of 1 to 100 characters, an explanation of up to 200 characters and two line feeds. These are the limits of the tool, not facts about the server: the server may accept values the tool rejects, or reject values the tool accepts. Its rejection comes back as `POLL_*` or `QUIZ_*`, exit code 2 with the error type; `CHAT_SEND_POLL_FORBIDDEN` and `CHAT_ADMIN_REQUIRED` are exit code 5.

**Saved locally and read back.** A sent poll goes to the database with the question in `text`, `media_type` `poll` and `out: true`. When a message is read (`message history`, `message get` and the rest), the question of a poll is in `text` if the message has no text of its own, and `media_type` is `poll`. The options and the votes are not among the message fields.

**Example.**

```sh
tg message poll @release_team --question "Release day?" --option Tuesday --option Thursday --multiple
tg message poll @release_team --question "2 + 2?" --option 3 --option 4 --quiz --correct 2 --explanation "Plain arithmetic"
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"id":4120,"date":"2026-11-14T10:10:00Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `id` integer (optional), `date` string (optional), `dry_run` boolean (optional), `action` string (optional), `count` integer (optional), `text_length` integer (optional), `mode` string (optional), `plain_length` integer (optional), `entities` integer (optional), `rich` boolean (optional).

With `--idempotency-key`, `replayed` is an optional boolean. With `--dry-run`, `replay_available` is an optional boolean.

**Exit codes.** 0; 2 limits, a quiz with `--multiple`, `--correct` or `--explanation` without `--quiz`, fewer than 2 options; 3, 4; 5; 6; 7; 9.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 5.3.
