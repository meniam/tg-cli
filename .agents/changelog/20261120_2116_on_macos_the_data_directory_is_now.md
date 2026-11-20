---
date: 2026-11-20T21:16:50+03:00
type: Изменено
breaking: true
decision: 20261120_2116_default_home_is_dot_config_and_the_old_macos_home_moves_automatically
---
On macOS the data directory is now ~/.config/tg-cli. The first command moves the old one and keeps secret copies in .home-move-backup-*; stop the old daemon first (tg daemon stop).
