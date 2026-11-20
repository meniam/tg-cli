# Daemon and login

The Daemon is the background process that holds the Telegram connection and the session of one profile. Login goes through it, so start the Daemon before `tg auth login`. This page describes the four `daemon` commands and the four `auth` commands. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md).

## The Daemon

### tg daemon start

Starts the Daemon of the profile in the background and waits until it answers on its socket. The background process has no terminal, writes its journal to the configured profile log and keeps running after the command returns.

```text
tg daemon start [flags]
```

<!-- schema:flags tg daemon start -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--client` | string | `none` | official client preset to use instead of your own application, for example desktop |
| `--device` | string | `none` | device name Telegram shows for this session, remembered for the profile (default MacBook Pro M1 BRN) |
| `--foreground` | bool | `false` | run the daemon in this terminal instead of the background |
| `--idle-timeout` | duration | `0s` | stop the daemon after this long without a request, like 30m; 0 means never |
| `--proxy` | string | `none` | SOCKS5, HTTP or Telegram proxy URL, or none (remembered for the profile) |
| `--transport` | string | `none` | Telegram transport: tcp, wss or auto (remembered for the profile) |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** No Daemon (this command starts it) and no login. The command needs application credentials from one source or a remembered preset, and a preset needs network access to download its keys. On a profile whose Daemon already runs no credentials are needed.

**Credentials.** The Daemon needs application credentials from exactly one source:

- `TG_API_ID` and `TG_API_HASH` in the environment: your own application from https://my.telegram.org. The tool's owner has no `api_id` and will not get one, so the project uses presets.
- `--client <preset>`: the credentials of an official client, downloaded from a pinned commit of the [opentele](https://github.com/thedemons/opentele) project and checked against a SHA-256 before use. The available presets are `android`, `android-x`, `desktop`, `ios`, `macos`, `web-k`, `web-z` and `webogram`. The chosen preset is stored in `[profiles.<name>]` in `config.toml`, so later starts need no flag. Every start that uses a preset downloads it again, so the Daemon needs network access to `raw.githubusercontent.com` then. `TG_API_ID` and `TG_API_HASH` in the environment avoid the download.

Both sources together are exit code 2 (`use either --client or TG_API_ID and TG_API_HASH, not both`). No source at all is exit code 2 too. When a preset is used the command prints a warning on stderr (`--quiet` hides it): according to the opentele documentation, Telegram puts accounts of unofficial clients under observation, and the risk for this tool is not assessed. The credentials never go into the arguments of the background process, where `ps` would show them: the process reads them from its environment or the preset from the profile. Never print or log keys or hashes.

**Network.** `--transport tcp|wss|auto` selects the transport, TCP by default. `auto` is an explicit choice: it starts with TCP, recreates the client on WSS for the same DC after a TCP connection or transport-handshake error, and does the same if TCP has not become ready within ten seconds. WSS remains selected until the Daemon stops. `--proxy` accepts `socks5://[user:password@]host:port`, `http://[user:password@]host:port`, `tg://proxy?server=...&port=...&secret=...`, `https://t.me/proxy?...`, or `none` to clear it. HTTP proxies work only with WSS or auto and use HTTP CONNECT for WSS; auto with an HTTP proxy starts on WSS. HTTP Basic proxy credentials travel to the proxy without TLS. SOCKS5 works with TCP, WSS and both branches of auto. MTProxy cannot be combined with WSS or auto. `TG_CLI_PROXY` supplies the proxy; the flag takes precedence. The profile stores both settings in `[profiles.<name>]` in `config.toml`; a created file has mode `0600`, a file writable by group or others is rejected, and a group- or world-readable file containing proxy credentials causes a stderr warning. A proxy passed on the command line may be visible in shell history and process listings; use the environment variable to avoid that. The background process reloads the configuration rather than receiving the proxy in its arguments. HTTP proxy environment variables are not used for Telegram connections. A different setting for a running Daemon is rejected; stop it first. WSS media-only and CDN connections are not supported by the resolver.

**Device name.** Telegram lists the session in its device list. When no configured or flagged name is present, the tool uses `MacBook Pro M1 BRN`. `--device <name>` changes it and stores the name in `[profiles.<name>]` in `config.toml`. The name must be 1 to 64 characters without control characters, otherwise exit code 2 (`the device name must be 1 to 64 characters`, `the device name must not contain control characters`).

**Behavior.**

