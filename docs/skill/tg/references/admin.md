# Dialogs, folders, sessions, profile

`chat set`, `chat approve` and `chat decline` require the chat in `admin`; `chat requests` reads join requests. For `account set` and `account privacy set`, the owner enables `profile` in `access.json`. `privacy set` replaces all rules of the key: it needs `--yes` or `--dry-run`; hidden exceptions need `--replace-hidden`.

## Dialogs, folders and sessions

`chat archive|unarchive|pin|unpin|mute|unmute|notify` and the `folder` commands that create or change folders require `dialogs: true` in `access.json`. A mute can hide an important notification. `folder delete` and `account terminate` require `--yes` or `--dry-run`; terminating a session also requires `sessions: true`. Renaming a Saved Messages tag requires the owner's own Telegram user id in `write`; the tag is selected by its emoji. Only the human changes the access file.
