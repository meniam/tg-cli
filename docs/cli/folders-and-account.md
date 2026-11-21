# Folders and account state

These commands require a running Daemon. For the snapshot commands that support `--local`, the default reads Telegram and requires a login; `--local` reads the saved snapshot without a login or Telegram connection. Other reads and writes have their own conditions below. An empty local snapshot has an empty list and `synced_at: null`. Timestamps are Unix seconds. Closed chats are hidden by the access policy. See [overview.md](overview.md) for global flags and exit codes.

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

Terminates one other session. The owner must enable `access.sessions`. The current session cannot be terminated. Preview with `--dry-run`; an actual termination requires `--yes` and is recorded in `account.jsonl`.

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

Sets the folder order. Supply every existing folder ID exactly once. The owner must enable `access.dialogs`.

<!-- schema:flags tg folder order -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
<!-- /schema:flags -->

## tg folder create

Creates a folder with `tg folder create <title>`. Include at least one chat with `--include` or `--pin`, or select an inclusion rule with `--rules`. The owner must enable `access.dialogs`.

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

Edits a folder with `tg folder edit <id>`. The owner must enable `access.dialogs`. Existing hidden chats are preserved; concurrent edits can overwrite each other.

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

Deletes a folder with `tg folder delete <id>`. The owner must enable `access.dialogs`. Deletion requires `--yes` or `--dry-run` and is journaled.

<!-- schema:flags tg folder delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | preview without changing Telegram |
| `--yes` | bool | `false` | confirm deletion |
<!-- /schema:flags -->

### tg account profile

Read the current account's profile, birthday, personal channel and emoji status. Names, username and biography are untrusted.

```text
tg account profile
```

<!-- schema:flags tg account profile -->
No command flags.
<!-- /schema:flags -->

### tg account emoji-statuses

List default or recently used emoji statuses. Collectible titles and slugs are untrusted.

```text
tg account emoji-statuses [--recent]
```

<!-- schema:flags tg account emoji-statuses -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--recent` | bool | `false` | show recently used statuses |
<!-- /schema:flags -->

### tg account set

Change the birthday, personal channel or emoji status. The owner must enable `access.profile`. Multiple settings are applied in order and may partially succeed. `--dry-run` checks without writing.

```text
tg account set [flags]
```

<!-- schema:flags tg account set -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--birthday` | string | `none` | birthday YYYY-MM-DD, MM-DD or none |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--emoji-status` | string | `none` | emoji document id or none |
| `--emoji-status-until` | string | `none` | status expiry in RFC 3339 |
| `--personal-channel` | string | `none` | personal channel or none |
<!-- /schema:flags -->

### tg account privacy get

Read privacy rules for a key. IDs hidden by access lists are omitted and counted in `hidden`. Keys: `last-seen`, `phone-number`, `added-by-phone`, `profile-photo`, `forwards`, `calls`, `p2p-calls`, `invites`, `voice-messages`, `bio`, `birthday`, `gifts-auto-save`, `no-paid-messages`, `saved-music`.

```text
tg account privacy get <key>
```

<!-- schema:flags tg account privacy get -->
No command flags.
<!-- /schema:flags -->

### tg account privacy set

Replace all rules for a key. This requires the `profile` permission and `--yes`, or `--dry-run` to preview. Hidden existing exceptions require `--replace-hidden` before replacement. Exceptions may be passed more than once.

```text
tg account privacy set <key> --default everybody|contacts|nobody [flags]
```

<!-- schema:flags tg account privacy set -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--allow-bots` | bool | `false` | allow-bots |
| `--allow-chat` | stringArray | `[]` | add allow-chat exception |
| `--allow-close-friends` | bool | `false` | allow-close-friends |
| `--allow-premium` | bool | `false` | allow-premium |
| `--allow-user` | stringArray | `[]` | add allow-user exception |
| `--default` | string | `none` | default: everybody, contacts or nobody |
| `--deny-bots` | bool | `false` | deny-bots |
| `--deny-chat` | stringArray | `[]` | add deny-chat exception |
| `--deny-user` | stringArray | `[]` | add deny-user exception |
| `--dry-run` | bool | `false` | dry-run |
| `--replace-hidden` | bool | `false` | replace-hidden |
| `--yes` | bool | `false` | yes |
<!-- /schema:flags -->
