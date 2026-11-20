---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/access, internal/daemon/access.go]
---
# Фраза владельца защищает только ключи access.*, остальные настройки пишутся без неё

## Context

Агент может поднять `behavior.throttle_percent` до 100, включить фоновую
загрузку или сменить прокси. [Решение о фразе](20261120_2242_access_settings_are_written_only_with_the_owner_passphrase_typed_on_a_terminal.md)
защищает списки доступа. Владелец 2026-11-20 выбрал вариант A вопроса Q10 плана
`20261120_2242_settings_in_database`.

## Decision

- Фраза нужна только для записи `access.*` (и для чтения id `access.allow`,
  `access.block`).
- Остальные ключи, включая `profile.proxy`, `profile.transport`,
  `profile.client`, `profile.device` и `behavior.*`, пишутся без фразы: класс
  безопасности `local_write` в `tg schema`, запись в журнал Daemon с ключом и
  источником, без значения прокси.

## Why

- Отвергнуто: фраза ещё для `profile.*` и `behavior.throttle_percent` (вариант B).
  `daemon start --proxy`, `--client` и `TG_CLI_PROXY` пишут эти ключи без
  фразы, так что защита в `tg settings` ничего бы не закрыла, а владельцу
  пришлось бы набирать фразу на каждую правку дросселя.

## Cost

- Наведённый вызов может сменить прокси, клиент или дроссель. Чаты и файлы
  этим не открываются; след остаётся в журнале Daemon.
