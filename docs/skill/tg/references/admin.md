# Dialogs, folders, sessions, profile

`chat set`, `chat approve` and `chat decline` require the chat in `access.admin`; `chat requests` reads join requests. For `account set` and `account privacy set`, the owner enables `access.profile`. `privacy set` replaces all rules of the key: it needs `--yes` or `--dry-run`; hidden exceptions need `--replace-hidden`.

## Dialogs, folders and sessions

`chat archive|unarchive|pin|unpin|mute|unmute|notify` and the `folder` commands that create or change folders require `access.dialogs: true`. A mute can hide an important notification. `folder delete` and `account terminate` require `--yes` or `--dry-run`; terminating a session also requires `access.sessions: true`. `saved tag-rename <emoji> --title T` (or `--clear`) renames a Saved Messages tag; it requires the owner's own Telegram user id in `access.write`, and the tag is selected by its emoji. Only the owner changes access settings.

## Moderation, invites and blocking

- `chat kick|ban|unban|restrict|permissions|slow-mode` change a group, supergroup or channel. They need the chat in `access.admin`, have `--dry-run` (run it first), and are visible on the Telegram side. `unban` and `slow-mode` work in supergroups and channels; `slow-mode` takes 0, 10, 30, 60, 300, 900 or 3600 seconds.
- `user block` and `user unblock` change the account's block list (class `account_write`); run them only on an explicit request.
- `chat invite-info <link>` and `folder invite-info <link>` only read what an invite link leads to, without joining (class `read`).
