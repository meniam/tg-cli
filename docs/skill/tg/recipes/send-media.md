---
title: Send a file, photo or album
task: «отправь файл», «отправь фото», «отправь документ», «отправь голосовое», «отправь альбом»
writes: true
access: [write, files]
commands: [message send-media]
live_checked: 2026-11-14
tags: [writing, media, files]
---

# Send a file, photo or album

## When to use

The user directly asks to send a file to a chat. For text without a file: [send-report](send-report.md).

## Prepare

- The owner added the chat to `access.write` and the file's directory to `access.files` (absolute directories).
- The file lies inside such a directory. If not, tell the user which directory the owner must add, and wait. Do not ask for the passphrase.
- The user confirmed the chat and the file.

## Commands

Preview first. This block uploads and sends nothing:

```bash
tg message send-media @release_team --as document --file /path/to/files/report.pdf --caption "Report" --dry-run --idempotency-key <fresh-key>
```

After the user confirms the chat, the file and the caption, send once, with a key:

```bash
tg message send-media @release_team --as document --file /path/to/files/report.pdf --caption "Report" --idempotency-key <fresh-key>
```

Use one fresh `--idempotency-key` per intended delivery (`<fresh-key>` is a placeholder: replace it with a new value) and reuse it only to retry that same delivery.

Other shapes are alternatives, not a sequence. Each block below is a preview and sends nothing. To send one, get the user's confirmation, remove `--dry-run` and keep the same `--idempotency-key` that the preview showed. Album of photos (2 to 10 files of one type, caption on the first):

```bash
tg message send-media @release_team --as photo --file /path/to/files/a.png --file /path/to/files/b.png --caption "Album" --dry-run --idempotency-key <fresh-key>
```

Voice message (one Ogg Opus file):

```bash
tg message send-media @release_team --as voice --file /path/to/files/note.ogg --dry-run --idempotency-key <fresh-key>
```

Video, with metadata when the Daemon cannot read it from the file:

```bash
tg message send-media @release_team --as video --file /path/to/files/clip.mp4 --duration 12 --width 1280 --height 720 --dry-run --idempotency-key <fresh-key>
```

## Notes

- `--as photo|video|audio|voice|document` is required. The content is checked by its first bytes: `photo` only JPEG and PNG, `video` only MP4, `voice` only Ogg with Opus (one file), `audio` MP3, AAC and M4A. Send everything else (WebP, GIF, MOV, WAV, FLAC) as `document`.
- Limits: 100 MiB per file and per call, up to 10 files, caption up to 1024 code points. An album is 2 to 10 files of one type, the caption goes on the first.
- The response is `{chat_id, messages: [{id, date}]}`.

## Pitfalls

- Always run the exact call with `--dry-run` first: it checks the chat, `write`, roots, type, size and caption, and uploads nothing.
- Exit code 2 with the hint `--as document`: repeat with it, do not change the file. A path outside the roots is also exit code 2.
- Timeout of 600 seconds: the outcome is unknown. Before retrying, compare `message history` by caption, type and time. Do not blindly retry an album after an unclear failure.
- `FLOOD_WAIT` during upload means nothing has been sent yet.

## Verify

`tg message get <chat> <id>` shows the message with the right `media_type` and caption.
