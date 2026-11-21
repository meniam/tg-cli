# Daemon, login, transport, config

One Daemon per profile holds the connection and the session, and the CLI sends it requests. Profiles are independent: `--profile <name>` selects the account, and each has its own Daemon.

## First run

```bash
tg --profile <name> daemon start   # background; desktop is the default preset when own credentials are absent
tg --profile <name> daemon status  # profile, connection, login, active requests, background_sync block
tg status                          # text dashboard: vectors, transcription, history, Telegram, storage, settings
tg daemon logs --tail 50           # log of Daemon requests and errors for debugging (--follow tails it)
tg auth login                      # the human runs this (! tg auth login): number, code, 2FA (or --qr)
tg auth status                     # whether the account is logged in and who it is
```

`tg daemon start` is idempotent: for a profile with a live Daemon it reports that Daemon and does not start a second one. To tell which Daemons run, use `scripts/tg-doctor.sh` (every profile on disk, the selected one in detail, a `verdict:` line; see "First run" in `SKILL.md`) or, for one profile, `tg --profile <name> daemon status`. Exit code 3 means nothing answers on the socket of that profile. Any other failure of `daemon status` (for example a sandbox that blocks the socket) is not "stopped": do not start a Daemon on it. Exit code 4 means no login, a lost login or an `--expect-user` mismatch: check `data.authorized` and the profile in `tg daemon status` before asking for a login.

**Profiles.** There is no command that lists profiles. They are the directories `<home>/<profile>/` that hold `session.json` or `daemon.sock` (`home` is in `tg config show`; `db` and `models` are not profiles). With several profiles, never choose one yourself: ask the user and pass `--profile <name>` to every command.

**Transport.** Set it at start: `tg daemon start --transport wss` or `tg daemon start --transport auto`. Choose `auto` when TCP may be blocked: it starts with TCP and recreates the client on WSS of the same DC after a connection or handshake error, and also if TCP did not become `ready` within 10 seconds. WSS stays until the Daemon stops. To change the network of a running Daemon, first run `daemon stop`.

**Proxy.** `TG_CLI_PROXY='socks5://...' tg daemon start`. SOCKS5 works with TCP and WSS. An HTTP proxy `http://user:password@host:port` works only with WSS and `auto`; with it `auto` uses WSS at once, and the Basic password is sent to the proxy without TLS. MTProxy cannot be used with WSS and `auto`. The selected profile stores the network settings in its database; `--proxy none` stores no proxy.

**`tg status`.** Shows the daemon’s state and active settings in five sections: vector index, transcription, history, Telegram and storage. It makes no Telegram request and works after login is lost. Use `tg daemon status` for machine-readable daemon state. Explicit `--format json|jsonl|toon` returns exit code 2.

**Background load block.** `daemon status` has a `background_sync` block: `state` (`disabled`, `waiting_window`, `waiting_interval`, `running`), `next_run_at`, `last_run_at`, `last_result` and `last_chat_id`. The scheduled load is on by default; `background_sync.enabled` controls it and requires a Daemon restart. Do not change it unless the owner asks. With `--idle-timeout` the Daemon may stop before a run, because waiting for the schedule does not count as activity.

**Semantic search block.** `daemon status` has a `semantic_search` block (always present) and `tg status` a `VECTOR INDEX` section: `disabled` while `semantic_search.enabled` is off, otherwise the indexer state (`idle`, `indexing`, `unavailable`, `model_missing`, `error`), the model and `indexed` of `total` fragments. `unavailable` means Ollama does not answer and `model_missing` that it lacks the model (`ollama pull bge-m3`); `tg search` keeps working by words in every state. The feature is on by default and a change requires a Daemon restart.

**Status and failures.** `daemon status` shows `connection`, the selected `transport`, the current `active_transport`, the count `transport_switches`, `dc` and the proxy address without the secret. When the client is recreated, a request to Telegram gets code 10 `offline`; a write that was already admitted gets 30 seconds to finish, then its context is canceled, and the Daemon does not retry it itself. On a connection drop, a request to Telegram waits up to 15 seconds and may return code 10 `offline`; local operations remain available.

