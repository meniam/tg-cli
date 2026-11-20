# Daemon, login, transport, config

One Daemon per profile holds the connection and the session, and the CLI sends it requests. Profiles are independent: `--profile <name>` selects the account, and each has its own Daemon.

## First run

```bash
tg daemon start --client desktop   # background; the preset is saved in config.toml, no flag needed afterwards
tg daemon status                   # profile, connection, login, active requests, background_sync block
tg status                          # text screen for a person (not JSON): account, database, history, background load, jobs
tg daemon logs --tail 50           # log of Daemon requests and errors for debugging (--follow tails it)
tg auth login                      # the human runs this (! tg auth login): number, code, 2FA (or --qr)
tg auth status                     # whether the account is logged in and who it is
```

`tg daemon start` is idempotent: for a profile with a live Daemon it reports that Daemon and does not start a second one. One command checks the environment: `tg daemon status`. Exit code 3 means the Daemon is not running. Exit code 4 means no login, a lost login or an `--expect-user` mismatch: check `data.authorized` and the profile in `tg daemon status` before asking for a login.

**Transport.** Set it at start: `tg daemon start --transport wss` or `tg daemon start --transport auto`. Choose `auto` when TCP may be blocked: it starts with TCP and recreates the client on WSS of the same DC after a connection or handshake error, and also if TCP did not become `ready` within 10 seconds. WSS stays until the Daemon stops. To change the network of a running Daemon, first run `daemon stop`.

**Proxy.** `TG_CLI_PROXY='socks5://...' tg daemon start`. SOCKS5 works with TCP and WSS. An HTTP proxy `http://user:password@host:port` works only with WSS and `auto`; with it `auto` uses WSS at once, and the Basic password is sent to the proxy without TLS. MTProxy cannot be used with WSS and `auto`. Settings are saved in `[profiles.<name>]` of `config.toml`; `--proxy none` removes the proxy.

**`tg status`.** It prints plain text for a person: the account, the connection, the database size and chat count, how many chats are loaded to their first message (people, in folders, others), the background load, active jobs, the remaining `FLOOD_WAIT` and the events. It is not for parsing: `--format json|jsonl|toon` gives code 2, and `tg daemon status` returns the same facts as JSON. It asks Telegram for nothing and works after login is lost (the line `Account  not signed in`).

**Background load block.** `daemon status` has a `background_sync` block: `state` (`disabled`, `waiting_window`, `waiting_interval`, `running`), `next_run_at`, `last_run_at`, `last_result` and `last_chat_id`. The scheduled load is the owner's decision: it is off by default, the `[background_sync]` section of `config.toml` turns it on, and you do not turn it on or edit the config yourself. With `--idle-timeout` the Daemon may stop before a run, because waiting for the schedule does not count as activity.

**Semantic search block.** `daemon status` has a `semantic_search` block (always present) and `tg status` a `Semantic` row: `disabled` while `[semantic_search]` is off, otherwise the indexer state (`idle`, `indexing`, `unavailable`, `model_missing`, `error`), the model and `indexed` of `total` fragments. `unavailable` means Ollama does not answer and `model_missing` that it lacks the model (`ollama pull bge-m3`); `tg search` keeps working by words in every state. Turning the feature on is the owner's decision (`[semantic_search] enabled = true` in `config.toml`, then restart the Daemon).

**Status and failures.** `daemon status` shows `connection`, the selected `transport`, the current `active_transport`, the count `transport_switches`, `dc` and the proxy address without the secret. When the client is recreated, a request to Telegram gets code 10 `offline`; a write that was already admitted gets 30 seconds to finish, then its context is canceled, and the Daemon does not retry it itself. On a connection drop, a request to Telegram waits up to 15 seconds and may return code 10 `offline`; local operations remain available.

`daemon start --foreground` keeps the Daemon in the terminal. `--idle-timeout 30m` stops it when idle; by default it runs until `daemon stop`. Your own keys instead of the preset: `TG_API_ID` and `TG_API_HASH` (cannot be combined with `--client`).

Exit code 3: the Daemon is not running, see "Start the Daemon" in `SKILL.md`. Exit code 4: the login is missing or lost, or the expected-account check failed. Check `auth status` and `--expect-user`; if the login is lost, ask the human to run `! tg auth login`.

## Config

Settings are in `~/.config/tg-cli/config.toml`. `tg config path` shows the path, Do not run `tg config show` for diagnosis: it first migrates the old files `client`, `device`, `transport`, `proxy` (it may create a backup, update `config.toml` and remove the old files). Use `tg config path` and `tg daemon status` for read-only diagnosis. `tg config show` shows the file values with defaults and without the proxy secret. `tg config init` only writes a template with all keys in comments (it does not overwrite an existing file, code 2). Explicit migration: `tg config migrate`. `home` sets the directory of profile data (session, socket, logs, `access.json`), and `TG_CLI_HOME` overrides it for a single call only. Neither `home` nor `TG_CLI_HOME` moves the database. The database directory in the `tg config show` response is the `db_dir` field. See all keys, types, defaults, variables and flags in the `config` block of `tg schema`.

## Login and session loss

On `auth_lost`, stop the Daemon before importing a session. For a new login, do not stop the Daemon. The human runs `! tg auth login` and enters the code and, if needed, the password. After a successful login the Daemon recreates the client itself and `auth_lost` disappears. Reconnecting with an invalid key may fail.

A session string gives full access: import only a string from the owner, keep it in a file with mode 0600 and run only one process on the key, including other machines. Pass the 2FA password through a file with owner-only permissions.

## Build and install

`just build` builds the binary into `dist/tg`. Put it in `PATH` under the name `tg`. If the old npm `tg` (`@miolamio/tg-cli`, gramjs) is installed nearby, it is a different tool with a different contract: check which of them answers `tg version` (this one has the `revision` field).
