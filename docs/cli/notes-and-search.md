# Notes, local search and the tool description

Commands that work on the local database of the profile, and the two commands that describe the tool: `note add|list|delete`, `search`, `version` and `schema`. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md). All examples use fictional data.

The local database is one SQLite file per profile (`<profile>/store.db`, mode `0600`, plain text). The Daemon saves every chat, person and message it reads from Telegram into it, and full-text search (FTS5) works on it without a Telegram connection. Search text is lower-cased and `ё` is folded into `е`, so case and `ё` do not matter. The driver is `modernc.org/sqlite` (pure Go, FTS5, no cgo). The database holds other people's conversations, protected only by the file mode and the disk encryption of your machine.

## Notes

A note is a text that the owner or the AI agent attaches to a chat, a person or a message. Notes live apart from Telegram data: a reload of chats and messages never changes or deletes them, they survive a Daemon restart, and the AI writes them without the confirmation of the owner. A note has an author (`owner` or `ai`), the model name when the author is `ai`, tags, and creation and edit times. There is no importance field yet. The commands need a running Daemon but no login (the one exception is `note add` for an `@username` the database has never seen).

When the access lists close a chat, its notes are hidden: `note list` does not show them, `note add` for it gives exit code 7 `not_found`, and `note delete` of a note of such a chat gives the same `not_found` as a note that does not exist.

### tg note add

Adds a note to a chat, a person or a message.

```text
tg note add [flags] <body|->
```

<!-- schema:flags tg note add -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `owner` | note author: owner or ai |
| `--chat` | string | `none` | chat reference for a chat or message note |
| `--message` | int64 | `0` | message id when --chat identifies a message note |
| `--model` | string | `none` | AI model name, only with --author ai |
| `--person` | string | `none` | person reference for a person note |
| `--tag` | stringArray | `[]` | tag to add; repeat for multiple tags |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. Choose the target: `--chat` for a chat note, `--person` for a person note, or `--chat` together with `--message` for a message note. Without a target the command is exit code 2 (`choose --chat, --person, or --chat with --message`). `--model` works only with `--author ai` (`--model requires --author ai`), and `--author` must be `owner` or `ai`. The body is the argument; `-` reads the whole body from stdin. `--tag` can be repeated. For a negative chat id use `--chat=-12345` or `--chat -12345 -- body`: the `--` ends the flags before a body that starts with `-`. A note for an `@username` the database has never seen asks Telegram to resolve it, which needs a login. The note body is not marked `untrusted`: it is the text of the owner or the agent. An agent signs its notes with `--author ai` and the name of its model.

**Example.**

```sh
echo "Asked for the contract, follow up on Monday" | \
  tg note add --chat=-1001234567890 --message 4102 --author ai --model my-model --tag followup --tag contract -
tg note add --person 987654321 -- "Prefers short messages"
```

**Answer.**

```json
{"ok":true,"data":{"id":2,"kind":"message","chat_id":-1001234567890,"target_id":4102,"body":"Asked for the contract, follow up on Monday","author":"ai","model":"my-model","tags":["contract","followup"],"created_at":"2026-11-14T18:34:24Z","updated_at":"2026-11-14T18:34:24Z"}}
```

**Answer fields** (`data`): `id` integer, `kind` string, `chat_id` integer (optional), `target_id` integer (optional), `body` string, `author` string, `model` string (optional), `tags` array (optional), `created_at` string, `updated_at` string.

`kind` is `chat`, `person` or `message`; `target_id` is the message id of a message note and the person id of a person note.

**Exit codes.** 0; 2 no target, a bad `--author`, `--model` without `--author ai`; 3 no Daemon; 4 only for an `@username` that needs Telegram; 7 the chat is closed or unknown.

**Verification.** Notes about people are not filtered by chat access lists. See [FEATURES.md](../../FEATURES.md), item 10.8.

### tg note list

Lists local notes, newest first.

```text
tg note list [flags]
```

