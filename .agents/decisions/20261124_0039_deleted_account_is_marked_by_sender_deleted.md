---
decided: 2026-11-24
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [internal/tgclient/message.go, internal/store/migrations/0016_sender_deleted.sql, internal/store/telegram.go, internal/daemon/persist.go, internal/daemon/discovery.go, internal/rpc/api.go]
---
# Удалённый аккаунт автора помечается полем sender.deleted, а не выдуманным именем

## Context

После [имени автора из people](20261124_0027_live_message_sender_name_comes_from_people.md) на живой базе осталось 1316 сообщений с `sender_id` без имени. 725 из них пишет один пользователь супергруппы «ProFarm Болталка», которого Telegram не присылает с именем даже при чтении сообщения (`tg message get`). Предположение: удалённый аккаунт. Для него Telegram присылает `User` с `deleted: true` без имени и юзернейма.

## Decision

- `rpc.Sender` получает `deleted` (bool, `omitempty`). `senderOf` (`internal/tgclient/message.go`) ставит его из `User.Deleted`. Имя остаётся пустым.
- В `messages` колонка `sender_deleted INTEGER NOT NULL DEFAULT 0` (миграция `0016_sender_deleted.sql`). `rpcStoredMessage` отдаёт её как `sender.deleted`; её читают экспорт, контекст, поиск и веб-просмотр.
- Признак идёт в `people.deleted` и обратно: `upsertPeopleTx` при `deleted = 1` одним `UPDATE` помечает все сохранённые сообщения этого человека, поэтому старые строки не ждут чтения истории. Сообщение без объекта пользователя (`deleted` не пришёл) признак не снимает: `persist.go` пишет только `true`, слияние берёт старое значение.
- Миграция сама переносит `people.deleted = 1` в `messages`.

## Why

- Агенту нужно отличать удалённый аккаунт от неизвестного автора: первого имя не появится никогда, второго загрузка истории дополнит.
- Поле, а не имя «Deleted Account»: выдуманное имя ушло бы в `people.first_name` и в поиск людей, где его не отличить от настоящего.
- Колонка в `messages`, а не JOIN с `people` на чтении: читатели (экспорт, контекст, веб, поиск) уже получают строки через `messageColumns`, и каждый унаследует поле.

## Cost

- Колонка и миграция в живой базе (добавление столбца с `DEFAULT` мгновенное, проверка на копии: 667 840 строк, `integrity_check` ok).
- Работает только если гипотеза верна: для не удалённого аккаунта без имени признак не ставится. Проверка после деплоя: `tg message get -- -1001667018946 742364` должен вернуть `sender.deleted: true`.
- Остальные 75 безымянных авторов останутся как есть, пока их причина не изучена.
