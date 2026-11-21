# Settings

`tg settings` manages the selected profile's settings. It uses the running
Daemon when one is available. A stopped Daemon falls back to a locked local
database operation, so a settings write does not start Telegram or require a
session. The common profile and output rules are in [overview.md](overview.md).

The result for a setting has `key`, effective `value`, `source` (`default`,
`database`, `env`, or `flag`), `stored` (when a database value exists),
`overridden_by` (when an environment variable or flag wins), and
`restart_required`. Use `tg schema` for the complete registry: type, default,
environment variable, command-line flag, application timing, protection and
secret status.

Settings below `access.` control the chats and directories available to an
agent. They require the owner's passphrase for every write. Reading
`access.allow` or `access.block` also requires it, because those values contain
chat ids. The phrase is read without echo from the terminal; it is never a
command argument, printed answer, or journal field.

## Commands

### tg settings list

Lists every known setting, or the settings whose key starts with a prefix.

```text
tg settings list [--prefix <prefix>]
```

<!-- schema:flags tg settings list -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--prefix` | string | `none` | filter settings by key prefix |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`,
`human`.

**Preconditions.** The selected profile must have a usable local database. The
command needs neither a Telegram connection nor an owner passphrase. It does
not reveal ids in protected access lists.

**Answer fields** (`data`): `settings`, an array of setting values. Each item
has `key`, `value`, `source`, `stored` when set, `overridden_by` when an
override wins, and `restart_required`.

**Exit codes.** 0 success; 1 a local database error; 2 invalid configuration.

### tg settings get

Shows one effective setting.

```text
tg settings get <key>
```

<!-- schema:flags tg settings get -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Preconditions.** `<key>` is a key
from `tg schema`. `access.allow` and `access.block` prompt for the owner's
passphrase before contacting the Daemon or database. Other access values do not
expose chat ids and can be read without it.

**Answer fields** (`data`): `key`, `value`, `source`, `stored`, optional
`overridden_by`, and `restart_required`.

**Exit codes.** 0 success; 1 a local database error; 2 an unknown key, invalid
configuration, or no terminal for a required passphrase; 3 no Daemon when the
local profile cannot be opened; 9 a missing, wrong, or temporarily locked owner
passphrase.

### tg settings set

Sets one scalar setting.

```text
tg settings set <key> <value>
```

<!-- schema:flags tg settings set -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** The value
must match the key type and all related settings must remain valid. For example,
the background-sync window start stays before its end. List settings use
`add` and `remove`. An `access.*` key prompts for the owner's passphrase.
Values overridden by an environment variable or a command-line flag are stored
but do not change the effective value until the override is removed.

**Answer fields** (`data`): the resulting setting value, `previous`, and
`applied`. `applied` says whether the running Daemon used the change or a later
start will use it.

**Exit codes.** 0 success; 1 local database error; 2 invalid key, value or
setting combination, invalid configuration, or no terminal for a required
passphrase; 3 no Daemon when the local profile cannot be opened; 9 a missing,
wrong, or temporarily locked owner passphrase.

### tg settings unset

Removes the database value of a setting and returns it to its default or an
active override.

```text
tg settings unset <key>
```

<!-- schema:flags tg settings unset -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** `<key>` is a
key from `tg schema`; `access.*` prompts for the owner's passphrase.

**Answer fields** (`data`): the resulting setting value, `previous`, and
`applied`.

**Exit codes.** Same as `tg settings set`.

### tg settings add

Adds one or more values to an `ids` or `paths` list. Duplicate values are
ignored and the stored list has a stable order.

```text
tg settings add <key> <value> [value...]
```

<!-- schema:flags tg settings add -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** `<key>` has
type `ids` or `paths`. Every `access.*` list prompts for the owner's passphrase.

**Answer fields** (`data`): the resulting setting value, `previous`, and
`applied`.

**Exit codes.** Same as `tg settings set`.

### tg settings remove

Removes one or more values from an `ids` or `paths` list. Missing values leave
the list unchanged.

```text
tg settings remove <key> <value> [value...]
```

<!-- schema:flags tg settings remove -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** `<key>` has
type `ids` or `paths`. Every `access.*` list prompts for the owner's passphrase.

**Answer fields** (`data`): the resulting setting value, `previous`, and
`applied`.

**Exit codes.** Same as `tg settings set`.

### tg settings token init

Creates the owner passphrase for protected access settings. It asks for the new
phrase twice without echo. It succeeds only when no owner passphrase exists.

```text
tg settings token init
```

<!-- schema:flags tg settings token init -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** An
interactive terminal. The passphrase is not accepted as an argument.

**Answer fields** (`data`): `updated`.

**Exit codes.** 0 success; 1 local database error; 2 no terminal or mismatched
confirmation; 3 no Daemon when the local profile cannot be opened; 9 an owner
passphrase already exists.

### tg settings token change

Changes the owner passphrase. It asks for the current phrase once and for the
new phrase twice without echo.

```text
tg settings token change
```

<!-- schema:flags tg settings token change -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** An
interactive terminal and the current owner passphrase. The passphrase is not
accepted as an argument.

**Answer fields** (`data`): `updated`.

**Exit codes.** 0 success; 1 local database error; 2 no terminal or mismatched
new-phrase confirmation; 3 no Daemon when the local profile cannot be opened; 9
a missing, wrong, or temporarily locked current phrase.