<!-- schema:flags tg note list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | filter by owner or ai |
| `--before-id` | int64 | `0` | return notes with a smaller id |
| `--chat` | string | `none` | filter by chat reference |
| `--limit` | int | `50` | notes per page, 1 to 500 |
| `--message` | int64 | `0` | filter one message id, requires --chat |
| `--person` | string | `none` | filter by person reference |
| `--tag` | string | `none` | filter by tag |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. `--chat` filters chat and message notes, `--person` filters person notes, `--chat` with `--message` filters one message; `--message` without `--chat` is exit code 2 (`--message requires --chat and cannot be combined with --person`). Page with `--before-id`: pass the smallest id of the page to get older notes. A page holds 50 notes by default and 500 at most. With `--format jsonl` each note is a line and there is no cursor line. For a negative chat id use `--chat=-12345`.

**Example.**

```sh
tg note list --tag followup
tg note list --chat=-1001234567890 --message 4102
tg note list --before-id 3 --format jsonl --fields id,body
```

**Answer.**

```json
{"ok":true,"data":{"notes":[{"id":3,"kind":"person","target_id":987654321,"body":"Prefers short messages","author":"owner","created_at":"2026-11-14T18:34:24Z","updated_at":"2026-11-14T18:34:24Z"}]}}
```

**Answer fields** (`data`): `notes` array, `notes.id` integer, `notes.kind` string, `notes.chat_id` integer (optional), `notes.target_id` integer (optional), `notes.body` string, `notes.author` string, `notes.model` string (optional), `notes.tags` array (optional), `notes.created_at` string, `notes.updated_at` string.

**Exit codes.** 0; 2 `--message` without `--chat` or with `--person`, a bad `--author`; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 9.2 and 10.8.

### tg note delete

Deletes one note by id, or all notes of one author.

```text
tg note delete <id> | --author owner|ai --yes [flags]
```

<!-- schema:flags tg note delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | delete all notes by this author: owner or ai |
| `--yes` | bool | `false` | confirm deletion of all notes by author |
<!-- /schema:flags -->

**Safety class:** `local_write`. Dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. The command is dangerous: a deleted note cannot be restored. A single note by id needs no `--yes`. All notes of an author need `--author owner|ai --yes`; without `--yes` the command is exit code 2 (`would delete all ai notes; confirm with ...`) and deletes nothing. Giving no id and no author, or both, is exit code 2. Note that `--author` also deletes the notes of closed chats and is not filtered by the access lists.

**Example.**

```sh
tg note delete 2
tg note delete --author ai --yes
```

**Answer.**

```json
{"ok":true,"data":{"deleted":1}}
```

**Answer fields** (`data`): `deleted` integer.

**Exit codes.** 0; 2 no id and no author, both, or no `--yes` for the author mode; 3; 7 the note does not exist (`not found`) or belongs to a closed chat.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.8.

## Local search

### tg search

