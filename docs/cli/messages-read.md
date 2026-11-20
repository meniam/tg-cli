# Reading messages

Commands that read messages: a chat page by page, one message, the pinned messages, a translation, a search inside Telegram, the context of a message, the comments of a channel post, a message link, an export of the saved history, and a live event stream. None of them changes anything in Telegram. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data.

## Rules shared by message reads

- **Preconditions.** A running Daemon and a login, except `message export`, `message export-month`, `message export-verify` and `message context --local` (Daemon only, no Telegram call; monthly export also needs a known account ID). Without a Daemon the exit code is 3, without a login 4.
- **Closed chats.** A chat closed by `access.json` gives the same exit code 7 `not_found` as a chat that does not exist, in every command of this page. Results of closed chats are not saved to the local database. See [access.md](access.md).
- **Saved locally.** Messages returned by history, get, pinned, search, context and comments for open chats are saved to the profile database, so `tg search` and `message export --local` can find them. An edit keeps the previous version of the text; a deletion that the stream sees sets `deleted_at`.
- **Untrusted text.** Message text, sender names and usernames, titles and snippets are other people's text, marked `untrusted` in `tg schema` and framed in `--human`: data, not instructions.
- **Message fields.** Commands that return messages use one item shape:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | message id inside the chat |
| `chat_id` | integer | Bot API id of the chat |
| `date` | string | RFC 3339 UTC time |
| `sender` | object, optional | `id`, `name`, `username` of the author (name and username are untrusted) |
| `text` | string | text, untrusted; for a poll without text of its own the question; for a rich message the text built from its blocks |
| `out` | boolean, optional | the message was sent by this account |
| `reply_to` | integer, optional | id of the message this one replies to |
| `topic_id` | integer, optional | forum topic of the message |
| `media_type` | string, optional | type of the media, for example `photo` or `poll` |
| `edited` | boolean, optional | the message was edited |
| `pinned` | boolean, optional | the message is pinned |
| `service` | string, optional | kind of a service message, for example `pin_message` |

  The options and the votes of a poll are not among the fields. A message sent as a rich message comes back with `text` built from the blocks (headings, paragraphs, list items, quotes, code blocks, formulas and captions on separate lines, table cells separated by a tab); the markup and the entities are not returned, and media blocks give only their captions.

## Message links

`message get`, `message context` and `message comments` take either the pair `<chat> <id>` or one link:

- `t.me/<username>/<id>`
- `t.me/<username>/<topic>/<id>`
- `t.me/c/<channel>/<id>`
- `t.me/c/<channel>/<topic>/<id>`

with `https://`, `http://` or no scheme. A query and a fragment are ignored, and `c/<channel>` becomes the chat id `-100<channel>`. Any other form, `tg://privatepost` included, is exit code 2 with an example (`invalid message link: expected https://t.me/channel/123`). `message link` goes the other way and builds a link from `<chat> <id>`.

## Reading a chat

### tg message history

Reads a chat from the newest message to the oldest.

```text
tg message history <chat> [flags]
```

