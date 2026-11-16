---
date: 2026-11-16T03:27:58+03:00
type: Изменено
decision: 20261116_0136_databases_live_in_db_next_to_the_config
---
The profile database now lives in ~/.config/tg-cli/db/<profile>/ and is moved there on daemon start. Exclude that directory from dotfiles and cloud sync.
