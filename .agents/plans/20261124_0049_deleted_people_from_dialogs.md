# Step-by-step plan: удалённые собеседники личных чатов без признака deleted

ID: 20261124_0049_deleted_people_from_dialogs
Created: 2026-11-24
Owner: Мязин Евгений
Branch: bug/20261124_0049_deleted_people_from_dialogs (поверх bug/20261124_0036_deleted_account_sender: миграции идут подряд)
PR: —

## Status

- Current status: done (код, тесты, деплой и проверка на живой базе 2026-11-24)
- Last update: 2026-11-24
- Next safe step: нет.

## Context and sources

- Verified (2026-11-24, 75 чтений с паузой 2 с, по сообщению на автора): из 75 безымянных авторов 6 в группах (4 удалённых, помечены; 2 живых, получили имена) и 69 в личных чатах: `tg message get` отвечает `not_found` (сессия не знает `access_hash`).
- Verified (живая база): у всех 69 чатов в `chats.raw` и `people.raw` есть `"deleted":true` от `messages.getDialogs`, а колонка `people.deleted` равна 0. Всего 71 человек с признаком в `raw` и 0 в колонке, 639 сообщений без пометки.
- Решение: `.agents/decisions/20261124_0050_deleted_flag_of_people_comes_from_the_stored_object.md`. Предыдущее: `20261124_0039_deleted_account_is_marked_by_sender_deleted.md`.

## Goal

Колонка `people.deleted` и `messages.sender_deleted` следуют объекту Telegram, сохранённому в `raw`, для всех путей записи людей; сохранённые данные исправляются один раз.

## Scope

- In scope: `internal/store/raw.go` (`rawDeleted`), `upsertPeopleTx`, миграция `0017_deleted_people_and_names.sql`, тесты, решение, запись changelog.
- Out of scope: публичная схема (поле `Chat.deleted`), запросы в Telegram.

## Main invariant

`deleted: false` не снимает признак. Запросов в Telegram нет. Имя, уже записанное в `sender_name`, миграция не заменяет.

## Steps

- [x] `rawDeleted` и вызов в `upsertPeopleTx`; тест `TestUpsertPeopleReadsDeletedFromRaw` падает без правки, проходит с ней.
- [x] Миграция `0017`: признак из `raw` (плоский объект и с ключом метода), пометка сообщений, повторное заполнение имён; тест `TestDeletedPeopleMigration`. Проверка на копии живой базы: людей 5 → 76, сообщений 729 → 1368, безымянных без пометки 585 → 0, `integrity_check` ok.
- [x] Решение и запись changelog.
- [x] `just check` зелёный.
- [x] По КПД: бэкап `store.pre-0017-20261124_0057.db`, деплой, схема 17: людей с `deleted = 1` 76, сообщений с `sender_deleted = 1` 1368, безымянных без пометки 0; экспорт личного чата `169046910` отдаёт 47 сообщений с `sender.deleted: true`.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Миграция и upsert | `go test -count=1 ./internal/store` | зелёные | ok |
| Копия живой базы | `sqlite3`, `0017_deleted_people_and_names.sql` | 76 людей, 1368 сообщений, 0 безымянных без пометки | ok |
| Полная проверка | `just check` | зелёный | ok |

## Run log

- 2026-11-24: замер по 75 авторам, правка, тесты, проверка на копии базы.
