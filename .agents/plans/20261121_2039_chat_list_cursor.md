# Step-by-step plan: курсор в `tg chat list`

ID: 20261121_2039_chat_list_cursor
Created: 2026-11-21
Owner: Мязин Евгений
Branch: feature/20261121_2039_chat_list_cursor
PR: —

## Status

- Current status: in_progress
- Last update: 2026-11-21
- Next safe step: Codex добавляет `--cursor` и `next_cursor` в `chat list`.

## Context and sources

- Verified by commands (2026-11-21, живой Daemon): `tg chat list --limit 500` возвращает 500 самых свежих диалогов, а в базе после запросов по типам оказалось 1 235 чатов. `tg chat list --type user --limit 500` тоже упёрся ровно в 500 людей, продолжить нечем: у команды нет курсора. Кроме того, `FLOOD_WAIT` на запросах подряд.
- Read: `internal/tgclient/client.go` (`Chats`, итератор `query.GetDialogs`), `internal/tgclient/cursor.go` (`encodeOpaqueCursor`), `internal/rpc/api.go` (`ChatListParams`), `internal/cli/chat_lists_cmd.go` (образец `--cursor` у других команд), `internal/daemon` обработчик `chat.list`, `docs/cli/chats.md`.

## Goal

`tg chat list --cursor <next_cursor>` продолжает список с того места, где остановился предыдущий запрос, так что за несколько запросов читается весь список диалогов аккаунта, в том числе все личные чаты.

## Scope

- In scope: параметр `cursor` в `ChatListParams`, поле `next_cursor` в ответе, флаг `--cursor`, непрозрачный курсор, продолжение через смещение списка диалогов Telegram (offset_date, offset_id, offset_peer), тесты, документация и схема.
- Out of scope: другие команды, поиск по именам.

## Main invariant

Закрытые чаты не раскрываются: ни в списке, ни в курсоре (курсор не должен содержать открытый ID, название или иной признак закрытого чата и должен быть непрозрачным для человека и агента). Курсор привязан к типу (`--type`): с другим типом он отклоняется кодом 2. Список по-прежнему сохраняет в базу только видимые чаты.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Тесты | `go test -count=1 ./internal/tgclient ./internal/daemon ./internal/cli ./internal/rpc` | зелёные | not_run |
| Полная проверка | `just check` | зелёная | not_run |
