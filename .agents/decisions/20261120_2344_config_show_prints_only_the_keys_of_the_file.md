---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/cli/config_cmd.go, docs/cli/config.md, docs/skill/tg]
---
# tg config show печатает только ключи файла config.toml

## Context

`tg config show` печатает все секции `config.toml`. После переноса
[настройки лежат в базе профиля](20261120_2242_settings_live_in_the_profile_database_and_config_toml_keeps_only_what_finds_it.md),
а в файле остаются `home`, `default_profile` и `[files]`. Агенты могут
разбирать старый ответ. Владелец 2026-11-20 выбрал вариант A вопроса Q11 плана
`20261120_2242_settings_in_database`.

## Decision

- `tg config show` возвращает только `home`, `db_dir` (вычисленный корень баз),
  `default_profile` и `files`.
- Остальные настройки показывает `tg settings list`.
- Изменение ответа ломает формат: запись changelog `Изменено` с `--breaking`,
  навык `docs/skill/tg` и `docs/cli/config.md` правятся тем же планом.

## Why

- Отвергнуто: прежняя форма ответа со значениями из базы (вариант B). Две
  команды показывали бы одно и то же, а `config show` пришлось бы ходить в
  Daemon или открывать базу.

## Cost

- Сценарии, которые читали `behavior`, `background_sync`, `semantic_search` или
  `profiles` из ответа `config show`, ломаются и переходят на `tg settings`.
