# Configuration

`tg` reads its configuration from `~/.config/tg-cli/config.toml`. The file holds `home`, `default_profile`, and `[files]`. Profile settings live
in the selected profile database and are managed with `tg settings`.
`voice_transcription.*` is the current exception: it remains in `config.toml`.
Its automatic sources are enabled by default, with a daily budget of 5000 MB
and 500 files; set `enabled = false` to disable them after a Daemon restart.
`tg config init` writes the compact template; `tg schema` lists configuration
and profile-setting keys separately.

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

Prints the configuration that remains outside the profile database. Profile
settings, including network settings and access policy, are managed with
[`tg settings`](settings.md). It validates profile paths before returning a
result. `TG_CLI_HOME`, when set, selects the home used for this validation.

`home` is the base directory of the profile data: session, socket, lock, journal
and access list. It defaults to `~/.config/tg-cli`; the data of the previous
default on macOS, `~/Library/Application Support/tg-cli`, moves there at the
first command that reaches a profile, unless `home` or `TG_CLI_HOME` is set (see
[the previous default](overview.md#profiles-and-the-data-directory)). It is not
where the database lives. `db_dir` is the directory of
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
configuration. The command does not change the file.

**Example.**

```sh
tg config show
```

**Answer.**

```json
{"ok":true,"data":{"home":"/Users/alice/.config/tg-cli","db_dir":"/Users/alice/.config/tg-cli/db","default_profile":"default","files":{"database":"store.db","session":"session.json","log":"daemon.log","deletions":"deletions.jsonl","access":"access.json"}}}
```

**Answer fields** (`data`): `home` and `db_dir` strings, `default_profile`
string, and a `files` object with `database`, `session`, `log`, `deletions`,
and `access` strings. `access` is the legacy file name retained only for migration; access policy itself is in the profile database.

**Exit codes.** 0 success; 2 invalid configuration or an invalid profile path.

### tg config migrate

Explicitly migrates legacy configuration sections and the legacy access policy
into each affected profile database. Before changing sources, it copies the
configuration file and any legacy access file into a
`.config-migration-<time>` directory under the selected home. It does not
migrate a session, database, or journal file.

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
