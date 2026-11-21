---
title: Start the Daemon at login on macOS
task: «запускай tg при старте компьютера», «автозапуск демона», «чтобы демон сам стартовал после перезагрузки», «отключи автозапуск»
writes: true
access: [none]
commands: [autostart status, autostart on, autostart off, daemon status]
live_checked: no
tags: [daemon, macos, launchd, autostart]
---

# Start the Daemon at login on macOS

## When to use

The user wants the Daemon to come up by itself after a restart of the Mac or after a login, or to turn that off. For starting it once, `tg daemon start` is enough.

## Prepare

macOS and the installed `tg` (for example `~/.local/bin/tg`); say so to the user if `tg` runs from a build folder. The change touches the login items of the machine, so do it only on request. The profile needs its login and saved settings first; own credentials in `TG_API_ID` and `TG_API_HASH` do not work with autostart (unset them and use the official client preset).

## Commands

```bash
tg autostart status            # is the job installed and loaded, and which binary does it run
tg autostart on                # install and load it for the profile
tg daemon status               # the Daemon answers
tg autostart off               # unload and delete the job when the user asks to stop autostarting
```

Another profile has its own job: `tg autostart on --profile work`.

## Notes

- The job starts at login, not at power-on before login, and restarts the Daemon only after a crash. `tg daemon stop` leaves it stopped until the next login.
- A Daemon you started by hand is not under launchd: `on` then writes the job file but does not load it (`loaded: false`, the reason in `note`), and launchd loads it at the next login. To put the daemon under launchd now, run `tg daemon stop` and then `tg autostart on` again.
- `executable` in `status` is the binary the job runs. After `tg` moved or was rebuilt elsewhere, run `tg autostart on` again.
- The job file lives in `~/Library/LaunchAgents/` and holds no secret; its stderr goes to `launchd.err` in the profile directory.

## Pitfalls

- Exit code 2: not macOS, own `TG_API_ID`/`TG_API_HASH` are set, or `tg` is a temporary build. Exit code 1 carries the `launchctl` message; the job file stays for the owner to look at.
- After a reboot the history sync queue is empty; run the batch again ([sync-active-chats](sync-active-chats.md)).
- Do not enable it without being asked, and do not run `off` to "fix" a crashing Daemon: read `tg daemon logs --tail 50` first.

## Verify

`tg autostart status` shows `enabled: true` and `loaded: true` with the expected `executable`. After the next login `tg daemon status` answers without a manual start.
