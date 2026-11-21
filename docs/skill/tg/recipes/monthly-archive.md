---
title: Monthly conversation archive with SHA-256 verification
task: «сделай архив за месяц», «выгрузи переписку за октябрь», «ежемесячный архив чатов»
writes: true
access: [downloads]
commands: [chat sync status, message export-month, message export-verify]
live_checked: no
tags: [archive, export, verification]
---

# Monthly conversation archive

## When to use

You need a repeatable archive of a finished month (UTC) for one or several chats, with checksums. For a one-off export, [export-history](export-history.md) is enough.

## Prepare

- The owner added the archive directory to `access.downloads`.
- The history of each chat is loaded and shows `complete: true` ([load-full-history](load-full-history.md)).
- The month is over in UTC. The Daemon is running and knows the account.

## Commands

Check completeness first. Every requested chat must show `complete: true` (with `state: done`, or `idle` after a Daemon restart):

```bash
tg chat sync status --fields state,complete,newest_id -- <chat_id>   # numeric id of each requested chat (see `tg chat resolve`)
```

The `complete` check covers only the start of the chat; it cannot prove that the end of the month is covered (a load that finished before the month ended lacks its last days, a repeated sync does not fetch newer messages, and a running Daemon is no evidence either: its stream can emit `gap` after a storage failure). Confirm the end of the month with the user, or by comparing the saved messages of the last days with Telegram (`message history --since --until --fields messages.id,messages.date`). Then decide:

- Confirmed and every chat `complete: true`: publish.
- Unconfirmed: tell the user what you could not confirm and wait for their decision. Do not publish.
- Not covered, or a chat is not complete: stop and tell the user.

Always report what you could and could not confirm.

Publish the archive only after every requested chat is complete:

```bash
tg message export-month @release_team --month 2026-10 --output /path/to/tg-archives
```

Verify the directory printed in `data.dir` of that answer (replace `ARCHIVE_DIR` with it):

```bash
tg message export-verify "ARCHIVE_DIR" && (cd "ARCHIVE_DIR" && shasum -a 256 -c SHA256SUMS)
```

## Notes

- `export-month` accepts 1 to 50 distinct chats, only a finished month `YYYY-MM`, and an absolute existing `--output` inside `downloads`. Telegram is not called.
- The archive holds `<chat_id>.jsonl`, `SHA256SUMS` and, last, `manifest.json`. Message text and chat title do not go into the manifest.
- A finished directory is not overwritten: the response is `existing: true`. The directory is keyed by account and month, so an existing archive can hold a different set of chats than you asked for. Compare the manifest's `month` and `account_id`, `files[].chat_id` with your chats and the manifest's `include_deleted` with what you asked for (`--include-deleted` or not); if either differs, tell the user and do not rewrite or delete anything. For `existing: true` also check the manifest's `files[].sync`: an archive made before the sync finished has `complete: false` there, and it does not meet the completeness requirement of this recipe. A directory without `manifest.json` is incomplete, and only the owner can delete it.

## Pitfalls

- `sync.month_start_covered` in the manifest only says that the saved load reached the start of the month. It does not prove the completeness of the Telegram history.
- Treat the archive as intact only when both hold: `export-verify` exits with code 0 and `data.ok` is `true`. The outer `ok` of the envelope does not show an integrity failure, so read `data.ok`; if either one fails, the archive is not verified.
- No root in `downloads`: exit code 9. Tell the user what to add.
- The Daemon has no schedule: repeat it monthly with an external scheduler.
- Not verified on a live account (`FEATURES.md`, 13.4): only the code and tests are verified.

## Verify

`export-verify` exits with code 0 and `data.ok` is `true`, `files[].chat_id` matches the requested chats, and `shasum -c` finds no mismatches.
