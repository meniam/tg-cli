# Step-by-step plan: автор сообщения без имени, возможно удалённый аккаунт

ID: 20261124_0036_deleted_account_sender
Created: 2026-11-24
Owner: Мязин Евгений
Branch: bug/20261124_0036_deleted_account_sender (поверх bug/20261124_0026_live_sender_name: миграции идут подряд)
PR: —

## Status

- Current status: done (код, тесты, деплой и живая проверка 2026-11-24)
- Last update: 2026-11-24
- Next safe step: нет; хвост закрыт планом 20261124_0049_deleted_people_from_dialogs.

## Context and sources

- Verified (2026-11-24, живая база, после миграции 0015): остались 1316 сообщений с `sender_id` без имени, 76 разных авторов. Из них 725 сообщений даёт `7918158229` в супергруппе «ProFarm Болталка» (`-1001667018946`, 2207 участников), 9 сентября до 17 октября 2026, все входящие. Решение про имена: `.agents/decisions/20261124_0027_live_message_sender_name_comes_from_people.md`.
- Verified: строка `people` для `7918158229` пустая (id, ни имени, ни юзернейма, `raw` пуст). `tg user profile 7918158229` отвечает `not_found`: сессия не знает `access_hash`.
- Verified: `tg message get -- -1001667018946 742364` (чтение из Telegram) возвращает `sender: {"id": 7918158229}` без имени, значит имя не пришло от самого Telegram. `tg chat members` показывает 5 участников группы, его среди них нет.
- Hypothesis (не проверено): аккаунт удалён или заблокирован. Для удалённого аккаунта Telegram присылает `User` с `deleted: true` без имени и юзернейма; `displayName` (`internal/tgclient/client.go:346`) возвращает пусто, `senderOf` (`internal/tgclient/message.go:89`) оставляет `{id}`. Тишина с 17 октября согласуется, но не доказывает. Вторая версия: пользователя нет в списке `users` ответа, `ent.User` не находит его.
- Ни одна команда `tg` не показывает сырой объект пользователя из ответа, поэтому различить версии без диагностики нельзя.

## Goal

Узнать, почему у 7918158229 (и у остальных 75 авторов) нет имени, и если автор удалённый аккаунт, показывать это явно: агент должен видеть «удалённый аккаунт», а не безымянный id.

## Scope

- In scope: диагностика, правка `senderOf` и, по решению Q1, поле или имя для удалённого аккаунта, тесты, `docs/cli/` (таблицы генерируются из `tg schema`, `just docs`), решение и запись changelog.
- Out of scope: запрос `users.getUsers` для остальных неизвестных авторов (вариант 2 из решения 20261124_0027).

## Open questions

| # | Вопрос | Варианты | Статус |
| --- | --- | --- | --- |
| Q1 | Как показать удалённый аккаунт | A. Поле `sender.deleted` и колонка (выбрано) | решён 2026-11-24, решение `20261124_0039_deleted_account_is_marked_by_sender_deleted` |

## Steps

- [x] Q1 решён владельцем: вариант A. Шаг 0 с диагностическим бинарником заменён проверкой после деплоя: `senderOf` ставит `deleted` из `User.Deleted`, так что `tg message get` на сообщение 742364 покажет флаг, если гипотеза верна.
- [x] `senderOf`, `rpc.Sender.Deleted`, колонка `sender_deleted` (миграция 0016), `upsertPeopleTx` помечает все сообщения удалённого человека, `rpcStoredMessage`, `persist.go`.
- [x] Тесты `TestSenderOfMarksDeletedAccount`, `TestDeletedSenderMarksStoredMessages`; миграция проверена на копии живой базы.
- [x] `docs/cli/messages-read.md`, навык `docs/skill/tg/references/history.md`, решение и changelog.
- [x] `just check`, `just skill-check` зелёные.
- [x] По КПД: бэкап `store.pre-0016-20261124_0041.db`, деплой, схема 16. Живая проверка: `tg message get -- -1001667018946 742364` вернул `sender.deleted: true`, все 725 сообщений пользователя 7918158229 помечены.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Гипотеза | после деплоя `tg message get -- -1001667018946 742364` | `sender.deleted: true` | ok |
| Полная проверка | `just check`, `just skill-check` | зелёные | ok |

## Run log

- 2026-11-24: план создан по замеру живой базы. Q1 = A. Реализовано, проверено на копии базы; не задеплоено.
