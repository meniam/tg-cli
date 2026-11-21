---
date: 2026-11-21T21:44:59+03:00
type: Исправлено
---
tg autostart on no longer loads the job while a daemon already runs, so launchd does not retry a failing start every 30 seconds.
