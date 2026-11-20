---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/cli/schema_cmd.go, docs/cli]
---
# Ключи настроек имеют вид секция.ключ, списки меняются подкомандами add и remove

## Context

[Команда tg settings](20261120_2242_tg_settings_reads_and_writes_settings_with_flag_env_database_default_priority.md)
оставила форму ключей и набор подкоманд плану `20261120_2242_settings_in_database`.
Владелец 2026-11-20 принял вариант A вопроса Q2.

## Decision

- Ключ имеет вид `секция.ключ`: `behavior.throttle_percent`,
  `background_sync.window_start`, `semantic_search.min_similarity`,
  `profile.proxy`, `access.write`. Имени профиля в ключе нет, профиль выбирает
  `--profile`.
- Подкоманды: `list [--prefix P]`, `get <key>`, `set <key> <value>`,
  `unset <key>` (возврат к умолчанию), `add <key> <value>...` и
  `remove <key> <value>...` (только ключи-списки), `token init|change|reset`.
- Ответ записи несёт `key`, `value`, `previous`, `applied` (`live`,
  `restart_required`, `next_start`) и `overridden_by` (`env` или `flag`).

## Why

- Отвергнуто: только `list`, `get`, `set`, `unset` со списком целиком
  (вариант B). Правка одного id требовала бы переписать список, и опечатка
  выбросила бы соседние id.
- Отвергнуто: путь как в TOML, `profiles.default.proxy` (вариант C). Профиль уже
  выбирает `--profile`, а `config.toml` больше не источник настроек.

## Cost

- Девять подкоманд в `tg schema`, в `docs/cli/` и в навыке `docs/skill/tg`.
- Переименование ключа ломает сценарии агентов: имена ключей становятся
  контрактом.
