# Configuration

`tg` reads its configuration from `~/.config/tg-cli/config.toml`. The file
holds the profile data directory, file names, behaviour settings, and named
profile settings. `tg schema` lists every key, its default, and its command-line
or environment override.

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

### tg config show

Migrates legacy profile setting files first, then prints configuration-file
values with their defaults. It does not show the final values after environment
variables or flags override them. Proxy addresses are redacted: usernames,
passwords, and MTProxy secrets are never included in the answer. It validates
socket paths for the default profile and every named profile before returning a
result. `TG_CLI_HOME`, when set, selects the home used for migration and socket
path validation.

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
{"ok":true,"data":{"home":"/Users/alice/Library/Application Support/tg-cli","default_profile":"default","files":{"database":"store.db","session":"session.json","log":"daemon.log","deletions":"deletions.jsonl","access":"access.json"},"behavior":{"throttle_percent":60,"flood_autowait":"10s","expect_user":""},"profiles":{"default":{"client":"desktop","device":"MacBook","transport":"tcp","proxy":"socks5://127.0.0.1:1080"}}}}
```

**Answer fields** (`data`): `home` string, `default_profile` string, `files`
object (`database`, `session`, `log`, `deletions`, `access` strings), `behavior`
object (`throttle_percent` number, `flood_autowait` and `expect_user` strings),
and `profiles` object keyed by profile name. Each profile contains `client`,
`device`, `transport`, and redacted `proxy` strings.

**Exit codes.** 0 success, 2 invalid configuration or an invalid socket path,
1 a local migration error.

### tg config migrate

Explicitly migrates legacy `client`, `device`, `transport`, and `proxy` files
from every profile under the current home directory. Before changing the
configuration, it copies the migrated files and the prior configuration into a
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
