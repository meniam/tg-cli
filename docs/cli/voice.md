# Voice transcription

The Daemon turns voice messages, video notes and (on request) videos into text with a local speech engine, GigaAM v3 through sherpa-onnx, and `ffmpeg` for the conversion. The engine recognises Russian only: other speech comes out as noise. Every job goes through one persistent queue (the table `voice_queue` of the profile database) that a single worker serves, so jobs survive a restart of the Daemon. Telegram is asked only for the file: no command on this page asks Telegram to transcribe. The single path that does is [`tg message transcribe --telegram`](messages-read.md#tg-message-transcribe).

Two things are set once: the engine (`tg voice install`, about 190 MB under `<home>/models/gigaam`, one set for all profiles) and `ffmpeg` in `PATH`. The sources that queue jobs by themselves need `[voice_transcription] enabled = true` (the keys, the limits and the `TG_CLI_VOICE_TRANSCRIPTION_*` variables are in [overview.md](overview.md#request-throttle-and-flood_wait)); while it is off the Daemon queues nothing by itself and makes no new Telegram request for transcription. A manual command, `tg message transcribe` or `tg voice retry`, works in either case.

What a job does, in order: it checks that `access.json` does not close the chat and that the message is saved with a matching media type; it skips a file above the limit of its kind (30 MB for a voice message, 50 MB for a video note, 500 MB and 120 minutes for a video; `skipped` with the reason `too_large` or `too_long`, nothing downloaded); it checks free disk space and pauses between downloads (`pause`); it downloads the file into a private temporary directory of the Daemon (`tmp-voice` next to the database, mode 0700) and deletes it whatever happens; it converts the file to WAV and recognises it; it stores the text with `source: "local"` and `engine: "gigaam-v3"`. The text is untrusted Telegram data like any message text. A speechless recording is stored as an empty text and the job ends `done` with the reason `no_speech`. A network failure of the download is retried three times after 10, 60 and 300 seconds; every other refusal ends the job `failed` with a short reason (`download`, `convert`, `engine`, `disk`, `no_media`, `no_message`, `store`, `internal`).

The daily budget (`daily_download_mb`, `daily_files`, counted by the local date) pauses the queue until the next day when it is spent (`paused: budget`); a manual job ignores the budget but counts in it. A missing engine or `ffmpeg` pauses the queue (`paused: engine_missing`) without spending attempts, and an absent Telegram connection pauses it (`paused: offline`).

## Jobs, sources and reasons

A job has one of five states: `queued`, `running`, `done`, `failed`, `skipped`. Its source sets the priority, and a lower number goes first: `manual` 0 (`tg message transcribe`, `tg voice retry`), `incoming` 10 (a new message of the event stream), `sync` 20 (messages saved by `tg chat sync` and the background sync, only those younger than 90 days), `backfill` 30 (`tg voice backfill` and `tg chat voice on --backfill`). A message has one job: queueing it again adds nothing and only raises the priority. A manual job goes next, but never interrupts the job in progress. A job that is `running` when the Daemon stops goes back to `queued`.

The reason is a machine word (no text, no path):

| Reason | State | Meaning |
| --- | --- | --- |
| `no_speech` | `done` | the recording has no speech; an empty text is stored |
| `exists` | `done` | a finished text was there already (not for a manual job, which writes the text again) |
| `too_large` | `skipped` | the file is above the limit of its kind; nothing was downloaded |
| `too_long` | `skipped` | a video is longer than `max_video_minutes`; nothing was downloaded |
| `closed` | `skipped` | `access.json` closes the chat |
| `no_media` | `skipped` or `failed` | `skipped`: the message is deleted or its media type is not the kind of the job; `failed`: Telegram has no such message or file any more |
| `cancelled` | `skipped` | `tg voice cancel` stopped or skipped the job |
| `download` | `failed` | the download failed (after three retries for a network failure) or Telegram refused the file |
| `convert` | `failed` | `ffmpeg` could not convert the file |
| `engine` | `failed` | the speech engine failed or timed out |
| `disk` | `failed` | the free disk space is below twice the file size |
| `no_message`, `store`, `internal` | `failed` | the message is gone from the local database, a database error, an unexpected error |
| `interrupted` | `failed` | the job was stopped for a reason other than `tg voice cancel` or the stop of the Daemon |

A stop of the Daemon gives the job back to `queued` without a failure, and the next start marks the job `recovered` (the reason of a `queued` job). The text of a job that `tg message transcribe` waits for comes back in the answer; a failure comes back as `error.reason` (see [`tg message transcribe`](messages-read.md#tg-message-transcribe)).

What is not covered: the engine recognises Russian only and does not check the language, so another language gives noise that goes into the search as it is. The recognition of a `video` is stored and returned by `tg message transcribe`, but the search and the semantic index take only voice messages and video notes. With `--idle-timeout` the Daemon waits only for the job in progress: without a client it stops and the queue waits for the next start.

## Commands

### tg voice install

Downloads the engine into `<home>/models/gigaam`: the sherpa-onnx runtime, the GigaAM v3 model and the Silero VAD model, only from the links and with the SHA-256 sums fixed in the program. Each archive is checked against its size and sum before it is unpacked, and unpacked without leaving its directory (no `..`, absolute names or links). The directories get mode 0700 and the files 0600. The command runs in this process and needs neither the Daemon nor Telegram. A repeat run fetches only what is missing. Progress goes to stderr.

```text
tg voice install [--check]
```

<!-- schema:flags tg voice install -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--check` | bool | `false` | only report what is missing, download nothing |
<!-- /schema:flags -->

**Safety class:** `local_write` (`--check` only reads). Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** Network access to `github.com` for the download, about 190 MB of free disk space. `ffmpeg` is not downloaded: `--check` and a finished install report it as missing when `PATH` (or `[voice_transcription] ffmpeg`) does not have it. A failed download changes nothing that was installed.

**Example.**

```sh
tg voice install --check
```

**Answer.**

```json
{"ok":true,"data":{"installed":false,"checked":true,"missing":["runtime","model","vad"]}}
```

**Answer fields** (`data`): `installed` boolean (nothing is missing); `checked` boolean (true for `--check`, which downloads nothing); `missing` array of `ffmpeg`, `runtime`, `model`, `vad` (empty when `installed`). No path and no file name is reported.

**Exit codes.** 0 (also when `--check` finds parts missing: read `installed`); 1 the download failed, the sum did not match, the archive was refused or the command was interrupted (the message names the class, not a path); 2 an invalid `[voice_transcription]` setting.

### tg voice status

Reports the engine and the queue. It reads state in memory and the database; it never calls Telegram.

```text
tg voice status [flags]
```

<!-- schema:flags tg voice status -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. It needs no Telegram connection.

**Example.**

```sh
tg voice status
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"installed":true,"state":"idle","queue":{"queued":0,"running":0,"done":42,"failed":1,"skipped":3},"done_today":7,"files_today":7,"bytes_today":3145728,"daily_files":200,"daily_bytes":2097152000,"last_failure":"download"}}
```

**Answer fields** (`data`): `enabled` boolean (the setting); `installed` boolean; `missing` array of `ffmpeg`, `runtime`, `model`, `vad` (optional); `state` string, `disabled` (the worker does not run: the setting is off and no manual job has started it), `idle`, `working` or `paused`; `paused` string (optional), `engine_missing`, `budget` or `offline`; `paused_until` integer (optional, Unix seconds, for `budget`); `queue` object with `queued`, `running`, `done`, `failed`, `skipped` integers; `current` object (optional) with `chat_id`, `message_id`, `kind`, `source`, `started_at` and `stage` (checking, waiting to download, downloading, converting, transcribing or saving); `done_today` integer (jobs done since the Daemon started, on the local day); `files_today` and `bytes_today` (downloads of the local day); `daily_files` and `daily_bytes` (the budget); `last_failure` string (optional, the reason of the latest failed or skipped job since the Daemon started). Every string is a machine word: no message text and no path.

**Exit codes.** 0; 3 no Daemon.

### tg voice cancel

Without arguments it stops the running job and skips every queued one. With a chat and a message id, or a message link, it cancels only that message's job. A cancelled job ends `skipped` with the reason `cancelled`; `tg voice retry` queues it again.

```text
tg voice cancel
tg voice cancel <chat> <id>
tg voice cancel <message-link>
```

<!-- schema:flags tg voice cancel -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. It resolves the chat locally: a numeric id, or a `@username` the database knows. It needs no Telegram connection.

**Example.**

```sh
tg voice cancel
```

**Answer.**

```json
{"ok":true,"data":{"skipped":12,"stopped":true}}
```

**Answer fields** (`data`): `skipped` integer (queued jobs skipped); `stopped` boolean (the running job was stopped; the job ends a moment later, check `tg voice status`).

**Exit codes.** 0; 2 a chat without an id, a non-positive id or a chat reference that cannot be resolved locally; 3 no Daemon; 7 a closed or unknown chat, or no job for the message.

### tg voice retry

Queues the job of one saved message again at the front of the queue, whatever its earlier outcome. It does not wait for the text: read it with [`tg message transcribe`](messages-read.md#tg-message-transcribe). It works with `enabled = false` too.

```text
tg voice retry <chat> <id>
tg voice retry <message-link>
```

<!-- schema:flags tg voice retry -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon, the engine and `ffmpeg` installed, the message saved locally with the media type `voice`, `video_note` or `video`. A job that is queued or running already is left as it is (`queued: false`).

**Example.**

```sh
tg voice retry @alice 4711
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":123456789,"id":4711,"kind":"voice","queued":true,"state":"queued"}}
```

**Answer fields** (`data`): `chat_id` integer; `id` integer; `kind` string (`voice`, `video_note` or `video`); `queued` boolean (false when the job was queued or running already); `state` string (the state of the job after the call).

**Exit codes.** 0; 2 the engine is not installed (`error.reason` is `engine_missing`; run `tg voice install`), the message has no voice message, video note or video, or the chat reference cannot be resolved locally; 3 no Daemon; 7 a closed or unknown chat, or a message that is not in the local database.

### tg voice backfill

Queues the saved voice messages and video notes of the personal chats that have no finished text and no job yet, at the lowest priority, so that manual and incoming jobs go first. Chats that `access.json` closes, your own messages and deleted messages are left out. Videos are never queued here. One database query selects the candidates page by page, so every personal chat counts: there is no ceiling on the number of chats.

```text
tg voice backfill [--dry-run]
```

<!-- schema:flags tg voice backfill -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--dry-run` | bool | `false` | count the messages and queue nothing |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. Without `[voice_transcription] enabled = true` the command fails with exit code 2 and the message `voice transcription is off: set [voice_transcription] enabled = true`; `--dry-run` still counts and says so on stderr. A real run needs the engine installed.

**Example.**

```sh
tg voice backfill --dry-run
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"dry_run":true,"chats":14,"candidates":238,"queued":0}}
```

**Answer fields** (`data`): `enabled` boolean (the setting); `dry_run` boolean; `chats` integer (personal chats with saved messages that `access.json` leaves open); `candidates` integer (messages that qualify); `queued` integer (jobs this call queued, always 0 for a dry run).

**Exit codes.** 0; 2 voice transcription is off (not for `--dry-run`) or the engine is not installed; 3 no Daemon.

## Chat marks

Personal chats (chats of type `user`; bots, groups and channels are not personal) need no mark. For any other chat a mark in the local database says "transcribe the voice messages and video notes here", and with the video flag the videos too. The marks work through three commands. They read and write only the local database: no Telegram call, and the chat is found there (a numeric id or a `@username` the database knows; put `--` before a negative id).

Who gets queued, and when, is one rule. A message is queued only if `access.json` leaves its chat open, it is not your own, and it is not deleted. A voice message or video note needs a personal chat or a mark; a video needs a mark with `--video`. The sources are:

- a new message of the event stream, after it is saved: priority 10 (`incoming`);
- the messages a `tg chat sync` or the background sync has just saved, only those whose date is less than 90 days old: priority 20 (`sync`); older ones wait for a backfill;
- `--backfill` of a mark and `tg voice backfill`: priority 30 (`backfill`).

A source never queues a message twice (the queue keeps one row per message) and makes no Telegram request. Without `[voice_transcription] enabled = true` no source queues anything.

### tg chat voice on

Marks a chat. Marking a marked chat again changes only its video flag and keeps the time of the first mark. With `--backfill` it also queues the voice messages and video notes already saved for the chat that have no finished text and no job yet (and the videos, with `--video`); the answer counts them. With the setting off the mark is stored but `--backfill` fails with exit code 2 (`error.reason` is `disabled`); a missing engine fails it the same way (`engine_missing`).

```text
tg chat voice on <chat> [--video] [--backfill]
```

<!-- schema:flags tg chat voice on -->
| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `--backfill` | bool | `false` | queue the messages already saved for the chat |
| `--video` | bool | `false` | transcribe the videos of the chat too |
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon. The chat is known to the local database. The mark takes effect only with `[voice_transcription] enabled = true`; while it is off the command warns on stderr.

**Example.**

```sh
tg chat voice on @team --backfill
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"video":false,"enabled_at":1795000000,"enabled":true,"candidates":31,"queued":31}}
```

**Answer fields** (`data`): `chat_id` integer; `video` boolean (the flag of the mark after the call); `enabled_at` integer (Unix seconds of the first mark); `enabled` boolean (the setting; the mark does nothing while it is false); `candidates` and `queued` integers (the backfill; both 0 without it).

**Exit codes.** 0; 2 no chat, a chat reference that cannot be resolved locally, `--backfill` while the setting is off or the engine is not installed (the mark is stored anyway); 3 no Daemon; 7 a closed or unknown chat.

### tg chat voice off

Removes the mark. Jobs already in the queue stay; `tg voice cancel` skips them. Personal chats are transcribed with or without a mark.

```text
tg chat voice off <chat>
```

<!-- schema:flags tg chat voice off -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `local_write`. Not dangerous. **Output formats:** `json`, `toon`, `human`.

**Preconditions.** A running Daemon.

**Example.**

```sh
tg chat voice off @team
```

**Answer.**

```json
{"ok":true,"data":{"chat_id":-1001234567890,"removed":true}}
```

**Answer fields** (`data`): `chat_id` integer; `removed` boolean (false when the chat had no mark).

**Exit codes.** 0; 2 no chat or a chat reference that cannot be resolved locally; 3 no Daemon; 7 a closed chat.

### tg chat voice list

Lists the marked chats that `access.json` leaves open, ordered by chat id.

```text
tg chat voice list
```

<!-- schema:flags tg chat voice list -->
No command flags.
<!-- /schema:flags -->

**Safety class:** `read`. Not dangerous. **Output formats:** `json`, `jsonl`, `toon`, `human`.

**Preconditions.** A running Daemon.

**Example.**

```sh
tg chat voice list
```

**Answer.**

```json
{"ok":true,"data":{"enabled":true,"chats":[{"chat_id":-1001234567890,"title":"Team","username":"team","video":false,"enabled_at":1795000000}]}}
```

**Answer fields** (`data`): `enabled` boolean (the setting); `chats` array of `chat_id` integer, `title` string and `username` string (both untrusted Telegram data, omitted when the database does not know the chat), `video` boolean, `enabled_at` integer (Unix seconds). With `--format jsonl` each line is one chat.

**Exit codes.** 0; 3 no Daemon.
