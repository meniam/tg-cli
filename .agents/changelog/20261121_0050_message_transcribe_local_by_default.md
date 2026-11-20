---
date: 2026-11-21T00:50:02+03:00
type: Изменено
decision: 20261120_2238_voice_is_transcribed_locally_by_gigaam_through_a_persistent_queue
---
`message transcribe` now uses the local engine by default (run `tg voice install`), no Premium needed; `--telegram` is the old path. `--wait` up to 600 s; answers add `source`, `engine`, `queued`.
