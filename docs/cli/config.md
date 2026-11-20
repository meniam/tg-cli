# Configuration

`tg` reads its configuration from `~/.config/tg-cli/config.toml`. The file
holds the profile data directory, file names, behaviour settings, and named
profile settings. `tg config init` writes a template with every key; `tg schema`
lists every key, its default, and its command-line or environment override.

The configuration directory holds the database of every profile as well, in
`db/<profile>/`: `~/.config/tg-cli/db/default/store.db` for the default profile.
The root `db` depends on the configuration path alone: `home` and `TG_CLI_HOME`
do not move it, and `--profile` only selects one directory inside it. Keeping
that root out of a dotfiles repository, a file sync and a cloud backup is
described in [overview.md](overview.md#profiles-and-the-data-directory). The
`home` setting and `[files]` keep their meaning for the other files of the
profile and for the database file name.

### tg config path

Prints the configuration file path selected for this invocation. Before running,
the command reads and validates an existing configuration file; it does not
create one.

```text
tg config path [flags]
```

<!-- schema:flags tg config path -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Preconditions.** If the
configuration file exists, it must be valid.

**Example.**

```sh
tg config path
```

**Answer.**

```json
{"ok":true,"data":{"path":"/Users/alice/.config/tg-cli/config.toml"}}
```

**Answer fields** (`data`): `path` string.

**Exit codes.** 0 success, 2 invalid configuration, 1 a local read error.

### tg config init

Writes the configuration template to the configuration path. The template lists
every key with what it does, its default, the values it accepts, and the flag or
environment variable that overrides it; all of them are commented out, so the
written file behaves like the defaults. The command runs under the configuration
lock and creates the file exclusively with mode `0600`. It never replaces an
existing file: the answer then names the file and the exit code is 2, so the
owner keeps their edits.

```text
tg config init [flags]
```

<!-- schema:flags tg config init -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** The
configuration file must not exist.

**Example.**

```sh
tg config init
```

**Answer.**

```json
{"ok":true,"data":{"path":"/Users/alice/.config/tg-cli/config.toml","created":true}}
```

**Answer fields** (`data`): `path` and `created` (`true`).

**Exit codes.** 0 the file was written, 2 the file already exists, 1 a local
write error.

### tg config show

Migrates legacy profile setting files first, then prints configuration-file
values with their defaults. It does not show the final values after environment
variables or flags override them. Proxy addresses are redacted: usernames,
passwords, and MTProxy secrets are never included in the answer. It validates
socket paths for the default profile and every named profile before returning a
result. `TG_CLI_HOME`, when set, selects the home used for migration and socket
path validation.

`home` is the base directory of the profile data: session, socket, lock, journal
and access list. It is not where the database lives. `db_dir` is the directory of
the profile databases, the `db` directory next to `config.toml`; the database of a
profile is `<db_dir>/<profile>/<files.database>`. The database directory follows
the configuration file, therefore it does not change with `home` or
`TG_CLI_HOME`.

```text
tg config show [flags]
```

<!-- schema:flags tg config show -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** A valid
configuration. The command may create a migration backup, update
`config.toml`, and remove migrated legacy setting files.

**Example.**

```sh
tg config show
```

**Answer.**

```json
{"ok":true,"data":{"home":"/Users/alice/Library/Application Support/tg-cli","db_dir":"/Users/alice/.config/tg-cli/db","default_profile":"default","files":{"database":"store.db","session":"session.json","log":"daemon.log","deletions":"deletions.jsonl","access":"access.json"},"behavior":{"throttle_percent":60,"flood_autowait":"10s","expect_user":""},"background_sync":{"enabled":false,"window_start":"08:00","window_end":"20:00","interval_min":"40m","interval_max":"3h","page_pause_min":"2s","page_pause_max":"8s"},"profiles":{"default":{"client":"desktop","device":"MacBook","transport":"tcp","proxy":"socks5://127.0.0.1:1080"}}}}
```

**Answer fields** (`data`): `home` and `db_dir` strings, `default_profile`
string, `files` object (`database`, `session`, `log`, `deletions`, `access`
strings), `behavior` object (`throttle_percent` number, `flood_autowait` and
`expect_user` strings), `background_sync` object (`enabled` boolean;
`window_start`, `window_end`, `interval_min`, `interval_max`, `page_pause_min`
and `page_pause_max` strings, as written in the file or the defaults; the
environment variables are not applied, see
[Request throttle](overview.md#request-throttle-and-flood_wait)), and `profiles`
object keyed by profile name. Each profile contains `client`, `device`,
`transport`, and redacted `proxy` strings.

**Exit codes.** 0 success, 2 invalid configuration (including a wrong `[background_sync]` value) or an invalid socket path,
1 a local migration error.

### tg config migrate

Explicitly migrates legacy `client`, `device`, `transport`, and `proxy` files
from every profile under the current home directory. The `db` directory under
that home is not a profile and is skipped. Before changing the configuration, it
copies the migrated files and the prior configuration into a
`.config-migration-<time>` directory under that home. It never migrates a
session, database, access list, or journal file.

```text
tg config migrate [flags]
```

<!-- schema:flags tg config migrate -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Preconditions.** A valid
configuration. When a legacy setting file is found, it must be readable and
valid. `TG_CLI_HOME`, when set, selects the home scanned for legacy files. A
conflicting setting in the configuration is retained; the legacy file remains
and the warning appears in both stderr and `warnings`.

**Example.**

```sh
tg config migrate
```

**Answer.**

```json
{"ok":true,"data":{"migrated":true,"backup":"/tmp/tg-cli/.config-migration-20261116T120000","warnings":[]}}
```

**Answer fields** (`data`): `migrated` bool, `backup` string (when files were
migrated), and `warnings` array of strings.

**Exit codes.** 0 success, including when there is nothing to migrate; 2 invalid
configuration or legacy setting; 1 a local migration error.
