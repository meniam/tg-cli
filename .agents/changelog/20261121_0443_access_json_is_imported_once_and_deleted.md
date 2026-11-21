---
date: 2026-11-21T04:43:26+03:00
type: Удалено
breaking: true
decision: 20261120_2242_settings_move_from_config_toml_and_access_json_once_in_one_transaction
---
access.json is imported once and deleted; change the access lists with `tg settings` and the owner passphrase, and set the first passphrase with `tg settings token init` after the update.
