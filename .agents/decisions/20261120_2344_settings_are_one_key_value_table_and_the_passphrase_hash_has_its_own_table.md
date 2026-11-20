---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/store/migrations]
---
# Настройки лежат одной таблицей ключ-значение, хэш фразы владельца лежит отдельной таблицей

## Context

Настройки переезжают в базу профиля
([решение о хранении](20261120_2242_settings_live_in_the_profile_database_and_config_toml_keeps_only_what_finds_it.md)).
Нужна схема таблиц для миграции схемы `0012`. Владелец 2026-11-20 принял
вариант A вопроса Q1 плана `20261120_2242_settings_in_database`.

## Decision

- `settings(key TEXT PRIMARY KEY, value TEXT NOT NULL CHECK (json_valid(value)), updated_at INTEGER NOT NULL)`:
  строка на ключ, значение в JSON. Ключа нет в таблице: действует умолчание.
- `owner_secret(id INTEGER PRIMARY KEY CHECK (id = 1), hash TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`:
  одна строка с хэшем argon2id фразы владельца; чтение настроек её не возвращает.
- `settings_meta(key TEXT PRIMARY KEY, value TEXT)`: служебные отметки, например
  отметка переноса из `config.toml` и `access.json`.
- Тип, умолчание и проверку значения знает реестр настроек в Go
  (`internal/config`); запись в базу идёт только после проверки реестром.

## Why

- Отвергнуто: таблица на секцию, колонка на ключ (вариант B). Каждый новый ключ
  стоил бы миграции схемы.
- Отвергнуто: отдельные таблицы списков доступа `access_chat`, `access_root`
  (вариант C). Фильтр чатов строится в Go, SQL-запросам эти таблицы не нужны.
- Хэш отдельно: `settings.list` не может случайно отдать его вместе с настройками.

## Cost

- SQLite проверяет только, что значение это JSON; тип держит реестр. Значение,
  записанное в обход реестра (прямой SQL), ловит проверка при чтении: для
  `access.*` она закрывает всё.
- Если настройкам понадобятся запросы SQL по значению, схему придётся менять.
