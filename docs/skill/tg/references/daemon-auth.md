# Daemon, login, transport, config

One Daemon per profile holds the connection and the session, and the CLI sends it requests. Profiles are independent: `--profile <name>` selects the account, and each has its own Daemon.

## First run

```bash
tg daemon start                    # background; desktop is the default preset when own credentials are absent
tg daemon status                   # profile, connection, login, active requests, background_sync block
tg status                          # text screen for a person (not JSON): account, database, history, background load, jobs
tg daemon logs --tail 50           # log of Daemon requests and errors for debugging (--follow tails it)
tg auth login                      # the human runs this (! tg auth login): number, code, 2FA (or --qr)
tg auth status                     # whether the account is logged in and who it is
```

`tg daemon start` is idempotent: for a profile with a live Daemon it reports that Daemon and does not start a second one. One command checks the environment: `tg daemon status`. Exit code 3 means the Daemon is not running. Exit code 4 means no login, a lost login or an `--expect-user` mismatch: check `data.authorized` and the profile in `tg daemon status` before asking for a login.

**Transport.** Set it at start: `tg daemon start --transport wss` or `tg daemon start --transport auto`. Choose `auto` when TCP may be blocked: it starts with TCP and recreates the client on WSS of the same DC after a connection or handshake error, and also if TCP did not become `ready` within 10 seconds. WSS stays until the Daemon stops. To change the network of a running Daemon, first run `daemon stop`.

**Proxy.** `TG_CLI_PROXY='socks5://...' tg daemon start`. SOCKS5 works with TCP and WSS. An HTTP proxy `http://user:password@host:port` works only with WSS and `auto`; with it `auto` uses WSS at once, and the Basic password is sent to the proxy without TLS. MTProxy cannot be used with WSS and `auto`. The selected profile stores the network settings in its database; `--proxy none` stores no proxy.

**`tg status`.** It prints plain text for a person: the account, the connection, the database size and chat count, how many chats are loaded to their first message (people, in folders, others), the background load, active jobs, the remaining `FLOOD_WAIT` and the events. It is not for parsing: `--format json|jsonl|toon` gives code 2, and `tg daemon status` returns the same facts as JSON. It asks Telegram for nothing and works after login is lost (the line `Account  not signed in`).

**Background load block.** `daemon status` has a `background_sync` block: `state` (`disabled`, `waiting_window`, `waiting_interval`, `running`), `next_run_at`, `last_run_at`, `last_result` and `last_chat_id`. The scheduled load is on by default; `background_sync.enabled` controls it and requires a Daemon restart. Do not change it unless the owner asks. With `--idle-timeout` the Daemon may stop before a run, because waiting for the schedule does not count as activity.

**Semantic search block.** `daemon status` has a `semantic_search` block (always present) and `tg status` a `Semantic` row: `disabled` while `semantic_search.enabled` is off, otherwise the indexer state (`idle`, `indexing`, `unavailable`, `model_missing`, `error`), the model and `indexed` of `total` fragments. `unavailable` means Ollama does not answer and `model_missing` that it lacks the model (`ollama pull bge-m3`); `tg search` keeps working by words in every state. The feature is on by default and a change requires a Daemon restart.

**Status and failures.** `daemon status` shows `connection`, the selected `transport`, the current `active_transport`, the count `transport_switches`, `dc` and the proxy address without the secret. When the client is recreated, a request to Telegram gets code 10 `offline`; a write that was already admitted gets 30 seconds to finish, then its context is canceled, and the Daemon does not retry it itself. On a connection drop, a request to Telegram waits up to 15 seconds and may return code 10 `offline`; local operations remain available.

`daemon start --foreground` keeps the Daemon in the terminal. `--idle-timeout 30m` stops it when idle; by default it runs until `daemon stop`. Your own keys instead of the preset: `TG_API_ID` and `TG_API_HASH` (cannot be combined with `--client`).

Exit code 3: the Daemon is not running, see "Start the Daemon" in `SKILL.md`. Exit code 4: the login is missing or lost, or the expected-account check failed. Check `auth status` and `--expect-user`; if the login is lost, ask the human to run `! tg auth login`.

## Config

Configuration that locates profiles is in `~/.config/tg-cli/config.toml`. `tg config path` shows its path. `tg config show` returns only `home`, `db_dir`, `default_profile` and `files`; it does not open the profile database. `tg settings list` and `tg settings get` show profile settings. `tg config init` writes this reduced template without overwriting an existing file (code 2). `tg config migrate` imports legacy settings once. `home` sets the directory of profile data (session, socket and logs), and `TG_CLI_HOME` overrides it for one call only. `home` defaults to `~/.config/tg-cli`. On macOS the first command moves data from the old default, `~/Library/Application Support/tg-cli`, there when `home` and `TG_CLI_HOME` are unset. Neither `home` nor `TG_CLI_HOME` moves the database. The database directory in `tg config show` is `db_dir`. See profile-setting keys, types, defaults, variables and flags in the `settings` block of `tg schema`.

## Login and session loss

On `auth_lost`, stop the Daemon before importing a session. For a new login, do not stop the Daemon. The human runs `! tg auth login` and enters the code and, if needed, the password. After a successful login the Daemon recreates the client itself and `auth_lost` disappears. Reconnecting with an invalid key may fail.

A session string gives full access: import only a string from the owner, keep it in a file with mode 0600 and run only one process on the key, including other machines. Pass the 2FA password through a file with owner-only permissions.

## Build and install

`just build` builds the binary into `dist/tg`. Put it in `PATH` under the name `tg`. If the old npm `tg` (`@miolamio/tg-cli`, gramjs) is installed nearby, it is a different tool with a different contract: check which of them answers `tg version` (this one has the `revision` field).
