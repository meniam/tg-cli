# Step-by-step plan: `tg autostart on` не загружает задание при работающем демоне

ID: 20261121_2143_autostart_wait_for_a_manual_daemon
Created: 2026-11-21
Owner: Мязин Евгений
Branch: bug/20261121_2143_autostart_wait_for_a_manual_daemon
PR: —

## Status

- Current status: done (код и тесты; деплой по команде КПД)
- Last update: 2026-11-21
- Next safe step: деплой, затем `tg daemon stop` и `tg autostart on`, если демон работает вне launchd.

## Context and sources

- Verified (2026-11-21, живая машина, `tg autostart on` при работающем демоне): `launchctl print` показал `state = spawn scheduled`, `runs = 1`, `last exit code = 1`. `tg daemon start --foreground` при работающем демоне профиля завершается кодом 1 (`a daemon is already running for this profile`), а `KeepAlive: SuccessfulExit=false` повторяет запуск каждые 30 секунд, и после `tg daemon stop` ручного демона launchd поднял бы новый, то есть демон нельзя остановить.

## Goal

Если демон профиля уже работает, `tg autostart on` пишет файл задания, но не загружает его: launchd загрузит его при следующем входе, а ответ объясняет, как поставить демон под launchd сразу (`tg daemon stop` и `tg autostart on` ещё раз).

## Scope

- In scope: `internal/cli/autostart_cmd.go`, тесты (изоляция от реального профиля), тексты в `docs/cli/daemon-and-auth.md` и навыке.
- Out of scope: изменение поведения `daemon start --foreground`.

## Main invariant

`tg daemon stop` оставляет демон остановленным (перезапуск launchd только после сбоя). Тесты не обращаются к сокету настоящего демона.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Тесты | `go test -count=1 ./internal/cli` | зелёные, есть тест с работающим демоном (сокет) | ok |
| Полная проверка | `just check`, `just skill-check` | зелёные | ok |

## Run log

- 2026-11-21: исправлено после проверки на живой машине; тесты autostart изолированы от `~/.config/tg-cli`.