`daemon start --foreground` keeps the Daemon in the terminal. `--idle-timeout 30m` stops it when idle; by default it runs until `daemon stop`. Your own keys instead of the preset: `TG_API_ID` and `TG_API_HASH` (cannot be combined with `--client`).

Exit code 3: the Daemon is not running, see "Start the Daemon" in `SKILL.md`. Exit code 4: the login is missing or lost, or the expected-account check failed. Check `auth status` and `--expect-user`; if the login is lost, ask the human to run `! tg auth login`.

## Start at login (macOS)

`tg autostart` makes launchd start the Daemon of a profile at every login and again after a crash. It changes the login items of the machine, so run `on` and `off` only when the user asks; `status` only looks.

```bash
tg autostart status          # enabled (job file exists), loaded (launchd knows it), executable (the binary it runs)
tg status                    # on macOS the Autostart row of the Telegram block says the same in one line
tg autostart on              # write ~/Library/LaunchAgents/dev.tg-cli.daemon.plist and load it
tg autostart off             # unload the job and delete the file
tg autostart on --profile work   # another profile: its own job dev.tg-cli.daemon.work
```

- **What the job runs.** `tg daemon start --foreground` with the full path of the running binary, with the settings saved for the profile (the official client preset included). Run `on` with the installed `tg` (for example `~/.local/bin/tg`): a temporary build or a `go run` binary is refused with exit code 2, and a moved binary shows up as a stale `executable` in `status` (run `on` again to replace the job).
- **Secrets.** Own application credentials (`TG_API_ID`, `TG_API_HASH`) are never written to the job file; `on` refuses with exit code 2 while they are set. The job file has no secret otherwise.
- **Stop.** The job restarts the Daemon only after a failure. `tg daemon stop` ends it with exit code 0 and launchd leaves it stopped until the next login; a stop from `off` also stops the Daemon launchd ran.
- **A Daemon started by hand** is not under launchd. `on` then writes the job file but does not load it (`loaded: false`, a `note`): launchd loads it at the next login. To put the daemon under launchd now, `tg daemon stop` and then `tg autostart on` again. Starting a profile that already has a Daemon is not an error and creates no second one.
- **After a reboot** the history sync queue is empty (it lives in the memory of the Daemon): see [sync-active-chats](../recipes/sync-active-chats.md). Only macOS: on another system all three commands give exit code 2.
- **Not verified on a live account:** the real `launchctl bootstrap`, the start after a reboot and the restart after a crash (`FEATURES.md`, item 2.13); tests use a stub `launchctl`.

## Config

Configuration that locates profiles is in `~/.config/tg-cli/config.toml`. `tg config path` shows its path. `tg config show` returns only `home`, `db_dir`, `default_profile` and `files`; it does not open the profile database. `tg settings list` and `tg settings get` show profile settings. `tg config init` writes this reduced template without overwriting an existing file (code 2). `tg config migrate` imports legacy settings once. `home` sets the directory of profile data (session, socket and logs), and `TG_CLI_HOME` overrides it for one call only. `home` defaults to `~/.config/tg-cli`. On macOS the first command moves data from the old default, `~/Library/Application Support/tg-cli`, there when `home` and `TG_CLI_HOME` are unset. Neither `home` nor `TG_CLI_HOME` moves the database. The database directory in `tg config show` is `db_dir`. See profile-setting keys, types, defaults, variables and flags in the `settings` block of `tg schema`.

## Login and session loss

On `auth_lost`, stop the Daemon before importing a session. For a new login, do not stop the Daemon. The human runs `! tg auth login` and enters the code and, if needed, the password. After a successful login the Daemon recreates the client itself and `auth_lost` disappears. Reconnecting with an invalid key may fail.

A session string gives full access: import only a string from the owner, keep it in a file with mode 0600 and run only one process on the key, including other machines. Pass the 2FA password through a file with owner-only permissions.

## Build and install

`just build` builds the binary into `dist/tg`. Put it in `PATH` under the name `tg`. If the old npm `tg` (`@miolamio/tg-cli`, gramjs) is installed nearby, it is a different tool with a different contract: check which of them answers `tg version` (this one has the `revision` field).