- The command waits up to 15 seconds for the socket. If the process exits earlier, the command fails and shows the tail of the journal; if the socket does not answer in time, the error names the journal file. Errors of a background start are never hidden by `--quiet`.
- Starting again on a profile whose Daemon is running is not an error: the command answers `running: true` with the PID of the running Daemon, starts nothing, and needs no credentials, because two processes must never share one Telegram session.
- `--foreground` keeps the Daemon in the terminal. Its journal goes to stderr, there is no `daemon.log`, and after the Daemon stops the command returns `{"stopped":true}`. A second `--foreground` Daemon on a profile that already has one is refused (exit code 1, `a daemon is already running for this profile`).
- `--idle-timeout 30m` stops the Daemon after that long without a request. The default is `0`, never. A negative value is exit code 2. A running history load and an active `message watch` count as activity and keep the Daemon alive. The wait of the [scheduled history load](#tg-daemon-status) for its next run is not activity: with `--idle-timeout` set and `[background_sync] enabled = true` the Daemon can stop before the window opens or before a run is due, and no scheduled load happens. Leave the flag at `0` for a Daemon that has to run scheduled loads.
- The background process is the same binary started again with `--foreground` and a hidden `--journal <path>` flag.

**Example.**

```sh
tg daemon start --client desktop
TG_API_ID=12345 TG_API_HASH=0123456789abcdef0123456789abcdef tg daemon start --idle-timeout 30m
```

**Answer.** The background start prints a hint on stderr (`daemon is running in the background, journal: <path>`) and this answer on stdout:

```json
{"ok":true,"data":{"running":true,"pid":48213,"profile":"default","socket":"/.../default/daemon.sock","log":"/.../default/daemon.log"}}
```

**Answer fields** (`data`): `running` boolean, `pid` integer (optional), `profile` string, `socket` string, `log` string.

**Exit codes.** 0 started or already running; 2 no or two credential sources, a bad `TG_API_ID`, an unknown preset, a bad `--device` name, a negative `--idle-timeout`, a bad `TG_CLI_THROTTLE_PERCENT`, `TG_CLI_FLOOD_AUTOWAIT` or `TG_CLI_BACKGROUND_SYNC_*` value (see [Request throttle](overview.md#request-throttle-and-flood_wait)), a database that belongs to another data directory, or a database that exists in two places (see [Profiles and the data directory](overview.md#profiles-and-the-data-directory)); 1 the process exited early, the socket did not answer in 15 seconds, the preset could not be downloaded, a `--foreground` Daemon was refused because one already runs, the database move failed (`the database is in use`, `data was written during the copy`, a failed checksum or integrity check), or another data directory already holds the database (`the profile database is in use`).

**Verification.** The schema describes the background start result; `--foreground` returns `stopped`. See [FEATURES.md](../../FEATURES.md), item 2.12 and 3.8.

### tg daemon status

Shows the state of the Daemon and of the account: whether it is connected and logged in, who the user is, how many requests are active, the state of the chat access lists and the state of the scheduled history load. For a text overview of all of this, with the database and the jobs, use [`tg status`](#tg-status).

```text
tg daemon status [flags]
```

<!-- schema:flags tg daemon status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. No login.

If `auth_lost` is present, account commands return exit code 4 with the loss kind. Run `tg auth login` again in the same Daemon; after a successful login it recreates its Telegram client and `auth_lost` disappears. Stop the Daemon before importing a replacement session.

**Example.**

```sh
tg daemon status
```

**Answer.** A logged-in profile with the read filter on and one chat in `write`:

```json
{"ok":true,"data":{"transport":"auto","active_transport":"wss","transport_switches":1,"connection":"ready","dc":2,"reconnects":0,"profile":"default","pid":48213,"connected":true,"authorized":true,"user":{"id":123456789,"username":"me"},"active_requests":0,"access":{"enabled":true,"allow":1,"block":0,"write":1},"background_sync":{"enabled":false,"window":{"start":"08:00","end":"20:00"},"state":"disabled","next_run_at":null,"last_run_at":null,"last_result":"","last_chat_id":null,"current_chat_id":null}}}
```

A fresh profile without a login and without `access.json`:

```json
{"ok":true,"data":{"transport":"tcp","transport_switches":0,"connection":"connecting","reconnects":0,"profile":"default","pid":48213,"connected":false,"authorized":false,"active_requests":0,"background_sync":{"enabled":false,"window":{"start":"08:00","end":"20:00"},"state":"disabled","next_run_at":null,"last_run_at":null,"last_result":"","last_chat_id":null,"current_chat_id":null}}}
```

**Answer fields** (`data`): `transport` string, `active_transport` string (optional, `tcp` or `wss` after a connection), `transport_switches` integer, `proxy` string (optional, without credentials), `connection` string, `dc` integer (optional), `reconnects` integer, `profile` string, `pid` integer, `connected` boolean, `authorized` boolean, `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional), `login_state` string (optional), `auth_lost` string (optional), `active_requests` integer, `access` object (optional), `access.enabled` boolean (optional), `access.allow` integer (optional), `access.block` integer (optional), `access.write` integer (optional), `access.closed` boolean (optional), `background_sync` object, `background_sync.enabled` boolean, `background_sync.window` object, `background_sync.window.start` string, `background_sync.window.end` string, `background_sync.state` string, `background_sync.next_run_at` integer or null, `background_sync.last_run_at` integer or null, `background_sync.last_result` string, `background_sync.last_chat_id` integer or null, `background_sync.current_chat_id` integer or null.

`connection` is `connecting`, `ready`, `disconnected`, `restarting` or `offline`; `connected` is true when it is `ready`. During a client restart, Telegram operations return exit code 10 (`offline`), while requests admitted before the restart are given 30 seconds to finish before their contexts are cancelled. While offline, the last cached account is shown without a Telegram request. A Telegram request waits up to 15 seconds for a connection and then returns exit code 10 (`offline`). Local operations remain available.

`login_state` is present while a login is in progress: `qr_scan`, `code_required`, `password_required` or `authorized` (see [auth login](#tg-auth-login)). The `access` object is shown when the read filter is on, `write` or `admin` is nonempty, or `create`, `profile`, `dialogs` or `sessions` is enabled. `enabled` tells whether the read filter is on, `allow`, `block` and `write` are counts (`write` is shown when it is not empty), and `closed: true` means that `access.json` could not be read, parsed or has a mode that is too wide, so everything is closed. It never shows chat IDs. See [access.md](access.md).

**The `background_sync` block.** It is always present and describes the scheduled history load: while `[background_sync] enabled = true`, the Daemon loads older history of one chat per run, in a daily window (see [`[background_sync]`](overview.md#request-throttle-and-flood_wait) for the settings). `enabled` repeats the setting, and `window.start` and `window.end` are the local times of the Daemon machine, `HH:MM`. `state` is one of four values:

| `state` | Meaning |
| --- | --- |
| `disabled` | The scheduler is off, the default. The Daemon makes no request for it; `window` still shows the configured times, and the run fields are `null` or empty. |
| `waiting_window` | It is on and the clock is outside the window; the next run is at the window start or later. |
| `waiting_interval` | It is on, the clock is inside the window, and the next run is not due yet. |
| `running` | A run is in progress. It starts with the refresh of the folder snapshot when that snapshot is missing or older than 4 hours, so `running` can hold for a while before any chat is picked. |

`next_run_at` is a Unix time in seconds; it is `null` when the state is `disabled` or `running`. After a Daemon start the first run is a random 40 to 180 minutes away (with the default settings), and each later run is counted from the end of the previous one and moved to the window start when it would fall outside the window. `last_run_at` is the Unix time the last run ended, `null` before the first run. `last_result` is `done` (the chat was loaded to its first message), `cancelled` (the window ended or a manual load took over), `failed` (the load ended with an error; the chat is not picked again until the next local day) or `skipped` (nothing was loaded: no chat left to pick, no login, a busy history manager, or the window ended during the folder refresh); it is an empty string before the first run. `last_chat_id` is the chat of the last run that picked one, `null` until then; a `skipped` run that picked none leaves the earlier value unchanged, so read it together with `last_result`. `current_chat_id` is the chat being loaded; it is `null` while no chat is loading, also during the folder refresh. The scheduler state is kept in memory: after a Daemon restart the schedule starts again. An active `chat sync` job of a chat shows its `source` in [`chat sync status`](chats.md#tg-chat-sync-status).

**Exit codes.** 0; 3 no Daemon is running for the profile.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 12.1.

### tg status

Prints a text dashboard for a person: what the Daemon is doing, who the account is, how full the local database is and how far the history load has come. The Daemon collects the numbers in one request (the `dashboard` method) that only reads: it makes no Telegram request, and the answer has chat IDs but no chat title or message text. The command prints the answer and adds nothing of its own.

```text
tg status [flags]
```

<!-- schema:flags tg status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `human`.

**Preconditions.** A running Daemon. No login and no connection: after a lost login the command still prints what the Daemon knows, and the `Account` row says `not signed in`.

The output is text and has no JSON form: an explicit `--format json`, `jsonl` or `toon` exits with code 2 before the Daemon is asked, `--human` and `--format human` are accepted and change nothing. For the same facts as JSON use [daemon status](#tg-daemon-status).

**Example.**

```sh
tg status
```

**Output.**

```text
tg status  profile default  daemon pid 80307, up 3d 4h

Account      @name (id 114954417), signed in
Connection   ready, tcp, dc 2, reconnects 0
Access       lists off, writes allowed in 1 chat
Database     107.3 MB, schema 9, 245 chats, 67583 messages
History      4 of 245 chats loaded to the first message
             people 3 of 27 | in folders 0 of 12 | others 1 of 206
Background   on, window 08:00-20:00, waiting for the next run
             next run 14:35, last run 11:02 (chat 123, done)
Jobs         chat sync: none | chat full: none
Throttle     no flood wait, 0 queued
Events       12 in the last hour, 71052 stored
```

**How to read the blocks.**

- **Header.** The profile, the PID of the Daemon and its uptime (`3d 4h`, `4h 12m`, `1m 30s`).
- **Account.** `@username (id N), signed in`, or the name when the account has no username. `not signed in` when nobody is logged in or the login was lost.
- **Connection.** The state (`ready`, `connecting`, `disconnected`, `restarting` or `offline`), the transport (`auto (using wss)` when the Daemon picked it), the data center and the number of reconnects. `unavailable` when the client reports nothing.
- **Access.** `lists off`, or `lists on (allow N, block M)` when the read filter is on, or `access file unreadable, every chat closed`; then `writes off` or `writes allowed in N chats`. The counts are those of [`daemon status`](#tg-daemon-status), and no ID is shown.
- **Database.** The size of the `store.db` file in decimal megabytes (the write-ahead log is not counted), the schema version, and the number of chats and of messages that the access lists show. A closed chat is in no count; a message marked deleted is not counted either.
- **History.** How many of the visible chats are loaded to their first message (`complete`), in total and in three groups: people (type `user`), chats the owner pinned or included in a folder, and the others, bots included. The groups are the ones the scheduled load picks from, in that order.
- **Background.** The scheduled history load: `off (enable with [background_sync] enabled = true)`, or `on`, the window and the state (`waiting for the window to open`, `waiting for the next run`, `running`). The second line has the next run and the last run with its chat and result (`done`, `cancelled`, `failed` or `skipped`; a `skipped` run names no chat), or `no run yet`. The fields are those of the `background_sync` block of [`daemon status`](#tg-daemon-status).
- **Jobs.** The active `chat sync` jobs, each with its chat, state, source (`manual` or `background`), pages and loaded messages, and the state of `chat full` while that job runs; `none` when idle.
- **Throttle.** The rest of the account-wide `FLOOD_WAIT` pause, and the number of requests that wait in the throttle queue.
- **Events.** The events of the stream stored in the last hour and in the whole `events` table.

Clock times are in the local time of the machine that runs `tg status`: `HH:MM` for today, the date and time for another day.

**Exit codes.** 0; 2 a JSON format was requested; 3 no Daemon is running for the profile: stdout stays empty, and stderr says `daemon is not running` and `start it with tg daemon start` (with `--profile <name>` added when another profile was chosen); a Daemon that does not know the `dashboard` method, such as an older one, answers with an error envelope.

**Verification.** The text is checked against golden files and the method against a fake Telegram client in tests; it was not run on a live account. See [FEATURES.md](../../FEATURES.md), item 12.4.

### tg daemon stop

Asks the Daemon to stop and waits until the process is gone.

```text
tg daemon stop [flags]
```

<!-- schema:flags tg daemon stop -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. No login.

**Example.**

```sh
tg daemon stop
```

**Answer.**

```json
{"ok":true,"data":{"stopping":true}}
```

**Answer fields** (`data`): `stopping` boolean.

**Exit codes.** 0; 3 no Daemon is running; the command fails when the process is still alive after 10 seconds.

**Verification.** Stopping the Daemon loses an in-memory history sync job; saved messages remain. See [FEATURES.md](../../FEATURES.md), item 2.3 and 10.12.

### tg daemon logs

Reads the journal of the profile without a running Daemon. It is the tool for debugging the Daemon: it shows every request and every unexpected error.

```text
tg daemon logs [flags]
```

<!-- schema:flags tg daemon logs -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--follow` | bool | `false` | print new lines until interrupted, including after rotation |
| `--tail` | int | `0` | last N lines, 1 to 10000; 0 means up to 10000 lines |
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.


**Preconditions.** No Daemon and no login. The journal file `<profile>/daemon.log` must exist, which means a Daemon was started in the background on this profile.

**What the journal holds.**

- One `request` record per request at the Info level: the registered method (an unknown method is logged as `unknown`), `duration_ms` and `code` (`ok` or the numeric JSON-RPC code). It is written on success, on error and on a handler panic, and never holds request parameters, message text or error text.
- For an unexpected handler error (not an RPC error, a timeout or a cancel) a separate Error record `handler failed` keeps the method and the cause (`err`), and a panic adds a record `handler panic` with the stack (`stack`). These records carry no request parameters or results.
- Each request that checks access writes at least one Error record `access policy unavailable` when `access.json` cannot be used, without the file content.
- A background Daemon always logs at the Info level and writes to `<profile>/daemon.log`, mode `0600`, with rotation by size at 10 MiB and three archive copies `daemon.log.1` to `daemon.log.3`. `daemon start --foreground` writes to stderr and has no file, and `--quiet` hides the request records there. The stderr that the background process inherits keeps pointing to the renamed copy after a rotation, so it only carries what was printed before the journal opened, and panics.

**Behavior.** `--tail N` selects the last N lines (1 to 10000). Without it the command prints all lines of the current file, or the last 10000 when there are more. `--follow` prints new lines until Ctrl-C and keeps going after a rotation; in `json` each update is a separate result. Archive copies (`.1` to `.3`) are not read. With `--format jsonl` each journal line is its own JSON string.

**Example.**

```sh
tg daemon logs --tail 50
tg daemon logs --follow --format jsonl
```

**Answer.**

```json
{"ok":true,"data":{"lines":["time=2026-11-14T21:33:37.655+03:00 level=INFO msg=\"daemon started\" profile=default socket=/.../default/daemon.sock","time=2026-11-14T21:33:37.688+03:00 level=INFO msg=request method=status duration_ms=0 code=ok"]}}
```

**Answer fields** (`data`): `lines` array.

**Exit codes.** 0; 2 `--tail` outside 1 to 10000 (`--tail 0` included: `--tail must be between 1 and 10000`); 7 no journal file (`daemon journal does not exist: <path>`).

**Verification.** See [FEATURES.md](../../FEATURES.md), item 12.3.

## Login

Login runs through the Daemon and is interactive. The login code and two-factor password are asked on the terminal, never as flags, so they stay out of shell history, arguments and logs. In Claude Code, ask the user to type `! tg auth login` themselves. A whole login is bounded to 5 minutes (`login timed out`). See [FEATURES.md](../../FEATURES.md), items 1.1 and 15.1.

### tg auth login

Logs the profile in by phone number, login code and two-factor password, or by a QR code.

```text
tg auth login [flags]
```

<!-- schema:flags tg auth login -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--no-input` | bool | `false` | do not prompt for login credentials |
| `--password-file` | string | `none` | read the two-factor password from an owner-only file |
| `--phone` | string | `none` | phone number in international format; asked when omitted |
| `--qr` | bool | `false` | log in by scanning a QR code in the Telegram app of a logged-in device, no code is needed |
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon (`tg daemon start`); a terminal is needed for interactive prompts. Not logged in yet: a repeated login on a logged-in profile is refused, and so is a second login while one is in progress (cancel it first).

**Steps.**

With `--no-input`, specify `--phone` or `--qr`. Submit the code with `tg auth code`, and, when needed, submit the password with `tg auth password`. The login expires five minutes after it starts. `--password-file` supplies the password without a prompt.

1. Phone login. Without `--phone` the command asks for the number in international format. Telegram sends a code and the command says where it went: the Telegram app on another device, SMS, a phone call or e-mail. Then it asks for the code. Typing `resend` at the code prompt asks Telegram for another delivery, as `tg auth resend` does. When the account has two-factor authentication, it asks for the password.
2. QR login (`--qr`). No code is needed. The command draws a QR code on stderr (and prints its URL; `NO_COLOR` turns the colors off, `--quiet` hides the drawing). Scan it in the Telegram app of a logged-in device: Settings, Devices, Link Desktop Device. The code refreshes by itself.
3. On success the command prints the result and the progress line `Logged in as ...` on stderr.

Ctrl-C at a prompt ends the login and cancels it in the Daemon (exit code 1, `interrupted, the login was canceled`). An input that cannot be read is exit code 2.

**Example.**

```sh
tg auth login --phone +15551234567
tg auth login --qr
```

**Answer.** After the last step:

```json
{"ok":true,"data":{"state":"authorized","user":{"id":123456789,"username":"me","first_name":"Alice"}}}
```

**Answer fields** (`data`): `state` string, `delivery` string (optional), `next_delivery` string (optional), `resend_after_seconds` integer (optional), `qr_url` string (optional), `expires_in_seconds` integer (optional), `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional).

`state` is `qr_scan`, `code_required`, `password_required` or `authorized`. With `--no-input`, `code_required` and `password_required` are returned on stdout for a later command; otherwise intermediate states drive the prompts. `delivery` is `app`, `sms`, `call`, `email` or `other`; `next_delivery` and `resend_after_seconds` say how a resend would arrive and when Telegram allows it; `qr_url` and `expires_in_seconds` come with `qr_scan`.

**Exit codes.** 0 logged in; 1 the login was interrupted or the login state does not fit; 2 an unreadable input; 3 no Daemon; 5 Telegram rejected the step (a wrong code ends the login with the reason of Telegram, start again).

**Verification.** A wrong `api_id` returns `telegram_error` `API_ID_INVALID` at the first step. See [FEATURES.md](../../FEATURES.md), item 1.1 and 15.1.

### tg auth status

Shows whether the profile is logged in, and as whom.

```text
tg auth status [flags]
```

<!-- schema:flags tg auth status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. No login. When `auth_lost` is present, follow the recovery steps in [daemon status](#tg-daemon-status).

**Example.**

```sh
tg auth status
```

**Answer.**

```json
{"ok":true,"data":{"profile":"default","connected":true,"authorized":true,"user":{"id":123456789,"username":"me"}}}
```

**Answer fields** (`data`): `profile` string, `connected` boolean, `authorized` boolean, `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional), `login_state` string (optional), `auth_lost` string (optional).

**Exit codes.** 0; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 1.11.

### tg auth resend

Asks Telegram to send the login code another way, for example by SMS. Run it from a second terminal while `auth login` waits for the code, then type the new code in the first one. Typing `resend` at the code prompt does the same.

```text
tg auth resend [flags]
```

<!-- schema:flags tg auth resend -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon and a login that waits for a code.

**Example.**

```sh
tg auth resend
```

**Answer.**

```json
{"ok":true,"data":{"state":"code_required","delivery":"sms","resend_after_seconds":60}}
```

The example values are illustrative.

**Answer fields** (`data`): `state` string, `delivery` string (optional), `next_delivery` string (optional), `resend_after_seconds` integer (optional), `qr_url` string (optional), `expires_in_seconds` integer (optional), `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional).

**Exit codes.** 0; 1 the login is not waiting for a code (`login is not waiting for the code; the current state is none`); 3 no Daemon; 5 Telegram refused the resend.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 1.12.

### tg auth cancel

Cancels a login that waits for a code or a password.

```text
tg auth cancel [flags]
```

<!-- schema:flags tg auth cancel -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. Nothing to cancel is not an error.

**Example.**

```sh
tg auth cancel
```

**Answer.** With no login in progress:

```json
{"ok":true,"data":{"canceled":false}}
```

**Answer fields** (`data`): `canceled` boolean.

**Exit codes.** 0; 3 no Daemon.

**Verification.** See [FEATURES.md](../../FEATURES.md), item 1.10.

### tg auth code

Submit a login code from standard input for a login waiting for a code. If a password is requested, `--password-file` submits it in the same command.

<!-- schema:flags tg auth code -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--password-file` | string | `none` | read the two-factor password from an owner-only file |
<!-- /schema:flags -->

### tg auth password

Submit the two-factor password from an owner-only file, standard input, or a terminal without echo. Select `--password-stdin` when using standard input. File permissions must exclude group and other users.

<!-- schema:flags tg auth password -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--password-file` | string | `none` | read the password from an owner-only file |
| `--password-stdin` | bool | `false` | read the password from stdin |
<!-- /schema:flags -->
