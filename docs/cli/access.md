# Access lists: `access.json`

`access.json` is the file where the owner decides which chats the agent can see, in which chats it may write, and from which directories it may send files. It is the only control of that kind: no command writes it, and the agent must not edit it. This page describes the file completely. The commands that obey it are in [chats.md](chats.md), [messages-read.md](messages-read.md), [messages-write.md](messages-write.md) and [notes-and-search.md](notes-and-search.md).

## Who edits the file

Only a human edits `<profile>/access.json`, with an editor. There is no command that writes it, and the agent never changes any field. The filter protects against accidental reading, against reading induced through a chat, and against accidental writes. It does not protect against an agent that deliberately bypasses it: an agent running as the same user can edit the file or open the SQLite database directly.

The file lives in the profile directory, next to the session and the database (see [Profiles and the data directory](overview.md#profiles-and-the-data-directory)): `<TG_CLI_HOME>/<profile>/access.json`, or `tg-cli/<profile>/access.json` inside the user config directory.

## The file

A JSON object with optional fields. Chat lists and file roots are arrays; `create` and `profile` are booleans.

| Field | Content | Effect |
| --- | --- | --- |
| `allow` | nonzero integer chat ids, no duplicates | when not empty, only these chats are visible to the agent |
| `block` | nonzero integer chat ids, no duplicates | these chats are never visible; `block` wins over `allow` |
| `write` | nonzero integer chat ids, no duplicates | the chats where the agent may write (send, edit, delete, forward, react, pin, poll, mark read, send files) |
| `files` | absolute directory paths, no duplicates | the directories from which `message send-media` may read files |
| `admin` | nonzero integer chat ids, no duplicates | the visible chats where administration is allowed |
| `create` | boolean | allows creating chats when true |
| `profile` | boolean | allows own-profile and privacy changes when true; off by default |

Chat ids are in the Bot API form that `tg chat list` prints: users positive, groups negative, channels from `-1000000000000`. An example file:

```json
{
  "allow": [-1001234567890, 987654321],
  "block": [987654321],
  "write": [-1001234567890],
  "files": ["/Users/me/tg-outbox"]
}
```

Here the agent sees one chat (`block` removes the second one from `allow`), may write into it, and may send files from `/Users/me/tg-outbox`. A smaller file is just as valid: `{"write": [-1001234567890]}` turns writing on for one chat and leaves reading unrestricted.

Create the file with a mode that is not writable by group or others, for example `chmod 600 access.json`.

## How the lists combine

- **No file, an empty file, or all lists empty:** the read filter is off and every chat is visible. Writing is off everywhere, and so is sending files.
- **A nonempty `allow`:** only its chats are visible. Invitations (`chat invite-info`) are closed while `allow` is nonempty, because an invite has no known chat id before it is resolved.
- **`block`:** blocked chats are closed, even when they are in `allow`. A `block` alone turns the read filter on for those chats.
- **`write`:** a chat accepts writes only when it is in `write`, is visible by the read rules and is not in `block`. `write` alone does not turn the read filter on. A chat outside `write` gives exit code 9 `write_disabled` with a hint to add it; a chat closed for reading gives exit code 7 like an unknown chat.
- **`files`:** sending files needs the chat in `write` and every file under a directory of `files`. No `files` key or an empty list means sending files is off: exit code 9 `write_disabled` with a hint to ask the owner to add the directory. Paths are checked as described in [message send-media](messages-write.md#tg-message-send-media): a path outside every root, a `..` that leaves a root, a symlink that leads out and a root that does not exist all give the same exit code 2, `file is outside the allowed roots`.

## Errors in the file: everything closes

The file is checked when it is read, and it fails closed:

- An unknown field, a non-numeric or zero id, a duplicate id, a list field that is not an array, a non-boolean `create` or `profile`, a relative or empty `files` path, a duplicate path, or content that is not a JSON object makes the whole file invalid.
- A file that is writable by group or others (any of the mode bits `0o022`; `0664` and `0666` count, `0644` and `0600` do not) is invalid.
- A file that cannot be read is invalid, and so is a failed `stat` of it.
- An invalid file closes **everything**: all chats are hidden from the agent, writing and sending files are off. The Daemon keeps running, and each request that checks access writes at least one Error record `access policy unavailable` to the journal, without the file content (see [daemon logs](daemon-and-auth.md#tg-daemon-logs)).

Fix the file and the Daemon picks the new content up without a restart.

## Reload

The Daemon rereads the file when its modification time, size or mode changes. No restart is needed, and a change applies to the next request. A file that does not exist counts as no lists at all.

## What the Daemon shows: `daemon status`

`tg daemon status` shows the field `access` when the read filter is on, a write or admin list is nonempty, or `create` or `profile` is enabled:

```json
{"ok":true,"data":{"profile":"default","pid":48213,"connected":true,"authorized":true,"active_requests":0,"access":{"enabled":true,"allow":1,"block":0,"write":1}}}
```

| Field | Meaning |
| --- | --- |
| `enabled` | the read filter is on (a nonempty `allow` or `block`, or a closed file) |
| `allow`, `block` | the counts of ids |
| `write` | the count of ids, shown when it is not empty |
| `profile` | `true` when the owner has enabled own-profile changes |
| `closed` | `true` when the file does not parse, cannot be read or has a mode that is too wide |

It never shows chat ids. With `{"write": [-1001234567890]}` only, `enabled` is `false` and `write` is `1`.

## What a closed chat looks like to the agent

A closed chat does not exist for the agent:

- It is absent from `chat list` (the list is topped up from Telegram until `--limit`, until the dialogs run out or until 500 dialogs have been requested, so with many closed chats it can be shorter than `--limit`), from `tg search` and from `note list`. `chat common` drops it from its answer.
- `chat resolve`, `chat info`, `chat members`, `chat topics`, `chat sync start|status|cancel`, `chat mark-read`, `message history|get|pinned|search|context|comments|link|export`, `message watch --chat` and every write command give exit code 7 `not_found` with the same code and the same message as a chat that does not exist: `not found: this session does not know that id; use @username, or read the chat list first` for a numeric id the session does not know, and `not found: the chat or message does not exist or is not visible to this account` for every other reference.
- `note add` for it gives `not_found` with the text of the second message, and `note delete` of a note of such a chat gives the plain `not found` that a missing note gives.
- `message search --public` drops the hits of closed chats before it answers. Live events of closed chats are dropped before they are stored and before delivery, including `typing`, `read` and `status`.
- Closed chats are not saved to the local database. What was saved earlier is not deleted but hidden by the queries (an SQL condition in search and notes).

To learn the ids of closed chats, a human clears the file for a moment, runs `tg chat list` and restores the file. There is no command outside the filter, because the agent could call it.

## What the filter does not cover

- For an `@username`, a phone number or a `t.me` link the Daemon resolves the reference in Telegram first and checks the id afterwards. A request for a closed chat therefore still reaches Telegram, and its response time and the throttle counter may differ from an unknown chat.
- `note delete --author`, which also deletes the notes of closed chats.
- People (`people`) and notes about people, which are not filtered.
- Direct access to the SQLite file.
- An agent running as the same user that edits `access.json` itself.

The mark `untrusted` in `tg schema` and the `| ` frame of `--human` are hints for the agent, not a barrier: the barrier is this filter.

## Checks

Verification of the access filter is tracked in [FEATURES.md](../../FEATURES.md), item 9.2.

Older versions reject the `profile` field as unknown and close access until the owner removes it.
