# Step-by-step plan: глубина и пачка в `tg chat sync`

ID: 20261121_2107_chat_sync_depth_and_queue
Created: 2026-11-21
Owner: Мязин Евгений
Branch: feature/20261121_2107_chat_sync_depth_and_queue
PR: —

## Status

- Current status: done (код написан менеджером в сессии по просьбе владельца; деплой по команде КПД)
- Last update: 2026-11-21
- Next safe step: деплой по команде владельца, затем `tg chat sync queue --dry-run` на живом аккаунте.

## Context and sources

- Просьба владельца 2026-11-21: «Синхронизируй последний месяц», «Синхронизируй 100 сообщений», выбор чатов пачкой (люди с общением за последние 60 дней, незагруженные), по ответу «Этап 1 (Рекомендую)».
- Verified (2026-11-21, живой Daemon): из 500 людей в базе полностью загружены 33, у 467 нет ни одного сообщения; `tg chat sync start` умеет только полную загрузку (страницы по 100 назад от самого старого сохранённого до пустой страницы); демон грузит один чат за раз, остальные в очереди; задания живут в памяти и теряются при перезапуске.
- Read: `docs/contracts.md` (пункт Deep history load), `internal/daemon` менеджер истории (`history_sync*.go`), `internal/cli/chat*sync*`, `internal/rpc` методы `chat.sync.*`, `internal/tgclient` итератор диалогов.

## Goal

1. `tg chat sync start <чат>` принимает ограничитель глубины: `--messages N`, `--days N`, `--months N`, `--since ДАТА` (взаимоисключающие).
2. `tg chat sync queue` ставит в очередь пачку чатов по фильтрам (`--type`, `--active-days N`, `--unsynced`, `--max-chats N` и те же ограничители глубины), `--dry-run` только показывает, что было бы поставлено.
3. `tg chat sync cancel --all` сбрасывает всю очередь и текущее задание.

## Scope

- In scope: менеджер истории (условие остановки), RPC, CLI, дата последнего сообщения диалога для фильтра активности, схема, документация, тесты.
- Out of scope: расписание «вширь, потом вглубь», `--refresh`, приоритеты очереди, оценка времени в `tg status`.

## Main invariant

Частичная загрузка (по ограничителю) никогда не ставит `complete`: его ставит только пустой ответ Telegram. Следующая загрузка продолжается с `oldest_id`, ничего не теряется. Закрытые чаты не загружаются и не показываются ни пачкой, ни dry-run. Загрузка идёт через тот же дроссель.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Тесты | `go test -count=1 ./internal/daemon ./internal/cli ./internal/rpc ./internal/tgclient ./internal/store` | зелёные | ok |
| Полная проверка | `just check` | зелёная | ok |

## Run log

- 2026-11-21: реализовано ограничение глубины (`--messages`, `--days`, `--months`, `--since`), `chat sync queue` с `--dry-run`, `chat sync cancel --all`, поле `last_message_date` у диалога; тесты менеджера, CLI и документация; навык `docs/skill/tg` доработан тремя рецептами (sync-recent-history, sync-active-chats, sync-queue-control).
