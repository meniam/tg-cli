# Chats

Commands that find, describe and list chats, read their members and topics, mark a chat read, and load a whole chat history into the local database. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data.

## Rules shared by chat commands

- **Preconditions.** A running Daemon and a login, except where a command says otherwise. Without a Daemon the exit code is 3, without a login 4.
- **Chat reference.** `<chat>` is `@username`, a Bot API id (users positive, groups negative, channels from `-1000000000000`), the phone number of a contact, or a `t.me` link, see [Addressing a chat](overview.md#addressing-a-chat). Put `--` before a negative id and the flags before `--`.
- **Closed chats.** When `access.json` closes a chat, every command of this page treats it as a chat that does not exist: exit code 7 with the same message, and no request for its data goes to Telegram. For a numeric id that the session does not know the message is `not found: this session does not know that id; use @username, or read the chat list first`; for every other reference it is `not found: the chat or message does not exist or is not visible to this account`. For an `@username`, a phone number or a `t.me` link the Daemon still resolves the reference in Telegram first and checks the id afterwards, so such a request reaches Telegram. See [access.md](access.md).
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
| `--limit` | int | `50` | maximum number of chats, 1 to 500 |
| `--type` | string | `none` | only chats of this type: user, bot, group, supergroup, channel |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Chats closed by the access lists are absent. The list is topped up from Telegram until `--limit`, until the dialogs run out or until 500 dialogs have been requested, so with many closed chats it can be shorter than `--limit`.

**Example.**

```sh
tg chat list --limit 20 --type group
tg chat list --limit 2 --format jsonl
```

**Answer.**

```json
{"ok":true,"data":{"chats":[{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team","unread":3},{"id":987654321,"type":"user","title":"Alice","unread":0}]}}
```

**Answer fields** (`data`): `chats` array, `chats.id` integer, `chats.type` string, `chats.title` string (untrusted), `chats.username` string (optional, untrusted), `chats.unread` integer.

**Exit codes.** 0; 2 `--limit` below 0 or above 500, or an unknown `--type`; 3 no Daemon; 4 no login; 6 `FLOOD_WAIT` longer than the auto-wait.

**Limits and live check.** Checked on a live account (`chat list --limit 5`, `--type`, `--format jsonl`). The count of unread messages is in `unread`, the unread messages themselves and the unread topics are not available. `--limit 0` is accepted and counts as the default 50; a negative value and a value above 500 are exit code 2 ([FEATURES.md](../../FEATURES.md) 7.1.4). Per-method throttle buckets are an open item: `getDialogs` hits `FLOOD_WAIT` before the general bucket, so keep pauses between live reads.

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

**Limits and live check.** Checked on a live account for `@username` and numeric ids of a user, a bot, a group, a supergroup and a channel. A phone number from the contacts was not checked.

### tg chat info

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
{"ok":true,"data":{"id":-1001234567890,"type":"supergroup","title":"Release team","username":"release_team","about":"Deploys and releases","members":42,"forum":false,"verified":false}}
```

**Answer fields** (`data`): `id` integer, `type` string, `title` string (untrusted), `username` string (optional, untrusted), `about` string (optional, untrusted), `members` integer (optional), `forum` boolean (optional), `verified` boolean (optional).

`forum` tells whether the chat has topics, which `chat topics` needs.

**Exit codes.** 0; 2 a malformed reference; 3, 4; 7 the chat does not exist or is not visible.

**Limits and live check.** Checked on a live account for `@username` and numeric ids. The description, the member count and the forum and verified flags are saved to the database as they are read. Chat settings of the owner (archive, pin, sound, folder) are not saved.

### tg chat invite-info

Shows what an invite link leads to, without joining.

```text
tg chat invite-info <link> [flags]
```

<!-- schema:flags tg chat invite-info -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Invitations are closed while `allow` in `access.json` is nonempty, because an invite has no known chat id before it is resolved: the command then answers exit code 7 with the same `not found` message as for an unknown chat.

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

**Limits and live check.** Not checked on a live account for a real group: only a nonexistent link was run (exit code 7).

## Members, topics and common chats

Three read commands that ask Telegram, so they need a login and a running Daemon. They write nothing to Telegram. The people from `chat members` and the user of `chat common` are saved to `people` (id, name, username, bot flag; the role and the join time are not saved), and the chats of `chat common` to `chats`. The tables grow by the pages you ask for: there is no walk over a whole chat, and a database write error does not break the answer. Not done: managing members, a user profile, membership history, downloading all members of a chat, and reading the messages of a topic by these commands (`message history --topic` does that).

A page can be continued in two ways. Members page by `--offset` and `next_offset`. Topics and common chats page by `--cursor` and `next_cursor`: a cursor is opaque and belongs to one chat or user, and a cursor of another chat, another user or a damaged one is exit code 2.

### tg chat members

Lists the members of a group, supergroup or channel.

```text
tg chat members <chat> [flags]
```

<!-- schema:flags tg chat members -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--filter` | string | `none` | select recent members, admins or bots |
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

**Limits and live check.** Checked on a live account on 2026-11-14, read-only: members of a basic group without and with `--search`, `--offset` and `--filter admins`, members of one supergroup, and four channels without administrator rights (all four answered exit code 5, so an administrator-less channel is an error, not an empty list). Not checked: the member list of a channel for an administrator, the page size limit and the offset on a large supergroup.

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

**Limits and live check.** Checked on a live forum of 16 topics with `--limit` 5, 3, 1 and 16: every topic came exactly once. The error for a chat that is not a forum was checked too.

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

**Limits and live check.** Checked on a live account: the shared chats of one user, where a closed chat (a temporary `access.json` with `block`) was not in the answer, and the saving of the user. Not checked: continuing by `next_cursor` and the page size limit of Telegram (how it pages by the id of the last chat).

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


**Preconditions.** A running Daemon, a login, and the chat in the `write` list of `access.json` (the same rule as for `message send`, see [messages-write.md](messages-write.md#writing-is-off-until-the-owner-allows-it)). It needs no `--yes`. By how Telegram works, the other person sees the read mark, so use it only on request.

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

**Limits and live check.** Checked on a live account by exit codes only: no `write` gave 9, `--dry-run`, a write in the test chat, another chat gave 9. What the other side sees after the call was not checked.

## Loading a whole chat

`message history` stores the pages you read. To pull a whole chat into the local database, ask the Daemon to load it in the background. It loads one chat at a time and keeps the others in a queue. A page is 100 messages going back from the `oldest_id` saved by earlier loads (from the newest message on the first run), with a pause of 2 seconds between pages. A page and its progress (`oldest_id`, `newest_id`, `complete`) are written in one transaction, so an interrupted load never moves `oldest_id` past a message that was not saved. Only an empty answer from Telegram marks the chat `complete`.

A job has `chat_id`, `state`, `pages` and `loaded` (messages saved by this run), `oldest_id`, `newest_id`, `complete`, `wait_seconds`, `error_code`, `error`, `started_at` and `updated_at`. `oldest_id`, `newest_id` and `complete` describe what the database holds for the chat, not just this run. `state` is one of:

| `state` | Meaning |
| --- | --- |
| `idle` | no job in the Daemon; the numbers come from the database (progress of earlier loads), `started_at` and `updated_at` are empty |
| `queued` | waits until the Daemon is free |
| `running` | loads pages |
| `waiting_flood` | Telegram asked the account to wait; `wait_seconds` is what is left, and the load goes on by itself |
| `done` | an empty Telegram answer arrived, `complete` is `true` |
| `failed` | stopped by an error; `error_code` is the JSON-RPC code of the cause (`-32006` the chat was closed during the load, `-32002` a Telegram error, `-32603` an internal error such as a page that could not be saved, `-32001` a lost login) and `error` is a short text |
| `cancelled` | stopped by `chat sync cancel` |

A failed job is still a successful `status` call (exit code 0): read `state`. A load is finished when `state` is `done` and `complete` is `true`; `idle` with `complete: true` after a Daemon restart also means the chat is loaded to its beginning. A repeated `start` on a loaded chat does not fetch messages newer than `newest_id`.

Rules that hold for all three commands:

- **`FLOOD_WAIT`.** The load waits out any `FLOOD_WAIT`, however long, and repeats the page. While the rest of such a pause is longer than the auto-wait threshold (10 seconds by default), any other command that needs Telegram exits with code 6 at once with the seconds left in `error.message`, instead of waiting; with `TG_CLI_FLOOD_AUTOWAIT=0` in the environment of `tg daemon start` the commands queue instead. Do not retry in a loop: wait for `wait_seconds` or work locally (`tg search`, `note list`, `message context --local`). Cancelling the load does not shorten Telegram's pause.
- **After a restart.** Jobs live in the memory of the Daemon. After `daemon stop` and a new `daemon start` nothing resumes by itself and `status` without a chat does not show the old job. An explicit `start` continues from the saved `oldest_id`.
- **Closed chats.** A closed chat is `not_found`. If a chat is closed while it loads, the job stops as `failed` and what was saved stays.
- **Size.** The database grows with the chat, and there is no size limit. Tell the user before loading a chat of thousands of messages and more; 10 000 messages are 100 pages and at least 200 seconds of pauses, the waits of the throttle are added. For fresh messages `message history` with a cursor is enough.
- **Idle stop.** A running load keeps `--idle-timeout` from stopping the Daemon.

Checked on a live account on 2026-11-14 (message texts were not read): a group of 215 messages loaded in 4 pages (the last one empty) in 7 seconds and became `complete`; a repeated `start` on it answered `done` at once, with no pages and no Telegram call. A bot chat was cancelled after 2 pages (200 messages), then `start` resumed from the saved `oldest_id` and loaded 2897 more messages in 30 pages in 63 seconds, about 2.1 seconds per page, with no overlap and no duplicates (3097 messages in total). `status` showed `queued`, `running`, `done` and `cancelled`. Not checked on a live account: `FLOOD_WAIT` during a load (it did not happen) and the exit code 6 of other commands in that pause, a closed chat during a load, a stop of the Daemon during a load, a channel or a forum, a chat of tens of thousands of messages, and a load that runs while the live event stream writes the same chat. Not done: page size and pause as flags, loading several chats with one command, a schedule or an automatic start, detecting deleted messages, loading media, folders.

### tg chat sync start

Starts or resumes a background load of one chat's history. The command returns at once with the job. It reads Telegram in the background and writes only to the local database, hence the class `local_write`.

```text
tg chat sync start <chat> [flags]
```

<!-- schema:flags tg chat sync start -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. `start` on a chat that is already queued or running changes nothing and returns the job; on a `complete` chat it answers `done` with no pages and no Telegram call; after `failed` or `cancelled` it continues from `oldest_id`.

**Example.**

```sh
tg chat sync start @release_team
tg chat sync start -- -1001234567890
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"state":"queued","pages":0,"loaded":0,"complete":false,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:00Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `state` string, `pages` integer, `loaded` integer, `oldest_id` integer (optional), `newest_id` integer (optional), `complete` boolean, `wait_seconds` integer (optional), `error_code` integer (optional), `error` string (optional), `started_at` string, `updated_at` string.

**Exit codes.** 0; 3, 4; 7 an unknown or closed chat.

**Limits and live check.** See the checks above.

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
{"ok":true,"data":{"chat_id":-1001234567890,"state":"running","pages":3,"loaded":300,"oldest_id":3801,"newest_id":4102,"complete":false,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:07Z"}}
```

All jobs of the Daemon run (empty when nothing was started):

```json
{"ok":true,"data":{"jobs":[]}}
```

**Answer fields** (`data`): `jobs` array, `jobs.chat_id` integer, `jobs.state` string, `jobs.pages` integer, `jobs.loaded` integer, `jobs.oldest_id` integer (optional), `jobs.newest_id` integer (optional), `jobs.complete` boolean, `jobs.wait_seconds` integer (optional), `jobs.error_code` integer (optional), `jobs.error` string (optional), `jobs.started_at` string, `jobs.updated_at` string.

With a chat the job fields are at the top level of `data`; without a chat they are inside `jobs`.

**Exit codes.** 0 (also for a `failed` job); 2 `--format jsonl` with a chat; 3; 4 a chat reference that needs Telegram without a login; 7 an unknown or closed chat.

**Limits and live check.** `status` showed `queued`, `running`, `done` and `cancelled` on a live account. The states `waiting_flood` and `failed` were checked by tests only.

### tg chat sync cancel

Cancels a queued or running history load. Messages that are already saved remain.

```text
tg chat sync cancel <chat> [flags]
```

<!-- schema:flags tg chat sync cancel -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login. Put `--` before a negative chat id. `cancel` removes a job from the queue or aborts the current request. A new `start` continues from the saved `oldest_id`.

**Example.**

```sh
tg chat sync cancel @release_team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"state":"cancelled","pages":2,"loaded":200,"oldest_id":3901,"newest_id":4102,"complete":false,"started_at":"2026-11-14T10:00:00Z","updated_at":"2026-11-14T10:00:05Z"}}
```

**Answer fields** (`data`): `chat_id` integer, `state` string, `pages` integer, `loaded` integer, `oldest_id` integer (optional), `newest_id` integer (optional), `complete` boolean, `wait_seconds` integer (optional), `error_code` integer (optional), `error` string (optional), `started_at` string, `updated_at` string.

**Exit codes.** 0; 3, 4; 7 an unknown or closed chat.

**Limits and live check.** A bot chat was cancelled after 2 pages on a live account and resumed without overlap. Cancelling does not shorten a `FLOOD_WAIT` pause of the account.
