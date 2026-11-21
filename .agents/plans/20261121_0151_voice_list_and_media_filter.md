# Список заданий расшифровки, тип вложения в поиске и видео без звука

ID: 20261121_0151_voice_list_and_media_filter
Created: 2026-11-21
Owner: Мязин Евгений
Branch: `feature/20261121_0151_voice_list_and_media_filter` (worktree `../tg-cli-voice-list`)
PR: —

Исполнитель Go-кода: агент Claude Sonnet 5.5 в панели Herdr в своём worktree, режим `auto`. Менеджер проверяет, коммитит и сливает.

## Status

- Current status: in_progress (2026-11-21)
- Last update: 2026-11-21
- Next safe step: поток list: шаги 1-4, затем документы.

## Context and sources

- Живой запуск расшифровки 2026-11-21 показал пробелы: нельзя узнать, какие задания готовы, упали или пропущены (`tg voice status` даёт только счётчики, две ошибки `convert` не разобрать); `tg message get`, `history`, `context --local` не показывают расшифровку; локальный `tg search` не отдаёт тип вложения и не фильтрует по нему.
- Просьба владельца 2026-11-21: добавить это, а фильтр и признак типа сделать для голосовых, кружков, видео, картинок, файлов и gif. Распознавание содержимого картинок, файлов и gif не просили в этом плане (отдельный план при желании).
- Есть Telegram-аналог: `tg message search --media photo|video|document|voice|audio|link|gif|pinned`.

## Goal

Владелец и агент находят сообщения по типу вложения в локальной базе, видят расшифровку рядом с сообщением и список заданий очереди с причинами. Видео без звуковой дорожки не считается ошибкой.

## Scope

В объёме:
1. `tg voice list [--state queued|running|done|failed|skipped] [--chat <чат>] [--kind voice|video_note|video] [--limit N] [--cursor C]`: задания очереди (`chat_id`, `chat_title`, `message_id`, `kind`, `source`, `state`, `reason`, `attempts`, `bytes`, `enqueued_at`, `finished_at`), без текстов сообщений и расшифровок; закрытые `access.json` чаты не видны; класс `read`; страницы по `next_cursor`; сначала новые.
2. Поле `transcription` (`text`, `source`, `engine`; текст недоверенный) у сообщений там, где команда читает локальную базу: `tg message get`, `tg message context --local`, `tg history`/`export`, если они читают локальную базу (агент выясняет по коду и фиксирует список в отчёте).
3. Фильтр `--media` для локального `tg search` (область `messages`): `voice`, `video_note`, `video`, `photo`, `document`, `gif`, `audio`; значения берутся из того, что реально пишет `media_type` в базу (агент сверяет словарь по коду `internal/store` и `internal/tgraw`, `gif` и `photo` как у `message search --media`); у попадания поле `media_type`.
4. Видео и кружки без звуковой дорожки: `internal/voice/exec` отличает «в файле нет звука» (типизированная ошибка, определение по тексту ошибки `ffmpeg` без новых программ) от прочих сбоев конвертации; задание завершается `done` с причиной `no_audio`, пустого текста в базу не пишется (или пишется пустой, как у `no_speech`: агент выбирает по поведению соседнего кода и фиксирует).

Вне объёма: распознавание текста с картинок, файлов и gif, миграции схемы, поиск по содержимому файлов.

## Main invariant

Новые команды и поля только читают локальную базу и очередь, ни одного запроса к Telegram. Тексты сообщений и расшифровок в `voice list` не попадают. Закрытые чаты не видны ни в списке, ни в поиске. Формат ответов существующих команд меняется только добавлением полей.

## Steps

- [ ] Шаг 1. Поле `media_type` и фильтр `--media` в локальном поиске (store, daemon, rpc, cli, схема).
- [ ] Шаг 2. `tg voice list` (store-запрос с курсором, rpc `voice.list`, cli, схема).
- [ ] Шаг 3. Поле `transcription` в сообщениях локальных команд чтения.
- [ ] Шаг 4. `no_audio` в `internal/voice/exec` и в задании рабочего.
- [ ] Шаг 5. Документы: `docs/cli`, `docs/contracts.md`, скилл `docs/skill/tg`, `FEATURES.md`, запись в `.agents/changelog/` рецептом `just changelog add`.

## Verification

| Check | Command | Expected result | Status |
| ----- | ------- | --------------- | ------ |
| Тесты | `isocheck.sh` (gofmt, vet, все тесты) | CHECK_OK | not_run |
| Гонки | `go test -race` по затронутым пакетам | зелёные | not_run |
| Доки | тесты `Docs` и `skill-check` | зелёные | not_run |

## Open questions

Нет.