Full-text search in the local database: chats, people, messages and notes. It needs no Telegram connection and no login, only the running Daemon. It finds only what the Daemon has already loaded or saved: read a chat first (`message history`), load a whole chat with `chat sync start`, and search Telegram itself with [`message search`](messages-read.md#tg-message-search).

```text
tg search [flags] <query>
```

<!-- schema:flags tg search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--author` | string | `none` | filter note hits by owner or ai |
| `--chat` | string | `none` | restrict message and note hits to this chat |
| `--deleted` | bool | `false` | include messages marked deleted |
| `--limit` | int | `20` | hits per scope, 1 to 100 |
| `--prefix` | bool | `false` | match the last query term as a prefix |
| `--scope` | stringSlice | `[]` | scopes: chats, people, messages, notes; repeat or separate with commas |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** A running Daemon. Without `--scope` all four scopes are searched. Quote a multiword query in the shell, and put `--` before a query that starts with `-`. FTS5 syntax inside the query is harmless. `--limit` counts hits per scope (20 by default, 100 at most). `--chat` restricts message and note hits to one chat, `--author` filters note hits, `--prefix` matches the last query term as a prefix, `--deleted` includes messages marked deleted. `--scope` accepts repeated values or a comma-separated list; with `--format jsonl` each hit is a line, in scope order. Hits of closed chats are hidden; people (`people`) and notes about people are not filtered. The `snippet` and `title` of a hit are marked `untrusted` (also for a note hit), so `--human` frames them.

**Example.**

```sh
tg search "invoice" --scope chats,messages --prefix
tg search contract --scope notes --author ai
```

**Answer.**

```json
{"ok":true,"data":{"hits":[{"scope":"notes","id":2,"chat_id":-1001234567890,"snippet":"Asked for the contract, follow up on Monday","author":"ai","model":"my-model","note_kind":"message","rank":-0.51351890473488}]}}
```

**Answer fields** (`data`): `hits` array, `hits.scope` string, `hits.id` integer, `hits.chat_id` integer (optional), `hits.title` string (optional, untrusted), `hits.snippet` string (untrusted), `hits.date` string (optional), `hits.deleted` boolean (optional), `hits.author` string (optional), `hits.model` string (optional), `hits.note_kind` string (optional), `hits.rank` number.

**Exit codes.** 0 (also with no hits); 2 a bad `--scope` or `--author`, no query; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 10.9.

## The tool describes itself

### tg version

Prints the version of the tool and the revision it was built from. It needs no Daemon and no network.

```text
tg version [flags]
```

<!-- schema:flags tg version -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** None.

**Example.**

```sh
tg version
tg version --human
```

**Answer.**

```json
{"ok":true,"data":{"version":"dev","revision":"15fe89f19de6b8e5d07a1fa26cb6c2e4f59ceb7b"}}
```

**Answer fields** (`data`): `version` string, `revision` string (optional).

**Exit codes.** 0.

**Version.** A build with `just build` reports `dev`; `revision` distinguishes it from the old npm `tg`.

### tg schema

Prints the machine description of every command, argument, flag and exit code. It is read only, needs no Daemon and no network, and prints the same document every time. It is the source of truth: do not copy the list of commands, take it from the schema.

```text
tg schema [command...] [flags]
```

<!-- schema:flags tg schema -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** None.

An optional command path restricts `commands` to that command or group. An unknown path returns a usage error. Without a path, the full schema is returned.

**What the document holds.**

| Field of `data` | Content |
| --- | --- |
| `schema_version` | the version of the schema format (1) |
| `tool` | `name`, `version`, `revision` |
| `output` | the formats (`json`, `jsonl`, `toon`, `human`) and the shape of the success and failure envelopes |
| `exit_codes` | `code`, `name`, `description` of every exit code |
| `global_flags` | `name`, `type`, `default`, `usage` of every global flag |
| `commands` | every command, see below |
| `subscription` | the live event subscription: the method `subscribe`, its result (`subscription_id`, `cursor`), the notification `event` and the event fields |

Every entry of `commands` has `path` (for example `message send-media`), `summary`, an optional `description`, `safety` (`read`, `local_write`, `account_write`), `dangerous`, `result` (the Go type of the answer), `formats`, `fields` (the response fields with `path`, `type`, `optional`, `untrusted`), `args` (`name`, `required`, `variadic`) and `flags` (`name`, `type`, `default`, `values`, `usage`). List commands carry `item`, the item fields of the list. `message search` also has `public_item` for the `--public` hits, `chat sync status` has `chat_result` for the one-chat answer, and `message watch` has `subscription_method`. The response fields come from the Go types of the results, so they follow the code, and a test fails when a command lacks a safety class, a `dangerous` mark or a result type. A field with `untrusted: true` carries text written by other people.

When a flag changes the result type, `result_variants` lists the flag, result type and fields. For example, `daemon start --foreground` returns `stopped` after the daemon stops.

**Example.**

```sh
tg schema
tg schema chat
tg schema --fields commands.path
tg schema --fields exit_codes
```

**Answer.** Shortened:

```json
{"ok":true,"data":{"commands":[{"path":"auth cancel"},{"path":"auth login"},{"path":"auth resend"},{"path":"auth status"}]}}
```

**Exit codes.** 0.

**Limits.** The schema describes the background `daemon start` result, not the `--foreground` result. See [FEATURES.md](../../FEATURES.md), item 3.8.
