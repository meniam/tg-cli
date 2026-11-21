# Step-by-step plan: название чата в строках заданий `tg status`

ID: 20261121_2026_status_job_chat_name
Created: 2026-11-21
Owner: Мязин Евгений
Branch: feature/20261121_2026_status_job_chat_name
PR: —

## Status

- Current status: in_progress
- Last update: 2026-11-21
- Next safe step: Codex добавляет название чата в данные панели и в строку задания.

## Context and sources

- Read: `internal/cli/status_cmd.go` (строка вида `Chat 110724171 · running (manual) · 62 pages · 6,200 messages loaded`), `internal/daemon/dashboard.go`, `internal/rpc/dashboard.go`, `internal/cli/testdata/status_*.golden`, `docs/cli/daemon-and-auth.md`.
- Просьба владельца 2026-11-21: после ID чата показывать название.

## Goal

В блоке HISTORY команды `tg status` каждая строка задания показывает название чата после его ID: `Chat 110724171 Имя Фамилия · running (manual) · ...`.

## Scope

- In scope: поле с названием в данных метода `dashboard` (берётся из локальной базы `chats.title`, без запросов к Telegram), вывод в `tg status`, golden-файлы и тесты, пример в `docs/cli/daemon-and-auth.md`. Название очищается от управляющих символов и обрезается до разумной длины; если названия нет в базе, строка остаётся как раньше.
- Out of scope: `tg chat sync status` и другие команды.

## Main invariant

Закрытые чаты не показываются (задания и так идут только по видимым чатам, но название не берётся для чата вне политики доступа). Панель по-прежнему не обращается к Telegram.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Тесты | `go test -count=1 ./internal/cli ./internal/daemon ./internal/rpc` | зелёные | not_run |
| Полная проверка | `just check` | зелёная | not_run |
