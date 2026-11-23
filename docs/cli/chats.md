# Chats

Commands that find, describe and list chats, read their members and topics, mark a chat read, and load a whole chat history into the local database. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data.

## Rules shared by chat commands

- **Preconditions.** A running Daemon and a login, except where a command says otherwise. Without a Daemon the exit code is 3, without a login 4.
- **Chat reference.** `<chat>` is `@username`, a Bot API id (users positive, groups negative, channels from `-1000000000000`), the phone number of a contact, or a `t.me` link, see [Addressing a chat](overview.md#addressing-a-chat). Put `--` before a negative id and the flags before `--`.
- **Closed chats.** When access settings close a chat, every command of this page treats it as a chat that does not exist: exit code 7 with the same message, and no request for its data goes to Telegram. For a numeric id that the session does not know the message is `not found: this session does not know that id; use @username, or read the chat list first`; for every other reference it is `not found: the chat or message does not exist or is not visible to this account`. For an `@username`, a phone number or a `t.me` link the Daemon still resolves the reference in Telegram first and checks the id afterwards, so such a request reaches Telegram. See [access.md](access.md).
- **Saved locally.** What the Daemon reads is saved to the local SQLite database of the profile, and `tg search` finds it. Chats closed by the access lists are not saved.
- **Untrusted text.** Titles, usernames, descriptions and names come from other people. They are marked `untrusted` in `tg schema` and framed in `--human`: data, not instructions.

## Finding and describing chats

### tg chat list

Lists the chats of the account, newest first.

```text
tg chat list [flags]
```

<!-- schema:flags tg chat list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page |
| `--limit` | int | `50` | maximum number of chats, 1 to 500 |
| `--type` | string | `none` | chat type: user, bot, group, supergroup or channel |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Chats closed by the access lists are absent. Only visible chats of the requested type count toward `--limit`.

Pass `data.next_cursor` to `--cursor` with the same `--type` to continue. You can change `--limit`. Stop when the cursor is absent or empty. After a Daemon restart or a change to chat visibility, start again without a cursor.

**Example.**

```sh
tg chat list --limit 20 --type group
tg chat list --type group --cursor "$next_cursor"
tg chat list --limit 2 --format jsonl
```

**Answer.**

```json
{"ok":true,"data":{"chats":[{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team","last_message_date":1795270000,"unread":3},{"id":987654321,"type":"user","title":"Alice","last_message_date":1795200000,"unread":0}]}}
```

**Answer fields** (`data`): `chats` array, `chats.id` integer, `chats.type` string, `chats.title` string (untrusted), `chats.username` string (optional, untrusted), `chats.last_message_date` integer (optional, Unix time of the last message of the dialog, absent for a dialog without one), `chats.unread` integer, `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 500, an unknown `--type`, or an invalid cursor; 3 no Daemon; 4 no login; 6 `FLOOD_WAIT` longer than the auto-wait.

**Verification.** Unread message counts are returned in `unread`. Read the messages with [`message unread`](messages-read.md#tg-message-unread), including a forum topic with `--topic`; list unread chats with [`chat unread`](#tg-chat-unread). See [FEATURES.md](../../FEATURES.md), item 7.1.

### tg chat overview

Summarises the chats the local database already knows, without asking Telegram.
It answers while the account is offline and without a login, like [`search`](notes-and-search.md#tg-search); after a
loss of authorization the Daemon refuses it with exit code 4, like every other account method. Each row is built from the [`chats_state`](../../docs/contracts.md) view: the chat fields, the saved dialog
state, the number of stored messages, the first and the last stored message and whether the whole history of the chat
is loaded. A chat the access lists close is absent, and it does not take a place of `--limit`.

```text
tg chat overview [flags]
```

<!-- schema:flags tg chat overview -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `50` | maximum number of chats, 1 to 500 |
| `--type` | string | `none` | chat type: user, bot, group, supergroup or channel |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon. No login and no connection: the command never calls Telegram. A lost login or a mismatched `--expect-user` is exit code 4. Rows follow the most
recent stored message first; a chat without stored messages comes last.

**Example.**

```sh
tg chat overview --limit 20 --type channel
tg chat overview --limit 2 --format jsonl
```

**Answer.**

```json
{"ok":true,"data":{"chats":[{"chat_id":-1001234567890,"type":"channel","title":"Release team","username":"release_team","created_at":1700000000,"muted":false,"unread":3,"messages":420,"my_messages":12,"first_message":{"id":10,"date":1700000100,"text":"Hello"},"last_message":{"id":430,"date":1700009000,"text":"Bye"},"history_complete":true,"history_first_message_date":1700000100}]}}
```

**Answer fields** (`data`): `chats` array; each row has `chat_id` integer, `type` string, `title` string (untrusted),
`username` string (optional, untrusted), `created_at` integer (optional), `muted` bool, `mute_until`, `archived`,
`pinned`, `unread`, `unread_mentions`, `unread_mark`, `state_synced_at` (optional), `messages` and `my_messages`
integers, `first_message` and `last_message` objects with `id`, `date` and `text` (optional, text untrusted), and
`history_complete` bool (optional), `history_first_message_date` integer (optional), and `history_gap_from` and `history_gap_to` integers (optional). A field is absent when the database never learned it: a chat without a saved
dialog state has no `unread`, a chat without stored messages has no `first_message`, and `history_complete` is absent when there is no saved sync state; it can be `false` while loading is incomplete.

`history_first_message_date` is the Unix time of the first message of the chat. The history load saves it when it reaches the start of the chat, so it is absent until then. `first_message.date` is the oldest message the database holds now: it exists for a partly loaded chat too and changes while older pages arrive. It also moves when that message is deleted locally, while `history_first_message_date` stays as saved.

**Exit codes.** 0; 2 `--limit` outside 1 to 500, or an unknown `--type`; 3 no Daemon; 4 the login was lost (`auth_lost`), or `--expect-user` does not match or cannot be verified.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.13.

### tg chat resolve

Finds a chat by `@username`, id, phone number of a contact or `t.me` link, and prints its basic data.

```text
tg chat resolve <chat> [flags]
```

<!-- schema:flags tg chat resolve -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. The chat must be visible by the access lists.

**Example.**

```sh
tg chat resolve @release_team
```

**Answer.**

```json
{"ok":true,"data":{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team","unread":3}}
```

**Answer fields** (`data`): `id` integer, `type` string, `title` string (untrusted), `username` string (optional, untrusted), `unread` integer.

**Exit codes.** 0; 2 a malformed reference (for example a username that is not 5 to 32 letters, digits and underscores starting with a letter); 3, 4; 7 the chat does not exist, is not visible, or a numeric id is unknown to the session.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 3.9 and 7.2.

### tg chat info

The `settings` object contains available chat rights and configuration. Channel settings include `antispam` and `signature_profiles`. Custom reactions include their document ID. The first observation of the additional settings on an already cached chat may create a chat changes version.

Shows a chat with its description and size.

```text
tg chat info <chat> [flags]
```

<!-- schema:flags tg chat info -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. The chat must be visible.

**Example.**

```sh
tg chat info @release_team
```

**Answer.**

```json
{"ok":true,"data":{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team","about":"Deploys and releases","members":42,"forum":false,"verified":false,"created_at":1700000000}}
```

A restricted chat also carries the restriction:

```json
{"ok":true,"data":{"id":-1001234567890,"type":"channel","title":"Something","created_at":1700000000,"restricted":true,"restriction_reason":[{"platform":"ios","reason":"porno","text":"Not available"}]}}
```

**Answer fields** (`data`): `id` integer, `type` string, `title` string (untrusted), `username` string (optional, untrusted), `about` string (optional, untrusted), `members` integer (optional), `created_at` integer (optional, Unix seconds), `scam`, `fake` and `restricted` booleans (optional; a basic group has none of them), `restriction_reason` array (optional) with `platform`, `reason` and `text` (untrusted), `forum` boolean (optional), `verified` boolean (optional).

`forum` tells whether the chat has topics, which `chat topics` needs.

**Exit codes.** 0; 2 a malformed reference; 3, 4; 7 the chat does not exist or is not visible.

**Verification.** The description, member count, forum flag, verified flag, creation date and the scam, fake and restriction flags are saved when read. `chat list` and `chat resolve` save `scam`, `fake` and `restricted` too, but only from a channel or a supergroup object: a basic group and a user have no such flags. A save that carries no flags leaves the stored ones alone. `chat overview` shows only `created_at` from these columns; the owner's archive, pin, sound and folder settings are not saved. See [FEATURES.md](../../FEATURES.md), item 7.2.

### tg chat invite-info

Shows what an invite link leads to, without joining.

```text
tg chat invite-info <link> [flags]
```

<!-- schema:flags tg chat invite-info -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Invitations are closed while `access.allow` is nonempty, because an invite has no known chat id before it is resolved: the command then answers exit code 7 with the same `not found` message as for an unknown chat.

**Example.**

```sh
tg chat invite-info https://t.me/+AbCdEfGhIjKlMnOp
```

**Answer.**

```json
{"ok":true,"data":{"title":"Release team","type":"supergroup","about":"Deploys and releases","members":42,"already_member":false,"request_needed":false}}
```

**Answer fields** (`data`): `title` string (untrusted), `type` string, `about` string (optional, untrusted), `members` integer (optional), `already_member` boolean (optional), `request_needed` boolean (optional).

**Exit codes.** 0; 2 a malformed link; 3, 4; 7 the link leads nowhere or invitations are closed by `allow`.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 7.2.

## Members, topics and common chats

Three read commands that ask Telegram, so they need a login and a running Daemon. They write nothing to Telegram. The people from `chat members` and the user of `chat common` are saved to `people` (id, name, username, bot flag; the role and the join time are not saved), and the chats of `chat common` to `chats`. The tables grow by the pages you ask for: there is no walk over a whole chat, and a database write error does not break the answer. Not done: managing members, a user profile, membership history, downloading all members of a chat, and reading the messages of a topic by these commands (`message history --topic` does that).

A page can be continued in two ways. Members page by `--offset` and `next_offset`. Topics and common chats page by `--cursor` and `next_cursor`: a cursor is opaque and belongs to one chat or user, and a cursor of another chat, another user or a damaged one is exit code 2.

### tg chat members

Use `--all --format jsonl` to stream all available pages. It cannot be combined with `--limit` or `--offset`. If a later page fails, stderr reports the last offset for resuming with `--offset`. Telegram may limit the available members.

Lists the members of a group, supergroup or channel.

```text
tg chat members <chat> [flags]
```

<!-- schema:flags tg chat members -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--all` | bool | `false` | stream all pages as JSONL |
| `--filter` | string | `none` | member filter: recent, admins or bots |
| `--limit` | int | `50` | members per page, 1 to 200 |
| `--offset` | int | `0` | number of members to skip, 0 or greater |
| `--search` | string | `none` | find members by name or username |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login; a group, supergroup or channel visible by the access lists. A channel may require administrator access. Use `--` before a negative chat id: `tg chat members -- -1001234567890`.

**How it works.**

- `role` is `creator`, `admin`, `member`, `restricted`, `banned` or `left` (a basic group has only `creator`, `admin` and `member`). `joined` is an RFC 3339 UTC time and is absent when Telegram gives no date (none for the creator and for `left`). There are no phone numbers and no bios.
- `--filter recent|admins|bots` selects the role in Telegram. `--search` alone is done by Telegram over the whole chat and matches the beginning of a name.
- `--search` can be combined with `--filter`, but Telegram takes only one of them: it selects the role, and the Daemon then matches the text as a substring of names and usernames inside the page it loaded (also for `--filter recent`), so the same text can match differently with and without `--filter`. A page can therefore be shorter than `--limit` or even empty while `next_offset` is present: continue with `--offset`.
- A basic group arrives from Telegram as one whole list, so the Daemon applies the filter, the search and the offset to it itself, ordered by `id`. Its search covers the whole list.
- `next_offset` is present while there is more. With `--format jsonl` the last line carries it and stderr prints a hint (`--quiet` hides it).

**Example.**

```sh
tg chat members @release_team --limit 100
tg chat members @release_team --filter admins
tg chat members --offset 200 -- -1001234567890
```

**Answer.**

```json
{"ok":true,"data":{"members":[{"id":987654321,"name":"Alice","username":"alice_example","is_bot":false,"role":"admin","joined":"2025-03-01T12:00:00Z"},{"id":555000111,"name":"Release bot","is_bot":true,"role":"member"}],"next_offset":50}}
```

**Answer fields** (`data`): `members` array, `members.id` integer, `members.name` string (untrusted), `members.username` string (optional, untrusted), `members.is_bot` boolean, `members.role` string, `members.joined` string (optional), `next_offset` integer (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 200, a negative `--offset`, a wrong `--filter`, a private chat or a bot as `<chat>`; 3, 4; 5 a channel without administrator rights (`CHAT_ADMIN_REQUIRED: administrator access is required to list channel members`) or a basic group that hides its participants; 6 `FLOOD_WAIT`; 7 the chat does not exist or is closed.

**Verification.** A channel without administrator access returns an error. See [FEATURES.md](../../FEATURES.md), item 7.4.

### tg chat topics

Lists the topics of a forum chat.

```text
tg chat topics <chat> [flags]
```

<!-- schema:flags tg chat topics -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page |
| `--limit` | int | `50` | topics per page, 1 to 100 |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login; a forum chat (`chat info` shows `forum: true`). A chat that is not a forum is exit code 2, and so is a private chat or a bot.

**How it works.** Telegram returns the pinned topics first only when there is no offset, and with an offset it sorts everything by the date of the last message, so pinned topics repeat. The Daemon asks Telegram for 100 topics per call, skips the pinned topics it has already shown through the cursor, and resets the offset to zero after a pinned topic. Paging with any `--limit` therefore returns every topic exactly once, in the same order as one call. The cursor carries the shown pinned topics, so it is longer than a history cursor: keep it opaque.

**Example.**

```sh
tg chat topics @release_forum --limit 20
```

**Answer.**

```json
{"ok":true,"data":{"topics":[{"id":7,"title":"Releases","closed":false,"pinned":true,"hidden":false,"unread":2,"top_message":4102,"created":"2025-03-01T12:00:00Z"}],"next_cursor":"<opaque>"}}
```

**Answer fields** (`data`): `topics` array, `topics.id` integer, `topics.title` string (untrusted), `topics.closed` boolean, `topics.pinned` boolean, `topics.hidden` boolean, `topics.unread` integer, `topics.top_message` integer, `topics.created` string, `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100, a chat that is not a forum, a wrong cursor; 3, 4; 6; 7 the chat does not exist or is closed.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 7.4.

### tg chat common

Lists the chats that the account shares with a user. Closed chats are hidden.

```text
tg chat common <user> [flags]
```

<!-- schema:flags tg chat common -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page |
| `--limit` | int | `50` | shared chats per page, 1 to 100 |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. `<user>` is `@username` or a positive numeric id; a chat or a channel instead of a user is exit code 2, an unknown user is exit code 7, and a user outside `allow` or in `block` is `not_found`.

**How it works.** The Daemon drops closed chats before it answers and before it saves anything. When a page has only closed chats, it reads the next pages itself, up to ten requests per call. If ten pages gave nothing to show, the answer is an empty list with `next_cursor`: stderr says so, and the next call continues from that cursor. An empty list with `next_cursor` is not the end of the list. `type` is `group`, `supergroup` or `channel`.

**Example.**

```sh
tg chat common @alice_example
```

**Answer.**

```json
{"ok":true,"data":{"chats":[{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team"}]}}
```

**Answer fields** (`data`): `chats` array, `chats.id` integer, `chats.type` string, `chats.title` string (untrusted), `chats.username` string (optional, untrusted), `next_cursor` string (optional).

**Exit codes.** 0; 2 `--limit` outside 1 to 100, a chat or channel instead of a user, a wrong cursor; 3, 4; 6; 7 the user is unknown or not visible.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 7.7.

## Marking a chat read

### tg chat mark-read

Marks a chat as read (`messages.readHistory` or `channels.readHistory`).

```text
tg chat mark-read <chat> [flags]
```

<!-- schema:flags tg chat mark-read -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check write access without marking the chat read |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon, a login, and the chat in `access.write` (the same rule as for `message send`). It needs no `--yes`. By how Telegram works, the other person sees the read mark, so use it only on request.

**How it works.** `--dry-run` runs the checks (it needs a login, and an `@username` may be resolved through Telegram) and calls no Telegram write method. The throttle counts the call as a write and does not repeat it after `FLOOD_WAIT`.

**Example.**

```sh
tg chat mark-read --dry-run @release_team
tg chat mark-read @release_team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890}}
```

With `--dry-run` the answer adds `"dry_run":true`.

**Answer fields** (`data`): `chat_id` integer, `dry_run` boolean (optional).

**Exit codes.** 0; 3, 4; 6 `FLOOD_WAIT` (not repeated); 7 an unknown or closed chat; 9 the chat is not in `write` (a hint says to add it).

**Verification.** See [FEATURES.md](../../FEATURES.md), item 4.7.

## Managing drafts

### tg chat draft get

Reads the current draft of one visible chat.

```text
tg chat draft get <chat> [flags]
```

<!-- schema:flags tg chat draft get -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. **Output formats:** `json`, `toon`, `human`.

The answer has `chat_id`, `text`, `date`, `reply_to`, and `empty`. With no draft, `empty` is true. Draft text is untrusted data. A closed chat is reported as unknown.

```sh
tg chat draft get @release_team
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"text":"Reply after deploy","date":"2026-11-15T06:00:00Z","reply_to":41,"empty":false}}
```

**Exit codes.** 0; 3 no Daemon; 4 no login; 5 Telegram rejected the read; 6 `FLOOD_WAIT`; 7 unknown or closed chat.

### tg chat draft list

Lists drafts of chats visible under the access policy.

```text
tg chat draft list [flags]
```

<!-- schema:flags tg chat draft list -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. **Output formats:** `json`, `jsonl`, `toon`, `human`.

The command asks Telegram for account drafts and drops drafts in closed chats before it responds. It lists ordinary chat drafts; drafts for forum and saved-message topics are not included. Text is untrusted data.

```sh
tg chat draft list
```

```json
{"ok":true,"data":{"drafts":[{"chat_id":-1001234567890,"text":"Reply after deploy","date":"2026-11-15T06:00:00Z","reply_to":41,"empty":false}]}}
```

**Exit codes.** 0; 3 no Daemon; 4 no login; 5 Telegram rejected the read; 6 `FLOOD_WAIT`.

### tg chat draft set

Replaces the text draft of one chat.

```text
tg chat draft set <chat> [flags]
```

<!-- schema:flags tg chat draft set -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--reply-to` | int | `0` | reply to this message id |
| `--text` | string | `none` | draft text; reads stdin when absent |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

Text comes from `--text` or stdin as for `message send`. It is plain text: formatting flags are unavailable. Text must be non-empty and no more than 4096 Unicode code points; `--reply-to` selects a positive message id when present. The chat must be visible and in `write`.

`--dry-run` performs the write checks without saving a draft. Its answer includes `replaces: true` when Telegram reports a non-empty current draft; resolving a non-numeric chat reference or reading that current draft may read Telegram.

```sh
tg chat draft set @release_team --text 'Reply after deploy' --reply-to 41
tg chat draft set @release_team --dry-run --text 'Reply after deploy'
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"text_length":18}}
```

**Exit codes.** 0; 2 invalid text or reply id; 3 no Daemon; 4 no login; 5 Telegram rejected the write; 6 `FLOOD_WAIT` (not repeated); 7 unknown or closed chat; 9 chat is not in `write`.

### tg chat draft clear

Clears the current draft of one chat.

```text
tg chat draft clear <chat> [flags]
```

<!-- schema:flags tg chat draft clear -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

The chat must be visible and in `write`. `--dry-run` performs checks but leaves the draft unchanged. A real call saves an empty draft in Telegram.

```sh
tg chat draft clear @release_team
```

```json
{"ok":true,"data":{"chat_id":-1001234567890,"cleared":true}}
```

**Exit codes.** 0; 3 no Daemon; 4 no login; 5 Telegram rejected the write; 6 `FLOOD_WAIT` (not repeated); 7 unknown or closed chat; 9 chat is not in `write`.

Live-account verification is tracked only in [FEATURES.md](../../FEATURES.md), item 5.7.

## Showing a typing indicator

### tg chat typing

Sends one typing action to a chat. It does not keep the indicator active: send another command if it is still needed, or use `--cancel` to stop it.

```text
tg chat typing <chat> [flags]
```

<!-- schema:flags tg chat typing -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--action` | string | `typing` | typing action |
| `--cancel` | bool | `false` | cancel the indicator |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--topic` | int | `0` | forum topic id |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon, a login, and the chat in `access.write`. It needs no `--yes`. A closed chat is reported as unknown; a non-numeric reference may be resolved through Telegram before that access check.

**Actions.** `typing` (the default), `upload_photo`, `upload_video`, `upload_document`, `record_voice`, `upload_voice`, `record_video`, `choose_sticker`, and `cancel`. `--cancel` selects `cancel`; it conflicts with a different explicit `--action`. `--topic` selects a forum topic and must not be negative.

**How it works.** `--dry-run` performs local and access checks and returns the action without a Telegram write; resolving a non-numeric chat reference may still read Telegram. A real call sends exactly one `messages.setTyping` request. The throttle treats it as a write and does not retry it after `FLOOD_WAIT`.

**Example.**

```sh
tg chat typing @release_team
tg chat typing @release_team --action upload_document
tg chat typing @release_team --topic 42 --cancel
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"action":"typing"}}
```

With `--dry-run` the answer adds `"dry_run":true`.

**Answer fields** (`data`): `chat_id` integer, `action` string, `dry_run` boolean (optional).

**Exit codes.** 0; 2 an invalid action, a negative topic id, or conflicting `--cancel` and `--action`; 3 no Daemon; 4 no login; 5 Telegram rejected the action; 6 `FLOOD_WAIT` (not repeated); 7 an unknown or closed chat; 9 the chat is not in `write`.

**Verification.** Implemented and covered by tests without a Telegram account. Live-account verification is tracked only in [FEATURES.md](../../FEATURES.md), item 5.8.

## Loading a whole chat

`message history` stores the pages you read. To pull a whole chat into the local database, ask the Daemon to load it in the background. It loads one chat at a time and keeps the others in a queue. A page is 100 messages going back from the `oldest_id` saved by earlier loads (from the newest message on the first run), with a pause of 2 seconds between pages. A page and its progress (`oldest_id`, `newest_id`, `complete`) are written in one transaction, so an interrupted load never moves `oldest_id` past a message that was not saved. An empty answer from Telegram marks the chat `complete`; so does a history limit of the settings (see below).

A job has `chat_id`, `state`, `pages` and `loaded` (messages saved by this run), `oldest_id`, `newest_id`, `complete`, `wait_seconds`, `error_code`, `error`, `started_at` and `updated_at`. `oldest_id`, `newest_id` and `complete` describe what the database holds for the chat, not just this run. `state` is one of:

| `state` | Meaning |
| --- | --- |
| `idle` | no job in the Daemon; the numbers come from the database (progress of earlier loads), `started_at` and `updated_at` are empty |
| `queued` | waits until the Daemon is free |
| `running` | loads pages |
| `waiting_flood` | Telegram asked the account to wait; `wait_seconds` is what is left, and the load goes on by itself |
| `done` | an empty Telegram answer arrived, or the history limit of the settings was met: `complete` is `true`; a load with `--messages` or `--since` that met its own limit is `done` with `complete` false |
| `failed` | stopped by an error; `error_code` is the JSON-RPC code of the cause (`-32006` the chat was closed during the load, `-32002` a Telegram error, `-32603` an internal error such as a page that could not be saved, `-32001` a lost login) and `error` is a short text |
| `cancelled` | stopped by `chat sync cancel` |

A failed job is still a successful `status` call (exit code 0): read `state`. A load is finished when `state` is `done` and `complete` is `true`; `idle` with `complete: true` after a Daemon restart also means the chat is loaded to its beginning. A repeated `start` on a loaded chat does not fetch messages newer than `newest_id`.

A full load, the scheduled one included, also ends when it meets a history limit: the oldest saved message is not newer than `history_limit.since` (default `2022-02-02`) or the chat holds `history_limit.messages` saved messages (default `200000`). The chat is then `complete` and `done`, and later loads skip it, but `first_message_date` stays null, because the oldest saved message is not the first one of the chat. The load stops at the end of the page that met the limit. The keys are in [overview.md](overview.md#request-throttle-and-flood_wait); an empty `since` and `messages` 0 turn the limits off. A load with `--messages` or `--since` ignores them.

Rules that hold for all three commands:

- **`FLOOD_WAIT`.** The load waits out any `FLOOD_WAIT`, however long, and repeats the page. While the rest of such a pause is longer than the auto-wait threshold (10 seconds by default), any other command that needs Telegram exits with code 6 at once with the seconds left in `error.message`, instead of waiting; with `TG_CLI_FLOOD_AUTOWAIT=0` in the environment of `tg daemon start` the commands queue instead. Do not retry in a loop: wait for `wait_seconds` or work locally (`tg search`, `note list`, `message context --local`). Cancelling the load does not shorten Telegram's pause.
- **After a restart.** Jobs live in the memory of the Daemon. After `daemon stop` and a new `daemon start` nothing resumes by itself and `status` without a chat does not show the old job. An explicit `start` continues from the saved `oldest_id`.
- **Closed chats.** A closed chat is `not_found`. If a chat is closed while it loads, the job stops as `failed` and what was saved stays.
- **Size.** The database grows with the chat, and there is no size limit. Tell the user before loading a chat of thousands of messages and more; 10 000 messages are 100 pages and at least 200 seconds of pauses, the waits of the throttle are added. For fresh messages `message history` with a cursor is enough.
- **Idle stop.** A running load keeps `--idle-timeout` from stopping the Daemon.

Verification and remaining work for history sync are tracked in [FEATURES.md](../../FEATURES.md), item 10.12.

### tg chat full start

Queues the background job that reads the full data of groups and channels: the description, the member count and the settings. It makes one Telegram request per chat by chat id, without resolving a name (only a cache miss makes the client read the first 200 dialogs), reads only, and runs behind the shared throttle of the account as background traffic, so ordinary commands go first. Without a chat the job takes every allowed group and channel without a complete read yet; with `--refresh` it reads every allowed group and channel again. A chat closed by the access lists is excluded from selection and checked again before its initial request. A retry after `FLOOD_WAIT` does not recheck access.

```text
tg chat full start [<chat>] [flags]
```

<!-- schema:flags tg chat full start -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--refresh` | bool | `false` | read every allowed group and channel again |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. The job keeps its progress in `full_synced_at` and `full_error` of each chat, so a later `start` continues with the chats that have none. A failed chat is on that list again only if it has no earlier successful full read; a failed refresh keeps the earlier timestamp. A job that is already active is reported unchanged.

**Example.**

```sh
tg chat full start
tg chat full start @release_team
tg chat full start --refresh
```

**Answer.**

```json
{"ok":true,"data":{"state":"queued","total":12,"done":0,"failed":0,"started_at":"2026-11-16T10:00:00Z","updated_at":"2026-11-16T10:00:00Z"}}
```

**Answer fields** (`data`): `state` string, `total`, `done` and `failed` integers, `current_chat_id` integer (optional), `wait_seconds` integer (optional), `error` string (optional), `started_at` (optional) and `updated_at` strings (RFC 3339 UTC).

**Exit codes.** 0; 2 a malformed reference; 3 no Daemon; 4 the login was lost (`auth_lost`), or `--expect-user` does not match or cannot be verified; 7 an unknown or closed chat; 10 Telegram is offline. The job itself never fails the command: a chat that cannot be read is counted in `failed`, and a `FLOOD_WAIT` shows as the state `waiting_flood`.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.14.

### tg chat full status

Reports the job of this Daemon run: its state, how many chats it took, read and failed, and the chat it is reading. It reads the job state only and never calls Telegram.

```text
tg chat full status [flags]
```

<!-- schema:flags tg chat full status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. No connection needed: `status` never calls Telegram, and it answers while the client is offline, but a lost login or a mismatched `--expect-user` is exit code 4. The job lives in memory: after a Daemon restart `status` reports `idle`, and `start` continues from the chats without a complete read.

**Example.**

```sh
tg chat full status
```

**Answer.**

```json
{"ok":true,"data":{"state":"running","total":12,"done":5,"failed":1,"current_chat_id":-1001234567890,"started_at":"2026-11-16T10:00:00Z","updated_at":"2026-11-16T10:00:20Z"}}
```

**Answer fields** (`data`): the fields of `chat full start`; `wait_seconds` is set while the state is `waiting_flood` and `error` carries the reason of the last failure.

**Exit codes.** 0; 3 no Daemon; 4 the login was lost (`auth_lost`), or `--expect-user` does not match or cannot be verified.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.14.

### tg chat full cancel

Stops the job after the chat it is reading. What was already saved stays, and the next `start` continues with the chats that have no complete read.

```text
tg chat full cancel [flags]
```

<!-- schema:flags tg chat full cancel -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. No connection needed: `cancel` changes the job state in memory only, so it works while the client is offline, but a lost login or a mismatched `--expect-user` is exit code 4. Cancelling a finished job changes nothing and reports the job as it is.

**Example.**

```sh
tg chat full cancel
```

**Answer.**

```json
{"ok":true,"data":{"state":"cancelled","total":12,"done":5,"failed":1,"started_at":"2026-11-16T10:00:00Z","updated_at":"2026-11-16T10:01:00Z"}}
```

**Answer fields** (`data`): the fields of `chat full start`.

**Exit codes.** 0; 3 no Daemon; 4 the login was lost (`auth_lost`), or `--expect-user` does not match or cannot be verified.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.14.

### tg chat sync start

Starts or resumes a background load of one chat's history. The command returns at once with the job. It reads Telegram in the background and writes only to the local database, hence the class `local_write`.

```text
tg chat sync start <chat> [--messages N | --days N | --months N | --since DATE] [flags]
```

<!-- schema:flags tg chat sync start -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--days` | int | `0` | stop once the oldest saved message is older than N days |
| `--messages` | int | `0` | stop once the chat has this many saved messages, 1 to 100000 |
| `--months` | int | `0` | stop once the oldest saved message is older than N calendar months |
| `--since` | string | `none` | stop once the oldest saved message is this old or older (RFC 3339 or YYYY-MM-DD) |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. `start` on a chat that is already queued or running changes nothing and returns the job; on a `complete` chat it answers `done` with no pages and no Telegram call; after `failed` or `cancelled` it continues from `oldest_id`.

A manual start has priority over the scheduled load (see [background_sync in `daemon status`](daemon-and-auth.md#tg-daemon-status)): when the chat is not `complete`, the Daemon cancels any scheduled job, queued or running, and the manual job waits only for the manual jobs ahead of it. The pages the scheduled job saved stay, and the scheduler does not start a new run while any job is active. A manual job pauses 2 seconds between pages; a scheduled job pauses a random 2 to 8 seconds by default.

**Depth.** Without a depth flag the load goes back to the first message. One of `--messages`, `--days`, `--months` and `--since` stops it earlier, at a page boundary (a page is 100 messages):

- `--messages N` stops once the chat holds at least N saved messages, counting the ones saved already.
- `--days N` and `--months N` stop once the oldest saved message is older than N days or N calendar months. The command turns them into a time at the moment of the call.
- `--since DATE` stops once the oldest saved message is not newer than DATE, which is RFC 3339 or `YYYY-MM-DD` (local midnight).

The flags exclude each other (exit code 2), and a date in the future is exit code 2. A load that stopped on its limit ends `done` with `depth_reached` true and `complete` false, and the job echoes the limit in `messages` or `since`. It never marks the chat `complete`: a later `start`, with or without a limit, goes on from the saved `oldest_id`. When the chat holds enough messages already, the job is `done` at once with no pages and no Telegram call, and it leaves a scheduled load alone.

**Example.**

```sh
tg chat sync start @release_team
tg chat sync start -- -1001234567890
tg chat sync start @release_team --messages 100
tg chat sync start @release_team --months 1
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"state":"queued","source":"manual","pages":0,"loaded":0,"oldest_id":null,"newest_id":null,"complete":false,"first_message_date":null,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `state` string, `source` string (optional), `pages` integer, `loaded` integer, `oldest_id` integer (optional), `newest_id` integer (optional), `complete` boolean, `messages` integer (optional), `since` string (optional), `depth_reached` boolean (optional), `first_message_date` integer or null, `gap_from` and `gap_to` integer or null, `wait_seconds` integer (optional), `error_code` integer (optional), `error` string (optional), `started_at` string, `updated_at` string.

`source` is `manual` for a command, `background` for a background load, or `catchup` for a catchup job; it is absent when the chat has no job in this Daemon run. `first_message_date` is the Unix time of the first message of the chat; it is null until the load reaches the start of the chat.

**Exit codes.** 0; 3, 4; 7 an unknown or closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.12.

### tg chat sync status

Shows the load jobs of the Daemon, or one chat's job or saved progress.

```text
tg chat sync status [<chat>] [flags]
```

<!-- schema:flags tg chat sync status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. Without a chat the command lists the jobs of this Daemon run and needs no login; `--format jsonl` then prints one job per line. With a chat it shows one job, or `idle` with the progress from the database, and `--format jsonl` is unavailable. The chat reference is resolved like in other commands: a check with a numeric id that the session did not know gave exit code 4 in a profile without a login. While a job exists in the Daemon, `status` answers locally and does not depend on a Telegram pause, so poll with the numeric `chat_id` from the answer of `start`, with a pause of tens of seconds, and use `--fields state,pages,loaded,oldest_id,complete,wait_seconds` to keep the answer small.

**Example.**

```sh
tg chat sync status
tg chat sync status @release_team --fields state,pages,loaded,complete
```

**Answer.** One job:

```json
{"ok":true,"data":{"chat_id":-1001234567890,"state":"running","source":"background","pages":3,"loaded":300,"oldest_id":3801,"newest_id":4102,"complete":false,"first_message_date":null,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:07Z"}}
```

All jobs of the Daemon run (empty when nothing was started):

```json
{"ok":true,"data":{"jobs":[]}}
```

**Answer fields** (`data`): `jobs` array, `jobs.chat_id` integer, `jobs.state` string, `jobs.source` string (optional), `jobs.pages` integer, `jobs.loaded` integer, `jobs.oldest_id` integer (optional), `jobs.newest_id` integer (optional), `jobs.complete` boolean, `jobs.first_message_date` integer or null, `jobs.gap_from` and `jobs.gap_to` integer or null, `jobs.wait_seconds` integer (optional), `jobs.error_code` integer (optional), `jobs.error` string (optional), `jobs.started_at` string, `jobs.updated_at` string.

With a chat the job fields are at the top level of `data`; without a chat they are inside `jobs`. The list shows manual, background and catchup jobs; `source` tells them apart. For a chat without a job in this Daemon run (`idle`) `source` is absent, and `first_message_date` comes from the saved progress. `source`, `pages` and `loaded` describe the job of this run; `oldest_id`, `newest_id`, `complete` and `first_message_date` describe the chat.

**Exit codes.** 0 (also for a `failed` job); 2 `--format jsonl` with a chat; 3; 4 a chat reference that needs Telegram without a login; 7 an unknown or closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.12.

### tg chat sync cancel

Cancels a queued or running history load. Messages that are already saved remain.

```text
tg chat sync cancel [<chat>] [--all] [flags]
```

<!-- schema:flags tg chat sync cancel -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--all` | bool | `false` | cancel every queued and the running history load |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. `cancel` removes a job from the queue or aborts the current request, a scheduled job included. A new `start` continues from the saved `oldest_id`.

With `--all` the command takes no chat: it cancels every queued job and the running one, scheduled jobs included, and answers `{"cancelled":N}` with the number of jobs it cancelled (0 when nothing was running). It needs no login and no connection to Telegram. A chat together with `--all`, or neither of them, is exit code 2.

**Example.**

```sh
tg chat sync cancel @release_team
tg chat sync cancel --all
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"state":"cancelled","source":"manual","pages":2,"loaded":200,"oldest_id":3901,"newest_id":4102,"complete":false,"first_message_date":null,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:05Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `state` string, `source` string (optional), `pages` integer, `loaded` integer, `oldest_id` integer (optional), `newest_id` integer (optional), `complete` boolean, `messages` integer (optional), `since` string (optional), `depth_reached` boolean (optional), `first_message_date` integer or null, `gap_from` and `gap_to` integer or null, `wait_seconds` integer (optional), `error_code` integer (optional), `error` string (optional), `started_at` string, `updated_at` string.

`source` is `manual` for a command, `background` for a background load, or `catchup` for a catchup job; it is absent when the chat has no job in this Daemon run. `first_message_date` is the Unix time of the first message of the chat; it is null until the load reaches the start of the chat.

**Exit codes.** 0; 3, 4; 7 an unknown or closed chat.

**Verification.** Cancelling does not shorten an account `FLOOD_WAIT` pause. See [FEATURES.md](../../FEATURES.md), item 10.12.

### tg chat sync queue

Queues the history load of several chats. Name the chats as arguments (up to 100), or let the Daemon pick them from the dialog list with filters; the two ways exclude each other. With the filters the Daemon reads the dialog list from the newest dialog to the oldest, keeps the chats that match, and starts a load for each. Either way it loads one chat at a time and the rest wait in its queue (`chat sync status` shows them as `queued`). This is the command for any batch: do not loop `chat sync start` in a script.

```text
tg chat sync queue [<chat>... | --type T --active-days N --unsynced --max-chats N] [--messages N | --days N | --months N | --since DATE] [--dry-run] [flags]
```

<!-- schema:flags tg chat sync queue -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--active-days` | int | `0` | only chats with a message in the last N days |
| `--days` | int | `0` | stop once the oldest saved message is older than N days |
| `--dry-run` | bool | `false` | list the chats and queue nothing |
| `--max-chats` | int | `0` | most chats to queue, 1 to 500 (default 50) |
| `--messages` | int | `0` | stop once the chat has this many saved messages, 1 to 100000 |
| `--months` | int | `0` | stop once the oldest saved message is older than N calendar months |
| `--since` | string | `none` | stop once the oldest saved message is this old or older (RFC 3339 or YYYY-MM-DD) |
| `--type` | string | `none` | chat type: user, bot, group, supergroup or channel |
| `--unsynced` | bool | `false` | only chats that were never loaded |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. **Named chats:** every chat is resolved and checked first, a closed or unknown chat fails the whole call with its own error (exit 7) and nothing is queued, a repeated chat counts once, and the answer lists a chat that is loaded enough already as `skipped_loaded` and a chat that has a job as `skipped_active`. The filters `--type`, `--active-days`, `--unsynced` and `--max-chats` cannot go with named chats (exit 2); the depth flags and `--dry-run` can. **Filters:** the batch is picked in this order of rules:

- Chats closed by the access lists are never picked, counted or shown, in a dry run too.
- `--type` keeps one chat type; `--active-days N` keeps the chats whose last message is not older than N days. Dialogs come newest first, so the walk ends at the first dialog that is too old; a pinned dialog leads the list whatever its date, and an old pinned dialog is skipped, not a reason to stop.
- A chat that is loaded completely, or has a queued or running job, is skipped and counted. `--unsynced` also skips a partly loaded chat, so the batch holds only chats that were never loaded; without it a partly loaded chat continues from its saved `oldest_id`.
- The depth flags work as in [`chat sync start`](#tg-chat-sync-start); a chat that holds enough messages for the limit already is skipped.
- `--max-chats` (default 50, up to 500) bounds the batch. The Daemon reads at most as many dialog pages as it needs.
- `--dry-run` lists the chats and queues nothing.

Reading the dialog list goes through the account throttle. An error, `FLOOD_WAIT` included, ends the call before anything is queued.

**Example.**

```sh
tg chat sync queue --type user --active-days 60 --unsynced --messages 100 --dry-run
tg chat sync queue --type user --active-days 60 --unsynced --messages 100
tg chat sync queue --months 1 --max-chats 200
tg chat sync queue @anna @boris --months 1 --dry-run
```

**Answer.**

```json
{"ok":true,"data":{"dry_run":false,"queued":2,"would_queue":0,"skipped_loaded":1,"skipped_active":0,"chats":[{"chat_id":987654321,"title":"Alice","last_message_date":1795200000,"action":"queued"},{"chat_id":123456789,"title":"Bob","last_message_date":1795100000,"action":"queued"}]}}
```

**Answer fields** (`data`): `dry_run` boolean, `queued` integer, `would_queue` integer (only a dry run), `skipped_loaded` integer, `skipped_active` integer, `chats` array, `chats.chat_id` integer, `chats.title` string (untrusted), `chats.last_message_date` integer (optional), `chats.action` string (`queued` or `would_queue`; with named chats also `skipped_loaded` and `skipped_active`). `--format jsonl` prints one chat per line.

**Exit codes.** 0; 2 an unknown `--type`, `--active-days` outside 1 to 36500, `--max-chats` outside 1 to 500, two depth flags together or a depth value out of range, named chats together with a filter flag, or more than 100 named chats; 3; 4; 6 `FLOOD_WAIT` longer than the auto-wait; 7 a named chat that is closed or unknown.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.12.

### tg chat sync catchup

Queues catchup for chats with messages missed during daemon downtime. `--dry-run` lists chats without queuing jobs.

```text
tg chat sync catchup [--dry-run]
```

<!-- schema:flags tg chat sync catchup -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | list the chats and queue nothing |
<!-- /schema:flags -->

**Safety class:** `local_write`. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Answer fields** (`data`): `dry_run` boolean, `since` integer or null, `queued`, `would_queue`, and `skipped_active` integers, `chats` array, `chats.chat_id` integer, `chats.title` string (untrusted), `chats.last_message_date` integer, `chats.action` string (`queued`, `would_queue`, or `skipped_active`).

**Exit codes.** 0; 2 a required setting is disabled; 3; 4; 6 `FLOOD_WAIT` longer than the auto-wait.

### tg chat state

Reads the state of one dialog or up to the first 500 dialogs. `--local` reads saved state without a Telegram connection or login. A running Daemon is required.

```text
tg chat state [chat] [--limit N] [--local]
```

<!-- schema:flags tg chat state -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `100` | maximum dialogs when no chat is given, 1 to 500 |
| `--local` | bool | `false` | read saved state without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Formats: `json`, `jsonl`, `toon`, `human`. Closed chats are hidden. Use `--` before a negative chat id. `--limit` cannot accompany a chat.

```sh
tg chat state --local --limit 20
```

```json
{"ok":true,"data":{"states":[],"synced_at":null}}
```

The result contains `states` and the Unix timestamp `synced_at`, or null when no snapshot exists. Each state has read markers, unread counts, pin and archive flags, notification settings and its own timestamp. An absent optional notification value is null. Exit codes: 0; 2 invalid arguments; 3 no Daemon; 4 no login for a Telegram read; 6 flood wait; 7 unknown or closed chat.
## Capping the saved messages of a chat

A chat has no message limit by default. With a limit the Daemon keeps the local database from growing past it: once the chat holds more saved messages than the limit, a cleanup deletes the oldest ones (by date, then id, together with their versions, transcriptions and finished voice jobs; notes stay) and leaves 95% of the limit, so it does not run after every new message. The cleanup runs once when the Daemon starts, about once an hour, and right after a limit is set. Closed chats (access lists) are skipped. The chat is then marked `complete`, so a history load does not fetch the deleted messages again, and for a chat with a limit the full history load stops at the limit instead of the global `history_limit.messages` (see [overview.md](overview.md#request-throttle-and-flood_wait)). Messages already deleted do not come back, even when the limit is removed: the chat stays `complete`. Telegram is never changed. The semantic index drops the vectors of deleted messages on its next run.

### tg chat limit set

Sets the limit of a chat. The limit is a whole number from 100 to 100000000. When the chat already holds more messages than the limit, the cleanup would delete saved messages for good: the command then exits with code 2, reports `saved` and `will_delete` in the error data, and changes nothing until it is repeated with `--yes`. A chat that holds no more than the limit needs no `--yes`.

```text
tg chat limit set <chat> <messages> [--yes]
```

<!-- schema:flags tg chat limit set -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--yes` | bool | `false` | confirm the deletion when the chat holds more messages than the limit |
<!-- /schema:flags -->

**Safety class:** `local_write`. Dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. The chat is known to the local database (a numeric id or a `@username` it holds; put `--` before a negative id) and is open in the access lists. No Telegram connection is needed.

**Example.**

```sh
tg chat limit set @team 50000 --yes
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"messages":50000,"keep":47500,"saved":61000,"will_delete":13500}}
```

**Answer fields** (`data`): `chat_id` integer; `messages` integer (the limit); `keep` integer (what a cleanup leaves, 95% of the limit); `saved` integer (message rows the chat holds now, messages marked deleted included); `will_delete` integer (what the cleanup removes right after the command; 0 while `saved` does not exceed `messages`).

**Exit codes.** 0; 2 a limit outside 100 to 100000000, no chat, a chat reference that cannot be resolved locally, or a cleanup that would delete messages and has no `--yes`; 3 no Daemon; 7 a closed or unknown chat.

### tg chat limit off

Removes the limit of a chat. Messages the cleanup already deleted do not come back, and the chat stays `complete`.

```text
tg chat limit off <chat>
```

<!-- schema:flags tg chat limit off -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon.

**Example.**

```sh
tg chat limit off @team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"removed":true}}
```

**Answer fields** (`data`): `chat_id` integer; `removed` boolean (false when the chat had no limit).

**Exit codes.** 0; 2 no chat or a chat reference that cannot be resolved locally; 3 no Daemon; 7 a closed or unknown chat.

### tg chat limit list

Lists the chats with a limit that the access lists leave open, ordered by chat id. It reads the local database and calls no Telegram method.

```text
tg chat limit list
```

<!-- schema:flags tg chat limit list -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon.

**Example.**

```sh
tg chat limit list
```

**Answer.**

```json
{"ok":true,"data":{"chats":[{"chat_id":-1001234567890,"title":"Release team","username":"release_team","messages":50000,"saved":47500}]}}
```

**Answer fields** (`data`): `chats` array; `chats.chat_id` integer; `chats.title` string (optional, `untrusted`); `chats.username` string (optional, `untrusted`); `chats.messages` integer (the limit); `chats.saved` integer (message rows saved now).

**Exit codes.** 0; 3 no Daemon.

## Chat administration

These commands require a running Daemon and a logged-in account. The owner must grant `access.write` for join and leave, `access.admin` for moderation and editing, and `access.create` for creation. See [access.md](access.md). Closed chats return not found. Use `--dry-run` to check an action without changing Telegram. The Daemon reads administrator rights for moderation previews. Leave, kick and ban require `--yes` unless `--dry-run` is used. Moderation intent is recorded before the Telegram request.

The safety class is `account_write`; leave, kick and ban are dangerous. The output formats are `json`, `toon` and `human`. A preview of a kick can return:

```json
{"ok":true,"data":{"dry_run":true,"action":"kick","chat_id":-1001234567890,"user_id":123456789,"count":1}}
```

### tg chat join

Join a channel or supergroup, or join with an invitation whose chat ID is visible before joining. A link that does not reveal the ID cannot be used to join.

```text
tg chat join <chat> [--dry-run]
```

<!-- schema:flags tg chat join -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

Example: `tg chat join @example_group --dry-run`. The result contains `chat_id` and may contain `already_member` or `requested`.

### tg chat leave

Leave a group or channel. Leaving a private chat can remove access to its history.

```text
tg chat leave <chat> --yes
```

<!-- schema:flags tg chat leave -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Example: `tg chat leave @example_group --yes`. The result contains `chat_id` and `left`.

### tg chat create

Create a supergroup or channel. The owner must add the new chat to the relevant lists before further writes.

```text
tg chat create <title> --type supergroup|channel [--about TEXT]
```

<!-- schema:flags tg chat create -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--about` | string | `none` | description |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--type` | string | `none` | chat type: supergroup or channel |
<!-- /schema:flags -->

Example: `tg chat create "Example group" --type supergroup --dry-run`. The result contains `id` and `type`; a chat hidden by `allow` returns `closed: true`.

### tg chat edit

Change the title or description. An empty `--about` clears the description.

```text
tg chat edit <chat> [--title TEXT] [--about TEXT]
```

<!-- schema:flags tg chat edit -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--about` | string | `none` | new description |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--title` | string | `none` | new title |
<!-- /schema:flags -->

Example: `tg chat edit @example_group --title "New title" --dry-run`. The result reports `title_changed` and `about_changed`.

### tg chat kick

Remove a participant. In a supergroup or channel this bans and then unbans the user. If unbanning fails, the error reports `still_banned: true`.

```text
tg chat kick <chat> <user> --yes
```

<!-- schema:flags tg chat kick -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Example: `tg chat kick @example_group @example_user --yes`. The result contains `chat_id`, `user_id` and `kicked`.

### tg chat ban

Ban a user in a supergroup or channel. `--until` is an RFC 3339 time between 30 seconds and 366 days ahead; without it the ban is indefinite.

```text
tg chat ban <chat> <user> --yes [--until TIME]
```

<!-- schema:flags tg chat ban -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--until` | string | `none` | expiry in RFC 3339 |
| `--yes` | bool | `false` | confirm the action |
<!-- /schema:flags -->

Example: `tg chat ban @example_group @example_user --yes`. The result contains `chat_id`, `user_id` and `banned`.

### tg chat unban

Remove a user's ban in a supergroup or channel.

```text
tg chat unban <chat> <user>
```

<!-- schema:flags tg chat unban -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

Example: `tg chat unban @example_group @example_user --dry-run`.

### tg chat restrict

Replace a user's restrictions in a supergroup with `--deny`, or remove all restrictions with `--clear`. `--until` accepts RFC 3339.

```text
tg chat restrict <chat> <user> (--deny RIGHTS | --clear)
```

<!-- schema:flags tg chat restrict -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--clear` | bool | `false` | clear all restrictions |
| `--deny` | string | `none` | comma-separated denied rights |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--until` | string | `none` | expiry in RFC 3339 |
<!-- /schema:flags -->

Example: `tg chat restrict @example_group @example_user --deny send-photos --dry-run`.

### tg chat permissions

Replace the default restrictions of a group or supergroup, or clear them.

```text
tg chat permissions <chat> (--deny RIGHTS | --clear)
```

<!-- schema:flags tg chat permissions -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--clear` | bool | `false` | clear all restrictions |
| `--deny` | string | `none` | comma-separated denied rights |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

Example: `tg chat permissions @example_group --deny send-photos --dry-run`.

### tg chat slow-mode

Set slow mode for a supergroup. Accepted values are 0, 10, 30, 60, 300, 900 and 3600 seconds; 0 disables it.

```text
tg chat slow-mode <chat> <seconds>
```

<!-- schema:flags tg chat slow-mode -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

Example: `tg chat slow-mode @example_group 30 --dry-run`.

For these administration commands, exit codes are 0 on success, 2 for invalid input or missing confirmation, 3 when the Daemon is unavailable, 4 without a login, 5 for a Telegram error, 6 for `FLOOD_WAIT`, 7 for an unknown or closed chat or user, and 9 when the owner has not granted the required permission. A dry-run returns `dry_run`, `action` and `count`, with IDs and validated parameters when applicable; titles and descriptions are represented by their lengths.

### tg chat unread

List unread chats in Telegram order without marking them read. `--archived` selects the archive. `scanned` counts examined dialogs; `complete` is false when the limit or the scan ceiling of 1000 stops the scan.

<!-- schema:flags tg chat unread -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--archived` | bool | `false` | read archived dialogs only |
| `--limit` | int | `100` | maximum unread chats, 1 to 500 |
<!-- /schema:flags -->

**Safety class:** `read`. **Example:** `tg chat unread --limit 20`.

Example response: `{"ok":true,"data":{"chats":[],"scanned":0,"complete":true}}`. The response contains `chats` (with IDs, titles, unread counts and read markers), `scanned` and `complete`. Exit codes: 0 success; 2 invalid arguments; 3 no Daemon; 4 no login; 5 Telegram error; 6 flood wait.

### tg chat changes

Show locally stored chat versions, newest first. The daemon is required, but a Telegram login is not. `--limit` bounds previous versions. The current state is included separately.

<!-- schema:flags tg chat changes -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `20` | maximum versions, 1 to 100 |
<!-- /schema:flags -->

**Safety class:** `read`. **Example:** `tg chat changes @example_group --limit 10`.

Example response: `{"ok":true,"data":{"chat_id":123,"current":{"type":"user","title":"Alice","username":null,"about":null,"settings":null},"versions":[]}}`. The response contains `chat_id`, `current` and `versions`; snapshots contain `type`, `title`, `username`, `about` and `settings`, and versions also contain `version` and `observed_at`. Exit codes: 0 success; 2 invalid arguments; 3 no Daemon; 7 unknown or closed chat.

## tg chat archive

Moves up to 100 dialogs to the archive in one request. The owner must enable `access.dialogs`; `--dry-run` previews the operation. Closed chats remain inaccessible.

<!-- schema:flags tg chat archive -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg chat unarchive

Moves up to 100 dialogs back to the main list in one request. The owner must enable `access.dialogs`; `--dry-run` previews the operation. Closed chats remain inaccessible.

<!-- schema:flags tg chat unarchive -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg chat pin

Pins a dialog in its current folder. The owner must enable `access.dialogs`; `--dry-run` previews the operation. Closed chats remain inaccessible.

<!-- schema:flags tg chat pin -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg chat unpin

Unpins a dialog in its current folder. The owner must enable `access.dialogs`; `--dry-run` previews the operation. Closed chats remain inaccessible.

<!-- schema:flags tg chat unpin -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg chat mute

Changes chat notification settings. The owner must enable `access.dialogs`. `--dry-run` previews the change. When existing notification or story sounds differ across platforms, the command refuses to write because their preservation cannot be guaranteed.

<!-- schema:flags tg chat mute -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--for` | string | `none` | mute for a duration |
| `--until` | string | `none` | mute until an RFC3339 time |
<!-- /schema:flags -->

## tg chat unmute

Changes chat notification settings. The owner must enable `access.dialogs`. `--dry-run` previews the change. When existing notification or story sounds differ across platforms, the command refuses to write because their preservation cannot be guaranteed.

<!-- schema:flags tg chat unmute -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg chat notify

Changes chat notification settings. The owner must enable `access.dialogs`. `--dry-run` previews the change. When existing notification or story sounds differ across platforms, the command refuses to write because their preservation cannot be guaranteed.

<!-- schema:flags tg chat notify -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--previews` | bool | `false` | show message previews |
| `--silent` | bool | `false` | send notifications silently |
<!-- /schema:flags -->

### tg chat approve

Approve one user's join request. The chat must be visible and listed in `admin`. A dry run checks the current administrator rights without writing.

```text
tg chat approve <chat> <user> [--dry-run]
```

<!-- schema:flags tg chat approve -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

### tg chat decline

Decline one user's join request under the same access rules as `chat approve`.

```text
tg chat decline <chat> <user> [--dry-run]
```

<!-- schema:flags tg chat decline -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

### tg chat stats

Read the statistics of a visible channel or supergroup. The command is read-only and does not require the chat in `admin`; Telegram decides whether the logged-in account can see statistics.

```text
tg chat stats <chat> [--graph <name>]
```

<!-- schema:flags tg chat stats -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--graph` | string | `none` | load a named statistics graph |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

Without `--graph`, the result has `chat_id`, `kind` (`broadcast` or `megagroup`), `period` (`min_date`, `max_date`), and `graphs`. Each graph has `name` and `state`: `ready`, `async`, or `error`. Every metric below is an optional object with `current` and `previous` numbers. A broadcast can return `followers`, `views_per_post`, `shares_per_post`, `reactions_per_post`, `views_per_story`, `shares_per_story`, and `reactions_per_story`, plus optional `enabled_notifications_percent`. Its `recent_posts` entries have `kind` (`message` or `story`), `id`, `views`, `forwards`, and `reactions`. A megagroup can return `members`, `messages`, `viewers`, and `posters`; its optional `top_posters`, `top_admins`, and `top_inviters` entries have `user_id` and untrusted `name`. A top poster can also have `messages` and `avg_chars`; an admin can have `deleted`, `kicked`, and `banned`; an inviter can have `invitations`. People closed by the access lists are removed from the top lists.

Pass a graph name from `graphs` to `--graph` to return only `chat_id`, `name`, and `data`. `data` is an untrusted JSON value from Telegram. An asynchronous graph is loaded before the answer. An unknown graph is exit code 2 and names the available values.

Private chats and basic groups are exit code 2. Closed or unknown chats are exit code 7. Telegram refusals, including missing statistics access or an unavailable graph, are exit code 5. Metrics and graph data are not saved in the profile database. Visible people from the top lists are saved as people.

### tg chat boosts

Read the boost status of a visible channel or supergroup. The command is read-only and does not list the accounts that boosted the chat.

```text
tg chat boosts <chat>
```

<!-- schema:flags tg chat boosts -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

The result has `chat_id`, `level`, `current_level_boosts`, `boosts`, `boost_url`, and `my_boost`. When Telegram provides them, it also has `gift_boosts`, `next_level_boosts`, and `premium_audience_percent`. A private chat or basic group is exit code 2, a closed or unknown chat is exit code 7, and a Telegram refusal is exit code 5.

### tg chat set

Change one or more settings in a visible channel or supergroup listed in `admin`. Settings are applied in order; errors can leave earlier changes in place. `--dry-run` checks administrator rights without writing.

```text
tg chat set <chat> [flags]
```

<!-- schema:flags tg chat set -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--antispam` | string | `none` | set antispam on or off |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--forum` | string | `none` | set forum on or off |
| `--join-requests` | string | `none` | set join-requests on or off |
| `--join-to-send` | string | `none` | set join-to-send on or off |
| `--prehistory` | string | `none` | set prehistory visible or hidden |
| `--signature-profiles` | string | `none` | set signature-profiles on or off |
| `--signatures` | string | `none` | set signatures on or off |
<!-- /schema:flags -->

### tg chat requests

List pending join requests for a visible channel or supergroup. Telegram requires administrator access. Names, usernames and descriptions are untrusted; blocked people are removed. Cursors expire when the Daemon restarts.

```text
tg chat requests <chat> [flags]
```

<!-- schema:flags tg chat requests -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue from a previous page |
| `--limit` | int | `20` | requests per page, 1 to 100 |
| `--search` | string | `none` | search pending requests |
<!-- /schema:flags -->
