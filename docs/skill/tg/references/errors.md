# Exit codes and what to do

Every command except the streams `message watch` and `message export` prints one envelope to stdout: `{"ok":true,"data":...}` or `{"ok":false,"error":{"code":"<name>","message":"..."}}`. Hints and logs go to stderr. Parse the envelope and the exit code, not the text. This holds for the default JSON format; with `--format jsonl` list commands print one item per line, and `--human` prints text. `tg schema` prints the full list of codes with descriptions (the `exit_codes` block).

| Code | Name | What to do |
|---|---|---|
| 0 | ok | |
| 1 | failure | other error or partial success of a batch (`data.partial: true`, errors in `data.errors`): for a partial batch read `data.errors` and the processed items in the data (there is no top-level `error.message`); retry only items that you know were not processed (for `message forward` check the destination first, see `writing.md`) |
| 2 | usage | wrong arguments, flags or environment: fix the call. Usually Telegram was not called, but a few Telegram rejections are mapped to 2 too (for example `REACTION_INVALID`); the command's own section and `error.message` say which |
| 3 | daemon_not_running | run `tg daemon start` and retry |
| 4 | not_authorized | no login, login lost or `--expect-user` did not match: read `data.authorized` and the profile in `tg daemon status`; the human runs `tg auth login` only when the authorization is missing or lost (`daemon status` can exit 0 while logged out, and a mismatch can happen while logged in) |
| 5 | telegram_error | show the reason from `error.message`; do not retry blindly |
| 6 | flood_wait | Telegram asks to wait, the time is in `error.message`. Do not retry at once and do not loop retries. The tool does not retry a write itself, the decision is yours |
| 7 | not_found | the chat, user, message or note does not exist or is invisible, including when closed by the access lists |
| 8 | cursor_expired | the event cursor is older than the retention limit (7 days or 100,000 events): run `message watch` without `--after` |
| 9 | write_disabled | the owner has not allowed this operation: the chat is not in `write`, or the permission the command needs is missing (`files`, `downloads`, `admin`, `create`, `dialogs`, `sessions`, `profile`). Name only the permission this command needs and ask the owner to add it to `access.json`; do not edit the file yourself. Read `error.message` first: another cause is possible (see By symptom) |
| 10 | offline | Telegram is unavailable or the Daemon is recreating the client: wait and retry; local operations (`tg search`, `message context --local`, `note list`) work |

## By symptom

- **`daemon_not_running` (3).** Run `tg daemon start`. For `message watch`, code 3 means the Daemon is stopped or the connection is closed.
- **`not_authorized` (4).** Check `tg auth status` and `--expect-user`. Only the human logs in: suggest `! tg auth login`. If the Daemon shows `auth_lost`, stop it before importing a session; for a new login do not stop the Daemon (see `references/daemon-auth.md`).
- **`flood_wait` (6).** While the background history load waits out a long pause, any command that calls Telegram gets code 6 at once, with the remaining seconds (unless the Daemon was started with `TG_CLI_FLOOD_AUTOWAIT=0`: then commands queue instead). Wait for the pause to end or work locally. Global search (`message search --public`) quickly hits this code: make one or two probes with a pause.
- **`not_found` (7) for a chat that should exist.** The chat is closed by the access lists (indistinguishable from a nonexistent one), the `@username` did not resolve or the numeric id is unknown. Do not look for the same chat through another link: tell the user.
- **`write_disabled` (9).** Find which permission the command needs (the chat in `write`; a root in `files` for sending files; a root in `downloads` for downloads and archives; `admin`, `create`, `dialogs`, `sessions` or `profile` for the commands that name them). Tell the user exactly that one, and do not ask for unrelated permissions. Read `error.message` too: exit 9 can mean something else, for example a join invite that is not resolved yet (the owner must join the chat first); say that to the user instead of asking for an `access.json` edit.
- **`telegram_error` (5) when reading members.** `CHAT_ADMIN_REQUIRED`: Telegram gives the member list of a channel only to admins. Do not retry and do not work around it.
- **Timeout after a write.** The outcome is unknown: read `message history`, then decide about a retry; use `--idempotency-key` for retries.
- **The export broke off.** The lines before the break are valid, the last stdout line is an error envelope, and stderr has `resume with --after N`. Continue with the same set of filters.
- **The stream returned `gap` or `lagged`.** `gap`: read the history with `message history` and do not treat the feed as complete. `lagged`: code 1; continue from the last cursor you actually processed (the stderr hint `--after <cursor>` is the last cursor delivered to the CLI, which with `--format json` can be ahead of what you saw).

Errors of individual commands (formatting, media, polls, members) are described in the references next to the commands: `writing.md`, `media.md`, `reading.md`, `history.md`, `stream.md`.
