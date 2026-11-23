---
decided: 2026-11-24
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/store/raw.go, internal/store/telegram.go, internal/store/migrations/0017_deleted_people_and_names.sql]
---
# Признак deleted у человека берётся из сохранённого объекта Telegram в одном месте, upsertPeopleTx

## Context

После [решения про `sender.deleted`](20261124_0039_deleted_account_is_marked_by_sender_deleted.md) на живой базе осталось 585 безымянных сообщений от 71 автора. Замер 2026-11-24: 69 из них личные чаты, где `tg message get` отвечает `not_found` (сессия не знает `access_hash`), а в `chats.raw` и `people.raw` лежит `{"messages.getDialogs":{"_":"user","deleted":true,"id":...}}`. Колонка `people.deleted` у всех 71 была 0: путь диалогов (`chatPerson` в `internal/daemon/persist.go`) её не заполнял, а `sender.deleted` и пометка сообщений идут от этой колонки.

## Decision

- `store.rawDeleted` читает `deleted` из `raw` человека: объект плоский или с ключом метода (`messages.getDialogs`, `users.getUsers`).
- `upsertPeopleTx` вызывает её для каждого входящего человека и ставит `Deleted = true` до слияния. Все пути записи людей (диалоги, участники, контакты, сообщения) получают признак без правки каждого. Значение `false` не снимает старое `true`.
- Колонка `people.deleted = 1` помечает все сохранённые сообщения человека (`UPDATE messages SET sender_deleted=1`, решение 20261124_0039).
- Миграция `0017_deleted_people_and_names.sql` один раз делает то же для сохранённых данных: `people.deleted` из `raw`, затем `messages.sender_deleted`. В неё же включено повторное заполнение имён из `people`, как в 0015: имена, попавшие в `people` после 0015 (живое чтение одного сообщения), дополняют старые строки.
- Публичная схема не меняется: поле `Chat` не получает признака.

## Why

- Одно место вместо правки каждого пути записи людей: признак уже в `raw`, который сохраняют все.
- Запросов в Telegram нет: для этих чатов они всё равно невозможны (`not_found`).
- Проверка на копии живой базы: людей с `deleted = 1` 5 → 76, сообщений с `sender_deleted = 1` 729 → 1368, безымянных без пометки 585 → 0, `integrity_check` ok.

## Cost

- Миграция меняет строки живой базы за один проход; перед деплоем нужен бэкап.
- Признак берётся из последнего сохранённого объекта и может устареть, если Telegram вернёт аккаунт: `false` не снимает `true`. Для удалённого аккаунта это не наблюдалось.
