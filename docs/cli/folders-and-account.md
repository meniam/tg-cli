# Folders and account state

These read-only commands require a running Daemon. Without `--local` they also require a login, read Telegram and save the result. `--local` reads the saved snapshot without a login or Telegram connection. An empty local snapshot has an empty list and `synced_at: null`. Timestamps are Unix seconds. Closed chats are hidden by the access policy. See [overview.md](overview.md) for global flags and exit codes.

## tg folder list

Lists folder rules and explicit pinned, included and excluded chats in Telegram order. Rules are not expanded into the actual membership. Folder titles and chat titles are untrusted.

```text
tg folder list [--local]
```

<!-- schema:flags tg folder list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--local` | bool | `false` | read the saved snapshot without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Formats: `json`, `jsonl`, `toon`, `human`.

```sh
tg folder list --local
```

```json
{"ok":true,"data":{"folders":[],"synced_at":null}}
```

Exit codes: 0; 2 invalid arguments; 3 no Daemon; 4 no login for a Telegram read; 6 flood wait.

## tg account sessions

Lists account sessions and the authorization TTL in days. Each session has an `id`, the first 16 hexadecimal characters of SHA-256 over its hash encoded as eight big-endian bytes. Session hash and IP are not printed. Device and application names are untrusted.

```text
tg account sessions [--local]
```

<!-- schema:flags tg account sessions -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--local` | bool | `false` | read the saved snapshot without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Formats: `json`, `jsonl`, `toon`, `human`.

```sh
tg account sessions --local
```

```json
{"ok":true,"data":{"sessions":[],"authorization_ttl_days":0,"synced_at":null}}
```

Exit codes: 0; 2 invalid arguments; 3 no Daemon; 4 no login for a Telegram read; 6 flood wait.

## tg account blocked

Lists blocked peers. Pass `next_cursor` to `--cursor` for the next Telegram page. A filtered page can be empty while a cursor remains. A partial page updates saved peers without removing other rows; only a complete first page replaces the snapshot. Titles and usernames are untrusted.

```text
tg account blocked [--limit N] [--cursor C | --local]
```

<!-- schema:flags tg account blocked -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--cursor` | string | `none` | continue after a previous page; cannot be used with --local |
| `--limit` | int | `100` | blocked peers per page, 1 to 1000 |
| `--local` | bool | `false` | read the saved snapshot without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Formats: `json`, `jsonl`, `toon`, `human`.

```sh
tg account blocked --limit 20
```

```json
{"ok":true,"data":{"peers":[],"synced_at":null}}
```

Exit codes: 0; 2 invalid limit, cursor or combination; 3 no Daemon; 4 no login for a Telegram read; 6 flood wait.

## tg account notify

Reads default notification settings for users, groups and channels. A null setting means Telegram did not supply a value.

```text
tg account notify [--local]
```

<!-- schema:flags tg account notify -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--local` | bool | `false` | read the saved snapshot without Telegram |
<!-- /schema:flags -->

**Safety class:** `read`. Formats: `json`, `jsonl`, `toon`, `human`.

```sh
tg account notify --local
```

```json
{"ok":true,"data":{"defaults":[],"synced_at":null}}
```

Exit codes: 0; 2 invalid arguments; 3 no Daemon; 4 no login for a Telegram read; 6 flood wait.

## tg account terminate

Terminates one other session. The owner must enable `sessions` in `access.json`. The current session cannot be terminated. Preview with `--dry-run`; an actual termination requires `--yes` and is recorded in `account.jsonl`.

```sh
tg account terminate <id> [--yes] [--dry-run]
```

<!-- schema:flags tg account terminate -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without terminating the session |
| `--yes` | bool | `false` | confirm session termination |
<!-- /schema:flags -->

**Safety class:** `account_write`, `dangerous`. Device and application names in the preview are untrusted.

## tg folder invite-info

Shows a shared folder invite without joining. A nonempty `allow` list closes this command. Blocked chats are omitted. Folder and chat titles are untrusted.

<!-- schema:flags tg folder invite-info -->
No command flags.
<!-- /schema:flags -->

## tg folder order

Sets the folder order. Supply every existing folder ID exactly once. The owner must enable `dialogs` in `access.json`.

<!-- schema:flags tg folder order -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg folder create

Manages a chat folder. The owner must enable `dialogs` in `access.json`. Deletion requires `--yes` or `--dry-run` and is journaled. Concurrent edits can overwrite each other.

<!-- schema:flags tg folder create -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--color` | int | `0` | folder color ID |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--emoticon` | string | `none` | folder emoticon |
| `--exclude` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--include` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--pin` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--rules` | string | `none` | comma-separated folder rules |
<!-- /schema:flags -->

## tg folder edit

Manages a chat folder. The owner must enable `dialogs` in `access.json`. Deletion requires `--yes` or `--dry-run` and is journaled. Concurrent edits can overwrite each other.

<!-- schema:flags tg folder edit -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--add` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--color` | int | `0` | folder color ID |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--emoticon` | string | `none` | folder emoticon |
| `--exclude` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--pin` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--remove` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--rules` | string | `none` | comma-separated folder rules |
| `--title` | string | `none` | new folder title |
| `--unexclude` | stringArray | `[]` | chat reference; repeat for multiple chats |
| `--unpin` | stringArray | `[]` | chat reference; repeat for multiple chats |
<!-- /schema:flags -->

## tg folder delete

Manages a chat folder. The owner must enable `dialogs` in `access.json`. Deletion requires `--yes` or `--dry-run` and is journaled. Concurrent edits can overwrite each other.

<!-- schema:flags tg folder delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--yes` | bool | `false` | confirm deletion |
<!-- /schema:flags -->
