# Users and contacts

Commands that read a user profile, manage the account's block list, and read or change Telegram contacts. Shared rules are in [overview.md](overview.md). Names, usernames and biographies returned by Telegram are untrusted data.

## Rules shared by these commands

`<user>` accepts a username, a positive user id, a phone number, or a public `t.me` link. Groups, channels, negative ids and private invite links are exit code 2. A user hidden by access settings is treated as unknown: exit code 7. A visible target for `user block`, `user unblock`, `contact add` or `contact delete` must also be in `write`; otherwise the command exits with code 9 before Telegram receives a write request. The owner alone changes access settings.

`--dry-run` checks the reference, visibility and write permission, then calls no Telegram write method. Contact and profile reads save returned people locally. A non-local contact search or list needs a running Daemon and a login; `--local` reads the local database and needs no login.

## Users

### tg user profile

Reads one user or bot profile.

```text
tg user profile <user>
```

<!-- schema:flags tg user profile -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon, a login and a visible user. The Daemon reads the full profile and saves the person locally. For a phone number absent from contacts and the peer cache, only this command calls `contacts.resolvePhone`; it does not add the person as a contact. The Daemon permits at most one such lookup per three seconds and 20 in a rolling 24 hours. The counter is in memory and resets when the Daemon restarts. A limit hit is exit code 6 with `error.retry_after_seconds`; Telegram does not receive that request.

**Answer fields** (`data`): `id` integer, `type` (`user` or `bot`), `first_name`, `last_name`, `username`, `usernames`, `phone` (optional), `bio`, `is_contact`, `mutual_contact`, `blocked`, `verified`, `premium`, `scam`, `fake`, `deleted`, `status`, `last_seen` (only for `offline`) and `common_chats_count`. `status` is `online`, `offline`, `recently`, `last_week`, `last_month` or `hidden`.

**Exit codes.** 0; 2 bad user reference; 3 no Daemon; 4 no login; 5 Telegram rejected the read; 6 `FLOOD_WAIT`; 7 unknown or closed user.

### tg user block

Blocks one user in the main block list.

```text
tg user block <user> [flags]
```

<!-- schema:flags tg user block -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon, a login, a visible user and that user's id in `write`. No `--yes` is required.

**Answer fields** (`data`): `user_id` integer, `blocked` boolean, `dry_run` boolean (optional).

**Exit codes.** 0; 2 bad user reference; 3, 4; 5 Telegram rejected the block; 6 `FLOOD_WAIT`, not retried; 7 unknown or closed user; 9 user not in `write`.

### tg user unblock

Removes one user from the main block list.

```text
tg user unblock <user> [flags]
```

<!-- schema:flags tg user unblock -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions and exit codes.** The same as `user block`. The answer has `user_id`, `blocked: false`, and optional `dry_run`.

## Contacts

### tg contact list

Lists Telegram contacts, sorted by first name, last name and id without case sensitivity.

```text
tg contact list [flags]
```

<!-- schema:flags tg contact list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `100` | contacts per page, 1 to 1000 |
| `--local` | bool | `false` | read only the local database |
| `--offset` | int | `0` | number of contacts to skip |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

Without `--local`, the Daemon downloads the complete contact list, refreshes the local contact flag, then returns the requested page. `--local` returns only saved contacts. Closed users are absent. `next_offset` is present when another page exists.

**Answer fields** (`data`): `contacts` array, `contacts.id`, `first_name`, `last_name`, `username`, `phone` (optional), `mutual` (only without `--local`) and `deleted`; `next_offset` integer (optional).

**Exit codes.** 0; 2 invalid page; 3 no Daemon; 4 no login without `--local`; 5 Telegram rejected the read; 6 `FLOOD_WAIT`.

### tg contact add

Adds a visible user or bot to Telegram contacts.

```text
tg contact add <user> [flags]
```

<!-- schema:flags tg contact add -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--first-name` | string | `none` | contact first name |
| `--last-name` | string | `none` | contact last name |
| `--phone` | string | `none` | contact phone number |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon, a login, a visible target and its id in `write`. A phone number by itself cannot select the target. Without `--first-name`, the Daemon uses the user's current first name; it similarly uses the current last name when `--last-name` is absent. The final first name must be nonempty. `--dry-run` writes nothing.

**Answer fields** (`data`): `user_id` integer, `first_name`, `last_name`, `is_contact`, `mutual` boolean and `dry_run` (optional). A completed addition returns `is_contact: true`; a dry run returns `is_contact: false` and `dry_run: true`.

**Exit codes.** 0; 2 bad target or empty final first name; 3, 4; 5; 6 not retried; 7 unknown or closed user; 9 user not in `write`.

### tg contact delete

Deletes one Telegram contact while keeping other saved person data locally.

```text
tg contact delete <user> [flags]
```

<!-- schema:flags tg contact delete -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | check without changing Telegram |
| `--yes` | bool | `false` | confirm deletion |
<!-- /schema:flags -->

**Safety class:** `account_write`. Dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon, a login, a visible user and its id in `write`. Without `--yes` and without `--dry-run`, the CLI exits with code 2 and does not call the Daemon. The answer has `user_id`, `deleted` and optional `dry_run`: a completed deletion returns `deleted: true`; a dry run returns `deleted: false` and `dry_run: true`.

**Exit codes.** 0; 2 no `--yes` and no `--dry-run`, or bad target; 3, 4; 5; 6 not retried; 7 unknown or closed user; 9 user not in `write`.

### tg contact search

Searches contacts and public users, or searches saved contacts locally.

```text
tg contact search <query> [flags]
```

<!-- schema:flags tg contact search -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--limit` | int | `20` | maximum results, 1 to 100 |
| `--local` | bool | `false` | search only local contacts |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

Without `--local`, Telegram's own contact results precede global results and channel results are omitted. Each result has `source` (`contacts` or `global`). With `--local`, the search uses the same case-insensitive, `ё`-folding full-text search as `tg search`, among saved contacts only. Closed users are omitted.

**Answer fields** (`data`): `contacts` array, `contacts.id`, `first_name`, `last_name`, `username`, `phone` (optional), `mutual` (optional), `deleted`, `source`.

**Exit codes.** 0; 2 empty query or invalid limit; 3 no Daemon; 4 no login without `--local`; 5 Telegram rejected the search; 6 `FLOOD_WAIT`.
