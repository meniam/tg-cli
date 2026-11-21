# Media: sending and downloading

## Sending media

`tg message send-media` counts as writing: class `account_write`, not `dangerous`, `--yes` is not needed. Run it only on an explicit user request. Two settings are required, and only the owner changes them: the chat is in `access.write` and the file's directory is in `access.files` (an array of absolute directories). Without either permission, sending files is off, code 9.

Every example below is a preview (`--dry-run`); to send for real, get the user's confirmation of the chat, the file and the caption, remove `--dry-run` and keep the `--idempotency-key` from the preview (add a new one if the preview had none).

```bash
tg message send-media <chat> --as photo --file /abs/dir/chart.png [--caption "Caption"] --dry-run
tg message send-media <chat> --as photo --file /abs/a.png --file /abs/b.png --caption "Album" --dry-run   # 2 to 10 files
tg message send-media <chat> --as document --file /abs/report.pdf [--reply-to ID] [--topic ID] [--silent] --dry-run
tg message send-media <chat> --as video --file /abs/clip.mp4 [--duration N --width N --height N] --dry-run
tg message send-media <chat> --as voice --file /abs/note.ogg [--duration N] --dry-run
tg message send-media --dry-run --as photo --file /abs/chart.png <chat>   # check without uploading
```

- **Roots.** A file is read only from the `access.files` directories. You cannot extend them: ask the owner to change `access.files`. A file outside the roots cannot be sent: tell the user which directory the owner must add and wait. The CLI turns a relative path into an absolute one using its working directory.
- **The type is always explicit.** `--as photo|video|audio|voice|document` is required, without it code 2; the type is not guessed from the extension. The content is checked by its first bytes: `photo` only JPEG and PNG, `video` only MP4, `voice` only Ogg with Opus (exactly one file), `audio` MP3, AAC (ADTS) and MP4 with the brand M4A, M4B or M4P, `document` any regular file. Send everything else (WebP, GIF, MOV, WAV, FLAC, Ogg Vorbis) as `--as document`. A mismatch with the type gives code 2 with the hint `--as document`: repeat with it, do not change the file.
- **Limits.** 100 MiB per file and for all files of the call together, at most 10 files, a caption up to 1024 Unicode code points. Several files (2 to 10, one type) go as one album, and the caption goes with the first file. Any excess gives code 2 before upload; a caption that is too long has `reason: premium_required`.
- **Transfer.** In `message send-media` and `message edit-media` a large file is uploaded in parts (the part size and the window are in `docs/cli/`). Across the whole Daemon at most two files are uploaded or downloaded at the same time; a third call waits and is cancelled together with the command context. A short `FLOOD_WAIT` on a part is waited out and only that part is retried; a long one gives code 6. Without `--idempotency-key` the tool does not retry sending the message after the upload; with a key, one transient network failure may be retried once with the same Telegram ids.
- **Metadata.** For video the Daemon reads the duration and dimensions from the file, for voice the duration. If it cannot, the command gives code 2 and asks for `--duration --width --height` (video) or `--duration` (voice). The flags override what was read and apply to all files of the album. For `audio` the duration is not detected on its own: set `--duration` if you need it. For `photo` and `document` the flags are ignored.
- **`--dry-run` first.** It checks the chat, `write`, the roots, the type, the size and the caption, reads the file to check its content and uploads nothing. The response: `dry_run: true`, `action: "send_media"`, `count`, `types`, `sizes`, `caption_length`, an empty `messages` list. Read it in `--format json`, `toon` or `human`: `jsonl` prints one line per sent message.
- **Response.** Always `{chat_id, messages: [{id, date}]}`, even for a single file. The Daemon saves what was sent to the local database (`out: true`, `media_type` as in `--as`, the caption on the first message); the file itself is not in the database.
- **Time.** The call waits for the upload and the send, but no longer than 600 seconds (the same CLI limit applies to `message edit-media`; the usual CLI limit is 120 seconds). On expiry it is code 1 and the outcome is unknown: check `message history` first, then decide whether to send again. Measured on a live account: 30 MiB went in 42 seconds (about 0.7 MiB/s), so a 100 MiB file, estimated from this measurement, takes about 140 seconds.

Exit codes of this command:

| Code | When | What to do |
|---|---|---|
| 2 | no `--as`, wrong arguments, a path outside the roots (`file is outside the allowed roots`: this is the answer for `..`, for a symlink pointing outside and for a nonexistent root), the file did not fit the type, an empty file, more than 100 MiB, a caption longer than 1024 characters | fix the call; for a path outside the roots ask the human to add the directory to `files` |
| 7 | the chat is closed or unknown, or the file is not inside the root (`file not found`) | check the chat and the path |
| 9 | the chat is not in `write` or there are no `files` roots | tell the user: only they add the chat to `write` and the directory to `files` |
| 6 | `FLOOD_WAIT`: seconds in `error.message` | do not retry at once and do not loop retries |
| 5 | Telegram rejected the upload or the send | show the reason from `error.message` |

- **Do not repeat an album after an unclear failure.** Without `--idempotency-key` the tool does not retry sending; with a key, one transient network failure may be retried automatically with the same Telegram ids. If the response to the send is lost (code 1 after the deadline, a dropped connection), part of the album or all of it may have gone out. Before sending again, compare `message history` by caption, type and time. `FLOOD_WAIT` during upload means nothing has been sent yet.
- **Verified** by automated tests on a Telegram stub and in temporary directories: the request parameters of each type, an album of three with the caption on the first, an upload interrupted without sending, symlinks and `..`, refusals before any call to Telegram, a journal without paths and captions. **Verified on a live account** (the test chat, 2026-11-14): the server accepted all five types and two albums (in an album of two photos the caption is only on the first attachment), `--dry-run`, the main refusals before Telegram (no `--as`, a chat outside `write`, `write` without `files`, a file over 100 MiB, a wrong type, a voice message of two files), one response for a path outside the roots, a nonexistent root and a symlink pointing outside, what was sent was saved with the correct `media_type`.
- **Not verified on a live account:** see `FEATURES.md`, items 6.1, 6.2. Do not use file sending for experiments, only on an explicit user request in a chat from `write`.

## Downloading

`tg message download` counts as `local_write`: the file is written to a directory that the owner put in advance into `access.downloads`. Only the owner changes this setting.

```bash
tg message download <chat> <id>... --output /path/to/downloads --dry-run   # check the metadata first
tg message download https://t.me/name/123 --output /path/to/downloads
```

- **Limits.** `--output` is an existing absolute directory inside `downloads`. Up to 20 messages per call, 100 MiB per file and 500 MiB per call. Existing files are not overwritten. Protected media is not downloaded.
- **Integrity.** A file appears under its final name only after a size check, and for a document also a check of the Telegram hashes. In a finished download `files[].sha256` holds the SHA-256 of the file. A broken or cancelled download leaves neither a temporary nor a partial file.
- **Concurrency.** The Daemon transfers at most two files at the same time (uploads and downloads together), a third call waits.
- **Partial success.** Code 1 and `errors` with the failed items. Code 9: no root in `downloads`. A `FLOOD_WAIT` shorter than `behavior.flood_autowait` is waited out and retried automatically, a long one gives code 6 (code 1 on partial success).
- **Verified:** the code and the tests on a Telegram stub cover parallel parts, cancellation, throttling, and the size and document hash checks. **Not verified on a live account:** speed, `FLOOD_WAIT` on parts, FileDC pools through a proxy and WSS, photo hashes (`FEATURES.md`, items 6.3, 6.4). Command details are in `docs/cli/messages-read.md`, section `tg message download`.