<!-- schema:flags tg message history -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page, the value of next_cursor |
| `--limit` | int | `20` | messages per page, 1 to 100 |
| `--since` | string | `none` | only messages sent at this time or later (RFC 3339) |
| `--topic` | int | `0` | read one topic of a forum chat, by the topic id |
| `--until` | string | `none` | only messages sent at this time or earlier (RFC 3339) |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. `<chat>` is a chat reference, see [Addressing a chat](overview.md#addressing-a-chat); put `--` before a negative id: `tg message history -- -1001234567890`. `--topic` reads one topic of a forum chat by its id.

**Paging.** A page holds 20 messages by default and 100 at most. When more follow, the answer has `next_cursor`: pass it back with `--cursor` and read until no cursor comes. With `--format jsonl` each message is a line and the last line is `{"next_cursor":"..."}` while pages remain. `--since` and `--until` take RFC 3339 times, like `2026-01-31T10:00:00Z`, and both limits include a message sent exactly at that time.

**Example.**

```sh
tg message history @release_team --limit 50 --since 2026-11-01T00:00:00Z
tg message history --limit 20 --topic 7 -- -1001234567890
```

**Answer.**

```json
{"ok":true,"data":{"messages":[{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","sender":{"id":987654321,"name":"Alice"},"text":"Deploy is green"}],"next_cursor":"<opaque>"}}
```

**Answer fields** (`data`): `messages` array, `messages.id` integer, `messages.chat_id` integer, `messages.date` string, `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100, a time that is not RFC 3339, a malformed reference; 3, 4; 5 a Telegram error; 6 `FLOOD_WAIT`; 7 the chat does not exist or is closed.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.1 and 4.8.

### tg message get

Reads one message by its id, or by a message link.

```text
tg message get <chat> <id> | get <message-link> [flags]
```

<!-- schema:flags tg message get -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Give `<chat> <id>` or one message link, see [Message links](#message-links).

**Example.**

```sh
tg message get @release_team 4102
tg message get https://t.me/release_team/4102
```

**Answer.**

```json
{"ok":true,"data":{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","sender":{"id":987654321,"name":"Alice"},"text":"Deploy is green"}}
```

**Answer fields** (`data`): `id` integer, `chat_id` integer, `date` string, `sender` object (optional), `sender.id` integer (optional), `sender.name` string (optional, untrusted), `sender.username` string (optional, untrusted), `text` string (untrusted), `out` boolean (optional), `reply_to` integer (optional), `topic_id` integer (optional), `media_type` string (optional), `edited` boolean (optional), `pinned` boolean (optional), `service` string (optional).

**Exit codes.** 0; 2 a malformed reference or link; 3, 4; 6; 7 the chat or the message does not exist or is not visible.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.2 and 4.10.

### tg message pinned

Reads the pinned messages of a chat.

```text
tg message pinned <chat> [flags]
```

<!-- schema:flags tg message pinned -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `20` | pinned messages per page, 1 to 100 |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login.

**Example.**

```sh
tg message pinned @release_team --limit 5
```

**Answer.**

```json
{"ok":true,"data":{"messages":[{"id":4001,"chat_id":-1001234567890,"date":"2026-11-10T08:00:00Z","text":"Release rules","pinned":true}]}}
```

**Answer fields** (`data`): `messages` array, `messages.id` integer, `messages.chat_id` integer, `messages.date` string, `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100; 3, 4; 6; 7 the chat does not exist or is closed.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.2.

## Finding messages

`tg search` works on the local database and finds only what the Daemon has already read. The commands below ask Telegram itself, so they need a login (except `message context --local`). Not done: search filters by sender and date, search across several chats at once, context by forum topic, unread messages by topic and `tg://privatepost` links.

### tg message search

Searches the messages of one chat, or across Telegram with `--public`.

```text
tg message search <chat> <query> | search --public <query> [flags]
```

<!-- schema:flags tg message search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page |
| `--limit` | int | `20` | results per page, 1 to 100 |
| `--media` | string | `none` | media filter in one chat: photo, video, document, voice, audio, link, gif or pinned |
| `--public` | bool | `false` | search globally rather than inside one chat |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login.

**How it works.**

- Search in a chat: `message search <chat> <query>` calls `messages.search` and returns messages in the format of the history, with `next_cursor` for the next page. `--media` filters by type (`photo`, `video`, `document`, `voice`, `audio`, `link`, `gif`, `pinned`). An empty query is allowed only together with `--media`.
- Global search: `message search --public <query>` calls `messages.searchGlobal`. Hashtags are plain query text. The result is `hits`, a list of `{chat_id, chat?, message}`; with `--format jsonl` each hit is a line. `--media` with `--public` is exit code 2. In `tg schema` the command has `item` `messages` and `public_item` `hits`.
- A cursor continues the same search. The default `--limit` is 20, the largest 100.
- Access: global search drops the hits of closed chats (and hits without a chat id or whose message chat id differs from the hit's) before the answer. With the read filter off, hits from foreign public chats come back as they are; a nonempty `allow` drops them. Results of closed chats are not saved; results of open chats are saved like history.

**Example.**

```sh
tg message search @release_team "deploy" --media document --limit 10
tg message search --public "#release" --limit 20
```

**Answer.** In a chat:

```json
{"ok":true,"data":{"messages":[{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","sender":{"id":987654321,"name":"Alice"},"text":"Deploy is green"}],"next_cursor":"<opaque>"}}
```

With `--public`:

```json
{"ok":true,"data":{"hits":[{"chat_id":-1001234567890,"chat":{"id":-1001234567890,"type":"supergroup","title":"Release team"},"message":{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","text":"Deploy is green"}}]}}
```

**Answer fields** (`data`): `messages` array (optional), `messages.id` integer (optional), `messages.chat_id` integer (optional), `messages.date` string (optional), `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (optional, untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `hits` array (optional), `hits.chat_id` integer (optional), `hits.chat` object (optional), `hits.chat.id` integer (optional), `hits.chat.type` string (optional), `hits.chat.title` string (optional, untrusted), `hits.chat.username` string (optional, untrusted), `hits.chat.unread` integer (optional), `hits.message` object (optional), `hits.message.id` integer (optional), `hits.message.chat_id` integer (optional), `hits.message.date` string (optional), `hits.message.sender` object (optional), `hits.message.sender.id` integer (optional), `hits.message.sender.name` string (optional, untrusted), `hits.message.sender.username` string (optional, untrusted), `hits.message.text` string (optional, untrusted), `hits.message.out` boolean (optional), `hits.message.reply_to` integer (optional), `hits.message.topic_id` integer (optional), `hits.message.media_type` string (optional), `hits.message.edited` boolean (optional), `hits.message.pinned` boolean (optional), `hits.message.service` string (optional), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100, a bad `--media`, an empty query without `--media`, `--media` with `--public`, a wrong number of arguments (all before the Daemon is called); 3, 4; 6 `FLOOD_WAIT`; 7 the chat does not exist or is closed.

**Verification.** Global search can hit `FLOOD_WAIT` quickly; keep pauses between probes. See [FEATURES.md](../../FEATURES.md), item 4.3 and 4.9.

### tg message context

Reads the messages around one message.

```text
tg message context <chat> <id> | context <message-link> [flags]
```

<!-- schema:flags tg message context -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--after` | int | `5` | messages after the target, 0 to 50 (default 5) |
| `--before` | int | `5` | messages before the target, 0 to 50 (default 5) |
| `--local` | bool | `false` | read only the local database, without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. Without `--local` a login is needed too. `--local` reads only the local database: no Telegram and no login, only messages that are not deleted, and an unknown message is `not_found`. Give `<chat> <id>` or a message link.

**How it works.** The default window is 5 messages before and 5 after, 0 to 50 on each side, ascending by id and including the target. Without `--local`, Telegram determines the window at chat edges; see [FEATURES.md](../../FEATURES.md), item 4.5.

**Example.**

```sh
tg message context @release_team 4102 --before 3 --after 3
tg message context https://t.me/release_team/4102 --local
```

**Answer.**

```json
{"ok":true,"data":{"messages":[{"id":4101,"chat_id":-1001234567890,"date":"2026-11-14T09:12:00Z","text":"Build finished"},{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","text":"Deploy is green"}]}}
```

**Answer fields** (`data`): `messages` array, `messages.id` integer, `messages.chat_id` integer, `messages.date` string, `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--before` or `--after` outside 0 to 50, a malformed reference or link; 3; 4 (not for `--local`); 6; 7 the chat or the message is unknown or closed.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.5.

### tg message comments

Reads the comments (the discussion) under a channel post.

```text
tg message comments <chat> <id> | comments <message-link> [flags]
```

<!-- schema:flags tg message comments -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page |
| `--limit` | int | `20` | comments per page, 1 to 100 |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Give `<chat> <id>` or a message link. The command calls `messages.getReplies`. A post without a discussion is exit code 7 `not_found` with an explanation, and a chat that is not a channel is exit code 2.

**Example.**

```sh
tg message comments https://t.me/news_channel/88 --limit 20
```

**Answer.**

```json
{"ok":true,"data":{"messages":[{"id":512,"chat_id":-1002345678901,"date":"2026-11-14T10:00:00Z","sender":{"id":555000111,"name":"Bob"},"text":"Thanks for the update","reply_to":88}],"next_cursor":"<opaque>"}}
```

**Answer fields** (`data`): `messages` array, `messages.id` integer, `messages.chat_id` integer, `messages.date` string, `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100, a malformed link, a chat that is not a channel; 3, 4; 6; 7 no discussion, or an unknown or closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.4.

### tg message link

Builds a link to a channel or supergroup message.

```text
tg message link <chat> <id> [flags]
```

<!-- schema:flags tg message link -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--topic` | int | `0` | forum topic id, when the message belongs to a topic |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login: the link is built locally and `channels.exportMessageLink` is not called, but the chat is resolved as in other commands, so Telegram may be asked.

**How it works.** The result is `https://t.me/<username>/<id>` for a channel or supergroup with a username, otherwise `https://t.me/c/<channel>/<id>`. With `--topic` the topic number goes before the id. A private chat and a basic group have no message links: exit code 2. `message get` accepts the link back.

**Example.**

```sh
tg message link @news_channel 88
tg message link --topic 7 @release_forum 4102
```

**Answer.**

```json
{"ok":true,"data":{"link":"https://t.me/news_channel/88"}}
```

**Answer fields** (`data`): `link` string.

**Exit codes.** 0; 2 a private chat or a basic group, a malformed reference; 3, 4; 6; 7 an unknown or closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 3.10.

## Exporting saved messages

### tg message export

Streams the messages that the local database already holds for one chat, oldest id first.

```text
tg message export <chat> [flags]
```

<!-- schema:flags tg message export -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--after` | int | `0` | resume after this message id; 0 means from the beginning |
| `--include-deleted` | bool | `false` | include locally marked deleted messages |
| `--limit` | int | `0` | maximum number of output messages; unlimited when omitted |
| `--local` | bool | `false` | read only messages already stored locally |
| `--since` | string | `none` | messages sent at this time or later (RFC 3339) |
| `--until` | string | `none` | messages sent at this time or earlier (RFC 3339) |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `jsonl`, `human`.


**Preconditions.** A running Daemon (exit code 3 otherwise). No login or Telegram call is needed, but `auth_lost` or an unverified expected account returns code 4. `--local` is required: without it the command exits with code 2, because export straight from Telegram is not implemented.

**Output.** The default and the main format is `jsonl`: one message per line, ascending by `id`, no envelope and no `next_cursor` line. A line has the fields of `message context --local`: `id`, `chat_id`, `date` (RFC 3339, UTC), `sender` (`id`, `name`), `text`, and, when set, `out`, `reply_to`, `topic_id`, `media_type`, `edited`, `pinned`, `service`. It is not the raw Telegram object. `sender.username` is in the schema, but the export does not fill it for messages whose sender id or name is saved. `--format json` and `--format toon` are exit code 2. `--human` works and frames other people's text. `--fields id,text` keeps only these paths of a line; keep `id` to resume by `--after` (when `--quiet` hides the resume hint, take the `id` of the last printed line).

**Behavior.**

- **Deleted messages** are hidden. `--include-deleted` adds them with `"deleted":true`; other lines have no `deleted` field. The mark follows `deleted_at` in the database: `message delete` sets it after a successful call, and the live stream sets it for channels and supergroups and, since plan 12, for private chats and basic groups too (the deletion event carries no chat there, so the Daemon finds the chats by the saved message ids in every access-list mode and, with the filter on, only for open chats). A deletion that never arrived as a stream event (for example a `gap` the updates manager did not close) leaves no mark, and saving a message again clears the mark, so a missing `deleted` field does not prove that the message is alive in Telegram.
- **Filters.** `--since` and `--until` are inclusive RFC 3339 times, and a `--since` later than `--until` is exit code 2. `--limit N` stops after N lines; omitting it leaves the export unlimited. `--after N` continues with messages whose id is above N.
- **Closed and unknown chats** (also an `@username` the database has never seen) are exit code 7, checked on every page.
- **Pages and resuming.** The Daemon answers in pages of up to 500 messages and 768 KiB, and the client prints them as they arrive. A message that does not fit into a page stops the export with exit code 1 and `message <id> does not fit into a response page`; it can be skipped only by `--after <id>`, and it does not reach the export. If the export stops after some lines (the Daemon stopped, the chat was closed), the exit code is not 0, the printed lines stay valid, the last line of stdout is the error envelope, and stderr says `last exported id: N; resume with --after N and the same filters`. The export is not a snapshot: a message saved, edited or deleted while pages are read may or may not be included.
- **Completeness.** Only saved messages are exported, and a line holds the current text; earlier versions of an edited message stay in the database. When the export ends, stderr says ``exported N locally saved messages; check completeness with `tg chat sync status <chat>` `` (`--quiet` hides it). Run [`tg chat sync start`](chats.md#tg-chat-sync-start) first when the whole history is needed and read `complete` from `chat sync status`. Do not present an export as the whole history without that flag.

**Example.**

```sh
tg message export @release_team --local > release_team.jsonl
tg message export @release_team --local --since 2026-11-01T00:00:00Z --until 2026-11-30T23:59:59Z
tg message export @release_team --local --include-deleted --fields id,date,text,deleted
tg message export --local --after 4102 -- -1001234567890
```

**Answer.** One line per message:

```json
{"id":4102,"chat_id":-1001234567890,"date":"2026-11-14T09:12:40Z","sender":{"id":987654321,"name":"Alice"},"text":"Deploy is green"}
```

**Answer fields** (`data`): `messages` array, `messages.id` integer, `messages.chat_id` integer, `messages.date` string, `messages.sender` object (optional), `messages.sender.id` integer (optional), `messages.sender.name` string (optional, untrusted), `messages.sender.username` string (optional, untrusted), `messages.text` string (untrusted), `messages.out` boolean (optional), `messages.reply_to` integer (optional), `messages.topic_id` integer (optional), `messages.media_type` string (optional), `messages.edited` boolean (optional), `messages.pinned` boolean (optional), `messages.service` string (optional), `messages.deleted` boolean (optional), `next_after` integer (optional).

`deleted` is present only with `--include-deleted` on deleted messages. The schema also lists `next_after`, a field of the page that the Daemon answers; the printed lines do not carry it.

**Exit codes.** 0 the export reached its end or `--limit` (zero lines is a success too); 1 a message that does not fit into a page, or the connection to the Daemon broke; 2 no `--local`, `json` or `toon`, `--limit` below 1, a negative `--after`, a bad time, a `--since` later than `--until`; 3 no Daemon; 4 authorization lost or an expected account is unknown or mismatched; 7 an unknown or closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.4.

### tg message export-month

Writes one completed UTC calendar month of locally saved messages to an archive
inside an owner-approved `downloads` root.

```text
tg message export-month <chat>... --month YYYY-MM --output <directory> [flags]
```

<!-- schema:flags tg message export-month -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--include-deleted` | bool | `false` | include locally marked deleted messages |
| `--month` | string | `none` | completed UTC month (YYYY-MM) |
| `--output` | string | `none` | required absolute output directory |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`,
`jsonl`, `toon`, `human`.

**Preconditions.** The Daemon must be running and must know the account id. The
command reads only the local database and does not call Telegram. The owner must
list a `downloads` directory in `access.json`; the absolute existing `--output`
directory must be inside one of those roots. No root is code 9; an invalid
output directory is code 2.

**Month and chats.** Give one through 50 distinct visible chats. `--month` is a
completed UTC month, such as `2026-10`; the current and future months and invalid
month strings are code 2. The selection is `[2026-10-01T00:00:00Z,
2026-11-01T00:00:00Z)`. Every JSONL line has the same encoding as `message
export --local` with those boundaries and the selected `--include-deleted`
setting, ordered by message id. A line over 768 KiB or all JSONL files over 1
GiB stops the command with code 1.

**Archive.** The Daemon creates `tg-export-<account_id>-<month>` below
`--output`, mode 0700. It writes `<chat_id>.jsonl` for each chat, then
`SHA256SUMS`, then `manifest.json`; files are mode 0600. `SHA256SUMS` is in the
`shasum -a 256 -c` form `<hex>  <name>`. `manifest.json` is the completion
marker. It contains its format and version, account id, month, UTC boundaries,
include-deleted setting, creation time, tool version, and each file's id, count,
byte size, SHA-256, first and last message id, and saved sync coverage. It
contains no chat names or message text.

`--output` cannot contain an existing symbolic link below its configured
`downloads` root. Archive files are first written under the opened downloads
root and published there with a new hard link. On macOS and Linux, every
directory component used to publish a file is opened with `O_NOFOLLOW`, so a
directory substituted with a symbolic link cannot receive the file. Other
platforms reject archive writes.

`sync.month_start_covered` is true when the stored history load is complete, or
when its oldest locally loaded message predates the month. It does not prove that
Telegram has no unseen messages or changes. An empty month still creates an
empty JSONL file with the SHA-256 of empty input.

**Repeat and failure.** A complete archive is verified and returned with
`existing:true`; it is never rewritten. An existing directory without
`manifest.json` is left untouched and fails with code 1. A failure while creating
a new archive removes only that new archive directory. Unknown or closed chats
fail with code 7 before a new directory is created.

**Output.** The result has `dir`, `month`, `existing`, `manifest_sha256`, and
`files`. A file item has `name`, `chat_id`, `messages`, `bytes`, `sha256`, and
`month_start_covered`; `--format jsonl` prints one file item per line.

```sh
tg chat sync start @release_team
tg chat sync status @release_team
tg message export-month @release_team --month 2026-10 --output /Users/me/tg-archives
```

**Exit codes.** 0 a new or valid existing archive; 1 an incomplete archive, a
line or total-size limit, or another archive failure; 2 invalid month, chat
count, duplicate chat, or output outside `downloads`; 3 no Daemon; 4 unknown or mismatched account or authorization lost; 7 unknown or closed chat; 9 no permitted downloads root; 10 client restart.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 13.4.

### tg message export-verify

Checks a monthly archive without contacting Telegram.

```text
tg message export-verify <archive-directory>
```

<!-- schema:flags tg message export-verify -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`,
`toon`, `human`.

The archive directory must be absolute and inside an owner-approved `downloads`
root. The command reads `manifest.json`, each listed JSONL file and
`SHA256SUMS`, compares hashes and byte counts, checks missing and extra files,
and rejects symbolic links. The result has `dir`, `month`, `ok`, `files`,
`missing`, and `extra`. A file item has `name`, `ok`, `expected_sha256`,
`actual_sha256`, and `bytes`; `--format jsonl` prints one item per line.

`ok:false` prints the result and exits 1. A missing or invalid manifest is also
code 1. Verification only proves saved archive bytes and structure, not the
completeness of Telegram history.

```sh
tg message export-verify /Users/me/tg-archives/tg-export-123456789-2026-10
(cd /Users/me/tg-archives/tg-export-123456789-2026-10 && shasum -a 256 -c SHA256SUMS)
```

For an external monthly schedule, run after the UTC month ends. This macOS
example runs on the second day in the scheduler's local time; `date -v` is a
BSD `date` flag.

```cron
10 3 2 * * tg message export-month @release_team --month "$(date -u -v-1m +\%Y-\%m)" --output /Users/me/tg-archives
```

**Exit codes.** 0 verified; 1 mismatch or invalid archive; 2 invalid archive
directory; 3 no Daemon; 4 authorization lost or an expected account is unknown or mismatched; 7 a now-closed chat in the manifest; 9 no permitted
downloads root.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 13.4.

## Live events

### tg message watch

Watches live Telegram events through the Daemon: new messages, edits, deletions, reactions, reads, typing and online status, one JSON line per event.

```text
tg message watch [flags]
```

<!-- schema:flags tg message watch -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--after` | string | `none` | resume after an event cursor; empty starts at the current cursor |
| `--chat` | stringArray | `[]` | watch a chat by reference; repeat for more chats |
| `--max-events` | int | `0` | stop after this many events; default 0 means no limit, an explicit value must be positive |
| `--topic` | int | `0` | watch one forum topic by id |
| `--types` | string | `none` | comma-separated event types: message.new, message.edit, message.delete, reaction, read, typing, status, gap or lagged |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `jsonl`, `json`, `human`.


**Preconditions.** A running Daemon and a login. The Daemon runs the Telegram updates manager after login; it keeps its state in the local database and asks Telegram for what was missed after a break. A first start without state goes "from this moment" and loads no history. An active subscriber keeps `--idle-timeout` from stopping the Daemon. For a script set `--max-events`: without it the command runs until Ctrl-C, SIGTERM, `lagged` or a stop of the Daemon.

**Event types.**

| `type` | Stored and replayed | `data` |
| --- | --- | --- |
| `message.new`, `message.edit` | yes | the message, in the shape of [Message fields](#rules-shared-by-message-reads) |
| `message.delete` | yes | `ids` and, when Telegram names the chat, `chat_id` |
| `reaction` | yes | `chat_id`, `message_id`, `reactions` (a list of `emoji` and `count`) |
| `read` | live only | `chat_id`, `max_id`, `outbox` |
| `typing` | live only | `chat_id`, `user_id`, `action` |
| `status` | live only | `user_id`, `online` |
| `gap` | live only | `reason` and, when known, `chat_id`: the updates manager could not close a hole (or a persistent failure to store an event, `reason: "store"`); read the history with `message history` and do not treat the feed as complete |
| `lagged` | live only | the subscriber is too slow, the stream closes |

The shapes of `data` come from the wire types; the schema marks `data` as `json` and `untrusted`.

**Output.** The default format is `jsonl`: one whole event per line (`id`, `type`, `chat_id`, `time`, `data`), no envelope. `--format json` works only together with `--max-events` and prints one envelope `{"ok":true,"data":{"events":[...]}}` at the end; without `--max-events` it is exit code 2. `--format toon` is exit code 2. `--human` prints one line per event and frames other people's text with `| ` under a warning. `--quiet` hides the hint, not the frame. `--fields` applies to every event. The hint `watching from cursor <cursor>` goes to stderr.

**Cursor and replay.** The `id` of a stored event is a string `<id>.<pts>.<qts>.<date>`. Only the leading `id` matters for resuming; the triple is the state of the updates manager when the cursor is issued and is informational. Live-only events and `gap` have no `id`; do not drop `id` with `--fields` if you want to resume. Pass the last cursor as `--after`: the Daemon replays the stored events after it, then the live ones, without a gap or a repeat (idempotent by a deduplication key). Without `--after` the stream starts from the current moment. The journal keeps 7 days or 100 000 events, whichever limit comes first, and is pruned at start and every hour. An `--after` older than the border is `cursor_expired` (exit code 8): run without `--after` and read the missing part with `message history`.

**Filters and the access lists.**

- `--chat` (repeatable) is resolved from the local database only: read the chat first (`chat list`, `message history`), otherwise `not_found`. A closed or unknown reference gives the same `not_found`.
- `--topic` keeps only `message.new` and `message.edit` of the topic. `--types` takes a comma-separated list of the types above.
- A `gap` without a chat passes every filter, including `--chat`, `--types` and `--topic`.
- Events of closed chats are dropped before they are stored and before delivery, including `typing`, `read` and `status` (for `status` the chat is the private chat of that user); the policy is applied again on replay. A deletion in a private chat or a basic group comes from Telegram without a chat id: the Daemon looks the chats up by message id in the local database and sets `deleted_at` in each chat found. With the filter on it drops the whole event, marking nothing, when a chat is closed or unknown, and sets `chat_id` only when the messages belong to exactly one chat; with the filter off the event goes without `chat_id`.
- Messages from the stream are saved to the local database, so `tg search` finds them.

**A slow reader.** The queue of a subscriber holds 1000 events. When it overflows, the Daemon sends `lagged` and closes the stream; the events stay in the journal, and the command exits with code 1 and the hint to resume with `--after <cursor>`.

**Raw JSON-RPC.** The method `subscribe` takes `chats`, `topic`, `types` and `after` and answers `subscription_id` and the start `cursor`; then the Daemon sends notifications without `id` (method `event`) on the same connection. One subscription per connection: a second `subscribe` is `busy`. Closing the socket or `unsubscribe` removes the subscription.

**Example.**

```sh
tg message watch                                  # all visible chats, jsonl
tg message watch --chat @release_team --topic 7   # one chat, one forum topic
tg message watch --types message.new,reaction --max-events 20
tg message watch --after 812.40117.0.1763111560   # resume from a cursor (illustrative value)
```

**Answer.** One line per event:

```json
{"id":"812.40117.0.1763111560","type":"message.delete","chat_id":-1001234567890,"time":"2026-11-14T09:20:00Z","data":{"chat_id":-1001234567890,"ids":[4102]}}
```

**Answer fields** (`data`): `events` array, `events.id` string (optional), `events.type` string, `events.chat_id` integer (optional), `events.time` string, `events.data` json (optional, untrusted).

**Exit codes.** 0 Ctrl-C, SIGTERM or `--max-events` reached; 1 `lagged` (with the hint `--after <cursor>`); 2 `--format json` without `--max-events`, `--format toon`, an unknown type in `--types`; 3 the Daemon stopped or the connection closed; 4 no login; 7 a closed or unknown chat in `--chat`; 8 `cursor_expired`.

**Verification.** A persistent journal write failure can advance the updates manager and produce a `gap`; `ChannelDifferenceTooLong` also produces a `gap`. See [FEATURES.md](../../FEATURES.md), item 8.2 and 8.4.

### tg message unread

Read unread incoming messages without marking them read. Use `--topic` for a forum topic, `--limit` for the page size and `--cursor` to continue. Pages are newest first; outgoing messages are omitted, so a page may contain fewer items than the limit even when `next_cursor` is present. The read marker is refreshed on every page.

<!-- schema:flags tg message unread -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue from next_cursor |
| `--limit` | int | `20` | messages per page, 1 to 100 |
| `--topic` | int | `0` | forum topic id |
<!-- /schema:flags -->

### tg message readers

Read the users who read an outgoing message in a group, supergroup or private user chat. Telegram limits receipt availability by message age, group size and privacy settings. Names are untrusted. Blocked users are omitted.

```text
tg message readers <chat> <id>
```

<!-- schema:flags tg message readers -->
No command flags.
<!-- /schema:flags -->

### tg message transcribe

Turns a voice message, video note or video into text with the local speech engine ([Voice transcription](voice.md)). The command puts the message at the front of the voice queue and waits for the text; Telegram is asked for the file only, never to transcribe. It does not send anything to a chat. It accepts a chat and message id, or a `t.me` message link. `--telegram` asks Telegram itself instead (Premium needed); it is the single path that calls `messages.transcribeAudio`.

```text
tg message transcribe <chat> <id>
tg message transcribe <message-link>
```

<!-- schema:flags tg message transcribe -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--telegram` | bool | `false` | ask Telegram to transcribe instead of the local engine (needs Premium) |
| `--wait` | duration | `1m0s` | wait for the text, up to 600s (up to 90s with --telegram) |
<!-- /schema:flags -->

The result has `chat_id`, `id`, `transcription_id`, `text`, `pending`, `cached`, `source`, `engine` and `queued`. `text` is untrusted Telegram data. `source` is `local` for the local engine and `telegram` for Telegram's text; `engine` names the local engine (`gigaam-v3`). `cached: true` means a stored text was returned at once, without any work or request. `queued: true` means the local queue produced the answer. A finished text is saved in the local profile database; a later request returns it with `cached: true`. If the job is not done when `--wait` ends, the command succeeds with `pending: true` and the job goes on: ask again later. `--wait 0s` returns at once. The manual job runs before every other job and ignores the daily budget; it works with `[voice_transcription] enabled = false` too, but needs the engine and `ffmpeg` installed (`tg voice install`). A message that is not in the local database is read from Telegram first and saved.

Only messages with `media_type` `voice`, `video_note` or `video` can be transcribed through the queue; with `--telegram` only `voice` and `video_note`. Another message, a non-positive id, or a wait outside the range is exit code 2, as is a missing engine (`error.reason: "engine_missing"`). A job that ends `skipped` for the limits (`too_large`, `too_long`, `no_media`) is exit code 2 and one that fails or is cancelled is exit code 1; both name the reason in `error.reason`. A closed or unknown chat is exit code 7. With `--telegram`, Telegram refusals are exit code 5; a Premium or exhausted free-trial refusal has `error.reason: "premium_required"`, and another transcription refusal has `error.reason: "transcription_failed"`. `trial_remains` and `trial_resets_at` appear in a `--telegram` response when Telegram provides free-trial values. `--telegram` replaces a text the local engine wrote earlier.

The `transcriptions` table is added by a database migration. An older binary refuses to open a profile database that has a newer migration; restore a database backup or use a current binary before rolling back.

### tg message translate

Translates one to 20 messages with Telegram. It accepts a chat and one or more message ids, or one `t.me` message link. It sends Telegram the peer, message ids and target language: it does not send arbitrary text, change the account, or store translations locally.

```text
tg message translate <chat> <id>... --to <language>
tg message translate <message-link> --to <language>
```

<!-- schema:flags tg message translate -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--to` | string | `none` | target language as two Latin letters (required) |
<!-- /schema:flags -->

`--to` is required and normalized to lower case. It must contain exactly two Latin letters. Each id is positive and unique; more than 20 ids, duplicate ids, and invalid language input are exit code 2 before Telegram is called. The command is class `read`; a closed chat is exit code 7 before Telegram is asked to translate messages. A non-numeric chat reference can first be resolved through Telegram.

The result contains `chat_id`, `to_lang`, and `items`. Every item has the input `id` and untrusted `text`. The item order is the input id order. With `--format jsonl`, each item is one line. Translation entities are not returned.

Telegram input errors for an id, an empty message, overly long text, or an unsupported target language are exit code 2. `TRANSLATIONS_DISABLED` is exit code 5 with `error.reason: "translations_disabled"`. Temporary translation failures and quotas are exit code 5 with `error.reason: "translation_unavailable"`; retry later rather than in a tight loop. A Premium refusal is exit code 5 with `error.reason: "premium_required"`. If Telegram returns a different number of translations than ids, the command returns exit code 5 and does not associate partial results with ids.

### tg message download

Download media to a directory explicitly allowed by the owner in `access.json` under `downloads`. `--output` must be an existing absolute directory. At most 20 messages, 100 MiB per file and 500 MiB per call are allowed. Protected content is refused. Existing files are not overwritten. `--dry-run` checks metadata without writing. A partial success exits with code 1.

```text
tg message download <chat> <id>... --output <directory>
tg message download <message-link> --output <directory>
```

<!-- schema:flags tg message download -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check media without writing files |
| `--output` | string | `none` | required absolute output directory |
<!-- /schema:flags -->

**Transfer and integrity.** The Daemon transfers the parts of one file through a
pool for its media DC. A download has five workers; its DC-wide adaptive window
starts at four in-flight parts and can grow to eight. At most two media transfers
(downloads and uploads together) run in one Daemon; another waits until a slot is
free or the command is cancelled. Parts are written by offset to a mode-0600
temporary file inside the opened `downloads` root. The final name is linked only
after every byte is present at the expected size, so a failed or cancelled
download leaves no temporary or partial final file.

Documents are also verified against the hashes Telegram returns; photos are
checked by their expected size. The result of a completed non-dry-run download
adds `files[].sha256`, the lowercase SHA-256 of the published file. A hash or
size mismatch removes the temporary file and is reported as a Telegram error.

`FLOOD_WAIT` pauses the account-wide throttle. For a part, a wait no longer than
`behavior.flood_autowait` is waited out and the part is retried. For a longer wait, a call with no successful files returns the first error (code 6 if that error is `flood_wait`); a partial success returns code 1 and lists the failed item in `errors`. `--dry-run` makes no part request.
