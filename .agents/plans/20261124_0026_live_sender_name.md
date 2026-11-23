# Step-by-step plan: живые сообщения группы приходят без имени автора

ID: 20261124_0026_live_sender_name
Created: 2026-11-24
Owner: Мязин Евгений
Branch: bug/20261124_0026_live_sender_name
PR: —

## Status

- Current status: in_progress (код, тесты, документы и `just check` готовы; деплой впереди)
- Last update: 2026-11-24
- Next safe step: по команде КПД: бэкап базы, деплой (миграция 0015 заполнит имена в живой базе).

## Context and sources

- Verified (2026-11-24, `tg message export --local` по «4P HR Core» с 1 ноября): у всех 235 сообщений есть `sender.id`, у 118 нет имени, все они после id 1090463 (`newest_id` фоновой загрузки). Живое чтение `tg message history` вернуло имена тех же id и записало их в базу.
- Verified (код): gotd v0.166.0 строит из `UpdateShortChatMessage` обновление без `Users` и `Chats` (`telegram/updates/conv_shorts.go:78-97`), `senderOf` (`internal/tgclient/message.go:89`) не находит пользователя.
- Verified (живая база, агрегаты): 2648 из 667 817 сообщений имеют `sender_id` без `sender_name`; у 1332 имя есть в `people`.
- Решение: `.agents/decisions/20261124_0027_live_message_sender_name_comes_from_people.md`. Владелец выбрал варианты 1 и 3 (2026-11-24).

## Goal

Живое сообщение получает имя автора из `people` до записи и доставки, а уже сохранённые безымянные строки с известным автором получают имя один раз.

## Scope

- In scope: `internal/daemon/events.go` (`withSenderName`), миграция `internal/store/migrations/0015_sender_names.sql`, тесты, `docs/contracts.md`, запись changelog.
- Out of scope: запрос `users.getUsers` для неизвестных авторов; 726 сообщений одного пользователя супергруппы, которого нет в `people` (другая причина, не исследована); 1316 строк без человека в `people`.

## Main invariant

Существующее имя автора не заменяется. Событие, у которого нечего заполнять, доставляется теми же байтами. Запросов в Telegram нет.

## Steps

- [x] Подстановка имени в `handleEvent` (`withSenderName`), тест `TestEventFillsSenderNameFromPeople`: падает без правки, проходит с ней.
- [x] Миграция `0015_sender_names.sql`, тест `TestSenderNamesMigration`: падает без миграции, проходит с ней. Проверка на копии живой базы: 2648 → 1316 безымянных, в «4P HR Core» 0.
- [x] `docs/contracts.md`, решение и запись changelog.
- [x] `just check` зелёный.
- [ ] По КПД: бэкап, деплой, проверка `tg message export --local` по «4P HR Core» после нескольких живых сообщений.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| События | `go test -count=1 -run 'TestEvent' ./internal/daemon` | зелёные | ok |
| Миграция | `go test -count=1 ./internal/store` | зелёные | ok |
| Копия живой базы | `sqlite3` на копии, `0015_sender_names.sql` | 2648 → 1316, HR Core 0 | ok |
| Полная проверка | `just check` | зелёный | ok |

## Run log

- 2026-11-24: исследование причины (gotd, живая база, чтение истории), правка, тесты, документы.
