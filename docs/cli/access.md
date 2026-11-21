# Access settings

The access policy is stored in the selected profile database. It decides which
chats the agent may see, where it may write, and which local directories it may
use for sending and downloading files. The rules apply to every Daemon request.
The related commands are described in [chats.md](chats.md),
[messages-read.md](messages-read.md), and [messages-write.md](messages-write.md).

## The owner passphrase

Only the owner can change `access.*` settings. First create the passphrase with
`tg settings token init`; it reads a new phrase twice without echo and succeeds
only while no phrase hash exists. `tg settings token change` requires the old
phrase and reads the new one twice. Neither command accepts a phrase as an
argument or prints it.

When the Daemon is stopped, `tg settings token reset` asks in a terminal for
the literal confirmation `yes`, removes the hash under the profile and database
locks, and tells the owner to run `tg settings token init`. It has no RPC
method. A running Daemon or a busy profile database makes the reset fail.

A missing, wrong, or temporarily locked phrase rejects an access change. It does
not change the current policy: an empty policy still leaves reads unrestricted.

## Access keys

Use `tg settings list --prefix access.` to inspect the keys. Without
`--show-ids`, it returns counts for `access.allow` and `access.block`, and only
ids still visible under the effective policy for `access.write` and
`access.admin`. To return all ids from any of those four lists, add
`--show-ids` and enter the owner passphrase in the terminal. Other access
values may be read normally. Change scalar values with `set` and `unset`; change
lists with `add` and `remove`. Every `access.*` write prompts for the owner
phrase.

| Key | Value | Effect |
| --- | --- | --- |
| `access.allow` | list of nonzero chat ids | a nonempty list allows only its chats |
| `access.block` | list of nonzero chat ids | blocks chats; it wins over `allow` |
| `access.write` | list of nonzero chat ids | permits writes in visible chats |
| `access.admin` | list of nonzero chat ids | permits administration in visible chats |
| `access.files` | list of absolute directories | permits files under these roots for `message send-media` |
| `access.downloads` | list of absolute directories | permits media downloads under these roots |
| `access.dialogs` | boolean | permits dialog and folder changes |
| `access.sessions` | boolean | permits terminating another session |
| `access.create` | boolean | permits chat creation |
| `access.profile` | boolean | permits own-profile and privacy changes |

Lists have no duplicates. Chat ids use the form printed by `tg chat list`:
users are positive, groups are negative, and channels begin with
`-1000000000000`. An empty policy leaves reading unrestricted but keeps every
write and file permission off.

## Closed by default on errors

The Daemon publishes a policy snapshot from the database at start and after an
`access.*` write. A failed database read, an invalid stored setting, or a
legacy configuration source that appears after migration publishes a denied
policy. A closed chat is indistinguishable from an unknown chat: commands
return exit code 7 and do not reveal its existence. Closed chats are neither
stored nor delivered in events.

## `daemon status`

`tg daemon status` returns an `access` object with whether the read filter is
enabled, counts for the allow, block, write, and admin lists, the boolean
permissions, and `closed`. `tg status` summarizes the same state in its Access
row. Neither command exposes chat ids. `closed` means the effective policy
denies access because its source could not be used.

## Legacy migration

Older installations kept the policy in `access.json` next to the session. On
the first settings migration, its validated contents move into the profile
database, the original is copied into a private migration backup, and the
legacy file is removed. A legacy file that appears after migration is not
imported; the Daemon denies access and offline settings refuse to proceed.

## Limits of the policy

The policy protects the Daemon interface. It does not stop a process running as
the same operating-system user from opening the SQLite database directly.
