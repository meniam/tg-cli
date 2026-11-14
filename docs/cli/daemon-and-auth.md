# Daemon and login

The Daemon is the background process that holds the Telegram connection and the session of one profile. Login goes through it, so start the Daemon before `tg auth login`. This page describes the four `daemon` commands and the four `auth` commands. The shared rules (profiles, global flags, the envelope, exit codes) are in [overview.md](overview.md).

## The Daemon

### tg daemon start

Starts the Daemon of the profile in the background and waits until it answers on its socket. The background process has no terminal, writes its journal to `<profile>/daemon.log` and keeps running after the command returns.

```text
tg daemon start [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--client` | string | none | official client preset to use instead of your own application, for example desktop |
| `--device` | string | none | device name Telegram shows for this session, remembered for the profile (default MacBook Pro M1 BRN) |
| `--foreground` | bool | `false` | run the daemon in this terminal instead of the background |
| `--idle-timeout` | duration | `0s` | stop the daemon after this long without a request, like 30m; 0 means never |

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** No Daemon (this command starts it) and no login. The command needs application credentials from one source or a remembered preset, and a preset needs network access to download its keys. On a profile whose Daemon already runs no credentials are needed.

**Credentials.** The Daemon needs application credentials from exactly one source:

- `TG_API_ID` and `TG_API_HASH` in the environment: your own application from https://my.telegram.org. The tool's owner has no `api_id` and will not get one, so the project uses presets.
- `--client <preset>`: the credentials of an official client, downloaded from a pinned commit of the [opentele](https://github.com/thedemons/opentele) project and checked against a SHA-256 before use. The available presets are `android`, `android-x`, `desktop`, `ios`, `macos`, `web-k`, `web-z` and `webogram`. The chosen preset is remembered for the profile in `<profile>/client`, so later starts need no flag. Every start that uses a preset downloads it again, so the Daemon needs network access to `raw.githubusercontent.com` then. `TG_API_ID` and `TG_API_HASH` in the environment avoid the download.

Both sources together are exit code 2 (`use either --client or TG_API_ID and TG_API_HASH, not both`). No source at all is exit code 2 too. When a preset is used the command prints a warning on stderr (`--quiet` hides it): according to the opentele documentation, Telegram puts accounts of unofficial clients under observation, and the risk for this tool is not assessed. The credentials never go into the arguments of the background process, where `ps` would show them: the process reads them from its environment or the preset from the profile. Never print or log keys or hashes.

**Device name.** Telegram lists the session in its device list. The default name is `MacBook Pro M1 BRN`. `--device <name>` changes it, and the name is remembered for the profile in `<profile>/device`. The name must be 1 to 64 characters without control characters, otherwise exit code 2 (`the device name must be 1 to 64 characters`, `the device name must not contain control characters`).

**Behavior.**

- The command waits up to 15 seconds for the socket. If the process exits earlier, the command fails and shows the tail of the journal; if the socket does not answer in time, the error names the journal file. Errors of a background start are never hidden by `--quiet`.
- Starting again on a profile whose Daemon is running is not an error: the command answers `running: true` with the PID of the running Daemon, starts nothing, and needs no credentials, because two processes must never share one Telegram session.
- `--foreground` keeps the Daemon in the terminal. Its journal goes to stderr, there is no `daemon.log`, and after the Daemon stops the command returns `{"stopped":true}`. A second `--foreground` Daemon on a profile that already has one is refused (exit code 1, `a daemon is already running for this profile`).
- `--idle-timeout 30m` stops the Daemon after that long without a request. The default is `0`, never. A negative value is exit code 2. A running history load and an active `message watch` count as activity and keep the Daemon alive.
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

**Exit codes.** 0 started or already running; 2 no or two credential sources, a bad `TG_API_ID`, an unknown preset, a bad `--device` name, a negative `--idle-timeout`, a bad `TG_CLI_THROTTLE_PERCENT` or `TG_CLI_FLOOD_AUTOWAIT` (see [Request throttle](overview.md#request-throttle-and-flood_wait)); 1 the process exited early, the socket did not answer in 15 seconds, the preset could not be downloaded, or a `--foreground` Daemon was refused because one already runs.

**Limits and live check.** Checked on a live profile: the start takes about 0.75 seconds, `daemon stop` about 0.15 seconds, an idle timeout of 6 seconds stopped the Daemon after about 7 seconds, `ps` shows no application keys, and the socket and the journal have mode `0600`. Not checked: the background start on Linux, and the journal rotation on a live Daemon. The schema of `daemon start` describes the answer of the background start (`running`, `pid`, `profile`, `socket`, `log`) and does not describe the `--foreground` answer (`stopped`), see [FEATURES.md](../../FEATURES.md) 3.8. `--device` was not checked on a live account.

### tg daemon status

Shows the state of the Daemon and of the account: whether it is connected and logged in, who the user is, how many requests are active, and the state of the chat access lists.

```text
tg daemon status [flags]
```

This command has no flags of its own. The global flags apply.

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. No login.

**Example.**

```sh
tg daemon status
```

**Answer.** A logged-in profile with the read filter on and one chat in `write`:

```json
{"ok":true,"data":{"profile":"default","pid":48213,"connected":true,"authorized":true,"user":{"id":123456789,"username":"me"},"active_requests":0,"access":{"enabled":true,"allow":1,"block":0,"write":1}}}
```

A fresh profile without a login and without `access.json`:

```json
{"ok":true,"data":{"profile":"default","pid":48213,"connected":false,"authorized":false,"active_requests":0}}
```

**Answer fields** (`data`): `profile` string, `pid` integer, `connected` boolean, `authorized` boolean, `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional), `login_state` string (optional), `active_requests` integer, `access` object (optional), `access.enabled` boolean (optional), `access.allow` integer (optional), `access.block` integer (optional), `access.write` integer (optional), `access.closed` boolean (optional).

`login_state` is present while a login is in progress: `qr_scan`, `code_required`, `password_required` or `authorized` (see [auth login](#tg-auth-login)). The `access` object is shown when the read filter is on or the `write` list is not empty. `enabled` tells whether the read filter is on, `allow`, `block` and `write` are counts (`write` is shown when it is not empty), and `closed: true` means that `access.json` could not be read, parsed or has a mode that is too wide, so everything is closed. It never shows chat IDs. See [access.md](access.md).

**Exit codes.** 0; 3 no Daemon is running for the profile.

**Limits and live check.** Checked on a live account.

### tg daemon stop

Asks the Daemon to stop and waits until the process is gone.

```text
tg daemon stop [flags]
```

This command has no flags of its own. The global flags apply.

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

**Limits and live check.** A stop takes about 0.15 seconds on a live profile. A running history load (`chat sync`) is lost: jobs live in the memory of the Daemon and are not resumed on the next start. What the load had saved stays in the database. A stop of the Daemon in the middle of a load or an export was not checked on a live account.

### tg daemon logs

Reads the journal of the profile without a running Daemon. It is the tool for debugging the Daemon: it shows every request and every unexpected error.

```text
tg daemon logs [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--follow` | bool | `false` | print new lines until interrupted, including after rotation |
| `--tail` | int | `0 (all lines)` | last N lines (1 to 10000); default: all lines, up to 10000 |

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

**Limits and live check.** Checked by tests and as a built binary on a temporary profile; not run against a live account. The rotation was not checked on a live Daemon.

## Login

Login runs through the Daemon and is interactive. The login code and the two-factor password are asked on the terminal and are never accepted as flags, so they stay out of the shell history, the argument list and the logs. In Claude Code, ask the user to type `! tg auth login` themselves. A whole login is bounded to 5 minutes (`login timed out`). Phone login, QR login, `auth resend`, `auth cancel` and `--device` were not confirmed end to end on a live account (the code did not arrive during the check; at the QR check Telegram asked for the 2FA password at once).

### tg auth login

Logs the profile in by phone number, login code and two-factor password, or by a QR code.

```text
tg auth login [flags]
```

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--phone` | string | none | phone number in international format; asked when omitted |
| `--qr` | bool | `false` | log in by scanning a QR code in the Telegram app of a logged-in device, no code is needed |

**Safety class:** `account_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon (`tg daemon start`) and a terminal for the prompts. Not logged in yet: a repeated login on a logged-in profile is refused, and so is a second login while one is in progress (cancel it first).

**Steps.**

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

`state` is `qr_scan`, `code_required`, `password_required` or `authorized`. The intermediate states are not printed on stdout: they drive the prompts. `delivery` is `app`, `sms`, `call`, `email` or `other`; `next_delivery` and `resend_after_seconds` say how a resend would arrive and when Telegram allows it; `qr_url` and `expires_in_seconds` come with `qr_scan`.

**Exit codes.** 0 logged in; 1 the login was interrupted or the login state does not fit; 2 an unreadable input; 3 no Daemon; 5 Telegram rejected the step (a wrong code ends the login with the reason of Telegram, start again).

**Limits and live check.** Not checked on a live account: phone login end to end (the code did not arrive), QR drawing and scanning. A wrong `api_id` gives `telegram_error` `API_ID_INVALID` at the first step.

### tg auth status

Shows whether the profile is logged in, and as whom.

```text
tg auth status [flags]
```

This command has no flags of its own. The global flags apply.

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.


**Preconditions.** A running Daemon. No login.

**Example.**

```sh
tg auth status
```

**Answer.**

```json
{"ok":true,"data":{"profile":"default","connected":true,"authorized":true,"user":{"id":123456789,"username":"me"}}}
```

**Answer fields** (`data`): `profile` string, `connected` boolean, `authorized` boolean, `user` object (optional), `user.id` integer (optional), `user.username` string (optional), `user.first_name` string (optional), `user.last_name` string (optional), `login_state` string (optional).

**Exit codes.** 0; 3 no Daemon.

**Limits and live check.** Checked on a live account: the answer showed that the account was logged in, the connection state and the account id.

### tg auth resend

Asks Telegram to send the login code another way, for example by SMS. Run it from a second terminal while `auth login` waits for the code, then type the new code in the first one. Typing `resend` at the code prompt does the same.

```text
tg auth resend [flags]
```

This command has no flags of its own. The global flags apply.

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

**Limits and live check.** Not checked on a live account.

### tg auth cancel

Cancels a login that waits for a code or a password.

```text
tg auth cancel [flags]
```

This command has no flags of its own. The global flags apply.

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

**Limits and live check.** Not checked on a live account.
