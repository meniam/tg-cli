# Пошаговый план: расшифровка голосовых, кружков и видео локальной моделью через очередь

ID: 20261120_2002_voice_transcription_local_queue
Created: 2026-11-20
Owner: Мязин Евгений
Branch: `feature/20261120_2002_voice_transcription_local_queue` (worktree `../tg-cli-voice-local`)
PR: —

Исполнители Go-кода: агенты Claude Sonnet 5.5 в панелях Herdr, по одному на поток в своём worktree, в режиме `auto` (порядок работы как у плана `20261120_1826_semantic_search_bge_m3`). Каждый загружает скиллы `expert-golang` и `expert-testing`. Менеджер проверяет, коммитит и сливает.

## Status

- Current status: ready_to_merge (2026-11-21: шаги 2-10 сделаны, все потоки слиты в ветку плана, полный `-race` идёт; деплой по команде владельца)
- Last update: 2026-11-21
- Next safe step: дождаться итогового `-race`, слить ветку в `main`, по команде владельца: копия базы, `just install`, `tg voice install`, включение, первая живая расшифровка.
- Связь с планом `20261120_1826_semantic_search_bge_m3`: шаг 15 того плана (расшифровки в поиске и индексе, поток voice) остаётся там и нужен этому плану как потребитель текста. Шаги 16 и 18 того плана перенесены сюда (отмечены в нём ссылкой).

## Context and sources

- Просьбы владельца 2026-11-20 (дословный смысл): голосовые в личных чатах и в отмеченных чатах расшифровывать при получении и при синхронизации чата; всё гнать через локальную модель и не нагружать Telegram API; расшифровывать через очередь; пределы скачивания: голосовое до 30 МБ, кружок до 50 МБ, видео до 500 МБ и 2 часа; метки чатов ставить командой в базе.
- Образец: проект `/Users/eugene/www/4p/stenograf` (Swift, macOS): `Sources/Stenograf/GigaAM.swift`. Движок GigaAM v3 (Сбер, MIT) через официальную сборку `sherpa-onnx` (`sherpa-onnx-v2.0.1-osx-arm64-shared-no-tts`), модель `sherpa-onnx-nemo-transducer-punct-giga-am-v3-russian-2025-12-16` (около 170 МБ), `silero_vad.onnx`; запуск программы `sherpa-onnx-vad-with-offline-asr` с аргументами `--silero-vad-model=… --encoder=…/encoder.int8.onnx --decoder=…/decoder.onnx --joiner=…/joiner.onnx --tokens=…/tokens.txt --model-type=nemo_transducer --num-threads=4 <wav>`; на входе WAV 16 кГц с простым заголовком; строки результата `7.974 -- 9.996: Текст фразы.`; в `stderr` служебные сообщения, в `stdout` результат (их нельзя смешивать, иначе в середину русской буквы попадала служебная строка и часовая расшифровка терялась, запись у них от 2026-11-19); `DYLD_LIBRARY_PATH` указывает на каталог `lib` рантайма; GigaAM берёт не больше 25 секунд за раз, длинный звук режет `silero-vad`. По их комментарию модель быстрее Whisper на порядок (это оценка автора, у нас не измерена).
- Существующий код:
  - `internal/daemon/transcribe.go`, `internal/tgclient/transcribe.go`: расшифровка силами Telegram (`messages.transcribeAudio`), ожидание обновления `updateTranscribedAudio` (`handleTranscription`), ограничение «только `voice` и `video_note`», ошибки `premium_required` и `transcription_failed`.
  - `internal/store/transcriptions.go`, миграция `0007_transcriptions.sql`: таблица `transcriptions` (`chat_id`, `message_id`, `transcription_id`, `text`, `pending`, `updated_at`, `raw`), методы `GetTranscription`, `PutTranscription`, `UpdateTranscription`.
  - Скачивание медиа: `internal/tgclient/download.go` (`Download`, `downloadLocation`), `internal/fileroot` (пишет только в каталоги `downloads` владельца из `access.json`, **предел 100 МиБ на файл**, `fileroot.MaxFileSize = 100 << 20` в `internal/fileroot/open.go`), пул частей на медиа-DC, пять рабочих, окно 4-8 частей, не больше двух медиапередач одновременно на демон, проверка SHA-256 и размера (`docs/cli/messages-read.md`, `### tg message download`).
  - Дроссель: `UploadGetFileRequest` относится к классу `throttle.File` (`internal/tgclient/throttle.go:19`), общий предел 18 запросов в секунду и не больше двух одновременно (решение `20261114_0310`), фоновая метка `throttle.WithBackground`.
  - Источники сообщений: поток событий (`internal/daemon/events.go`, `handleEvent`), сохранение страницы истории (`store.SaveHistoryPage`, `internal/daemon/history_sync.go`), планировщик `background_sync`.
  - Поиск и индекс используют текст расшифровки по шагу 15 плана `20261120_1826_semantic_search_bge_m3` (`transcriptions` в `search_text`, `MessagesForFragments`, `FragmentText`).
- Факты базы владельца (2026-11-20, профиль `default`): `media_type` `voice` 356 (238 в личных чатах), `video_note` 58, `video` 3 471 (2 513 за последние 90 дней); `transcriptions` 0 строк; аккаунт Premium (`tg account profile`: `premium: true`); у `messages.raw` у 121 из 356 голосовых нет блока `media` (старый формат), размер и длительность из базы известны не всегда.
- Документация Telegram (`core.telegram.org/api/transcribe`): аккаунты без Premium расшифровывают ограниченное число сообщений в неделю и ограниченной длительности (числа в настройках клиента), у Premium такого предела нет, других чисел нет; ошибки метода `messages.transcribeAudio`: `MSG_VOICE_MISSING`, `MSG_VOICE_TOO_LONG`, `PREMIUM_ACCOUNT_REQUIRED`, `TRANSCRIPTION_FAILED`. В этом плане метод автоматически не вызывается.
- Не проверено (и не будет проверяться до первого живого запуска, по решению владельца «без спайка»): скорость GigaAM на M1 Max, качество на голосовых Telegram (Opus, 16-48 кГц), поведение `ffmpeg` на кружках `video_note` (MPEG-4) и на длинных видео, имена и размеры файлов в текущем выпуске sherpa-onnx (Step 1), доступность одного кусочка `getFile` на файле 500 МБ без `FLOOD_WAIT`.

## Goal

Каждое голосовое и каждый кружок из личных и отмеченных чатов, а также любое видео по команде или метке, получают текст, который пишет локальная модель GigaAM на этой машине. Работа идёт одной постоянной очередью, видна в статусе и не ходит в Telegram за расшифровкой: нагрузка на Telegram только скачивание файла в заданных пределах.

## Problem and contract

Тип плана: product (меняются данные, команды и поведение демона).

**1. Данные** (одна миграция, номер следующий свободный; необратима для старых бинарников, нужна копия базы):
- `transcriptions`: столбцы `source TEXT NOT NULL DEFAULT 'telegram'` (`telegram` или `local`), `engine TEXT NOT NULL DEFAULT ''` (`gigaam-v3`), `duration REAL` (секунды звука, если известна).
- `voice_chats(chat_id INTEGER PRIMARY KEY, video INTEGER NOT NULL DEFAULT 0, enabled_at INTEGER NOT NULL)`: метка «расшифровывать голосовые» (и видео, если `video = 1`). Личные чаты (`chats.type = 'user'`) метки не требуют.
- `voice_queue(chat_id, message_id, kind, source, priority, state, reason, attempts, bytes, enqueued_at, started_at, finished_at, PRIMARY KEY(chat_id, message_id))`: `kind` из `voice`, `video_note`, `video`; `source` вызова из `manual`, `incoming`, `sync`, `backfill`; `priority` 0 ручная, 10 входящая, 20 синхронизация, 30 backfill; `state` из `queued`, `running`, `done`, `failed`, `skipped`; `reason` короткая причина без текста сообщений.
- Таблица `voice_usage(day TEXT PRIMARY KEY, files INTEGER, bytes INTEGER)` считает суточный бюджет скачиваний (день по локальной дате).

**2. Движок** (`internal/voice/gigaam`):
- Файлы лежат в `<home>/models/gigaam/` (`home` из `config.toml`, по умолчанию `~/.config/tg-cli`), один набор на все профили (Q1): рантайм `sherpa-onnx` (программа и каталог `lib`), модель (`encoder.int8.onnx`, `decoder.onnx`, `joiner.onnx`, `tokens.txt`), `silero_vad.onnx`.
- Установка `tg voice install`: скачивание с `github.com/k2-fsa/sherpa-onnx/releases` только по закреплённым в коде ссылкам и SHA-256 (имена и суммы Step 1 берёт из текущего выпуска и фиксирует), проверка суммы до распаковки, распаковка `tar.bz2` средствами Go (`compress/bzip2`, `archive/tar`) с защитой от выхода за каталог (`..`, абсолютные пути, ссылки), атомарная замена каталога, права 0700 на каталог и 0600 на файлы, программа 0700. Повторный запуск проверяет и докачивает. Без интернета команда падает с понятной причиной и ничего не ломает.
- Запуск программы без оболочки: `exec.CommandContext(binary, args...)` с аргументами из раздела выше, окружение `DYLD_LIBRARY_PATH=<lib>` и больше ничего, каталог запуска пустой временный, тайм-аут `max(120 с, 1,0 × длительность звука)` и жёсткий потолок 3 часа, `stdout` и `stderr` читаются раздельно, `stdout` ограничен по размеру (например 16 МБ), разбор строк `начало -- конец: текст` регулярным выражением, битые байты UTF-8 не ломают результат (чтение как байтов и замена, как в stenograf).
- `ffmpeg` (Q2): путь из `[voice_transcription] ffmpeg` или первый `ffmpeg` в `PATH`; запуск `ffmpeg -nostdin -v error -y -i <вход> -vn -ac 1 -ar 16000 -c:a pcm_s16le <выход>.wav`; выход только WAV PCM16; тайм-аут и потолок как выше; вывод `stderr` в причину ограничен 200 символами и не содержит имён файлов.

**3. Задание очереди** (работа одного рабочего):
1. Проверка: чат открыт `access.json` (закрытый чат даёт `skipped: closed`), сообщение есть в базе и его `media_type` подходит (`voice`, `video_note`, а `video` только при команде или метке с `video`), готовой расшифровки нет (или задание `retry`), движок и `ffmpeg` установлены (иначе `failed: engine_missing`, очередь приостанавливается, а не пересыпается отказами).
2. Скачивание (`internal/tgclient`, новая функция `DownloadToFile` с целью во временный закрытый каталог демона `<database dir>/tmp-voice/`, режим 0700, без привязки к `downloads` владельца: файл не виден пользователю и удаляется после использования; предел размера задаётся вызывающим, не `fileroot.MaxFileSize`; пул частей, дроссель `File`, фоновая метка, `FLOOD_WAIT` ждёт общий дроссель, проверка ожидаемого размера и SHA-256, где Telegram его отдаёт). Предел до начала скачивания по размеру из метаданных сообщения: голосовое 30 МБ, кружок 50 МБ, видео 500 МБ; длительность из атрибутов сообщения: видео до 120 минут (если длительности нет, предел действует по размеру и по тайм-ауту `ffmpeg`). Превышение даёт `skipped: too_large` или `too_long` без скачивания.
3. `ffmpeg` в WAV, затем `sherpa-onnx`; результат разбирается в сегменты `(start, end, text)`; пустой результат даёт `done` с пустым текстом и причиной `no_speech`.
4. Сохранение: `PutTranscription` или `UpdateTranscription` с `source = 'local'`, `engine = 'gigaam-v3'`, `pending = 0`, текст сегментов через пробел, сегменты с временами в `raw` JSON; затем сигнал `triggerSemanticIndex` (шаг 15 плана поиска).
5. Очистка: временные файлы удаляются на любом исходе (в том числе по отмене и панике), при старте демона каталог `tmp-voice/` чистится.

**4. Очередь:** один рабочий на демон; выбор задания по `priority`, затем по `enqueued_at`; дубль `(chat_id, message_id)` не ставится (повторный `enqueue` поднимает приоритет, если новый выше); `running` после падения демона возвращается в `queued` при старте; сетевые сбои скачивания повторяются до 3 раз с паузой 10, 60, 300 секунд, остальные отказы без повтора; пауза между скачиваниями `pause` (3 с); суточный бюджет `daily_download_mb` (2000) и `daily_files` (200): исчерпание приостанавливает очередь до следующих суток (статус `paused: budget`), ручное задание бюджет не проверяет, но считается в нём; отмена `tg voice cancel` останавливает текущее задание после очистки; очередь переживает перезапуск.

**5. Источники:**
- Ручная команда `tg message transcribe <чат> <id>` (и по ссылке): ставит `manual`, по умолчанию ждёт результат до `--wait` (60 с, до 600 с), при превышении отвечает `pending: true`; ответ получает `source` и `engine`; `--telegram` просит Telegram напрямую (старое поведение, единственный путь к `messages.transcribeAudio`, нужен Premium).
- Входящие: событие нового `voice` или `video_note` не от себя в личном чате или в чате с меткой ставит `incoming` (видео только при метке с `video`).
- Синхронизация: сообщения, которые сохранила `SaveHistoryPage` при `chat sync` и `background_sync`, ставятся `sync` по тем же правилам чатов; в базу пишется только то, что уже сохранено; сообщения старше 90 дней при синхронизации не ставятся (Q3).
- `--backfill`: `tg chat voice on <чат> --backfill` ставит `backfill` уже сохранённые голосовые чата; для личных чатов `tg voice backfill` по явной команде (числа до запуска показываются `--dry-run`).
- Источники работают, только если `[voice_transcription] enabled = true`; ручная команда работает всегда.

**6. Команды:**
- `tg voice install [--check]`, `tg voice status`, `tg voice cancel [<чат> <id>]`, `tg voice retry <чат> <id>`, `tg voice backfill [--dry-run]` (классы безопасности: `install` и `backfill` и `retry` `local_write`, `status` `read`, `cancel` `local_write`; Telegram не вызывают, кроме скачивания).
- `tg chat voice on|off <чат> [--video] [--backfill]`, `tg chat voice list` (класс `local_write` и `read`).
- `tg message transcribe` меняет результат: поля `source`, `engine`, `queued`, `cached` остаются.

**7. Конфиг `[voice_transcription]`** (переменные `TG_CLI_VOICE_TRANSCRIPTION_*`, проверка значений с кодом 2, выключено по умолчанию):

```toml
[voice_transcription]
enabled = false
ffmpeg = "ffmpeg"
max_voice_mb = 30
max_video_note_mb = 50
max_video_mb = 500
max_video_minutes = 120
daily_download_mb = 2000
daily_files = 200
pause = "3s"
threads = 4
```

**8. Статус:** блок `voice` в `daemon status` (установлен ли движок, длина очереди по состояниям, сделано сегодня, скачано сегодня МБ, `paused` с причиной, последняя причина отказа) и строка `Voice` в `tg status`.

## Scope

- В объёме: движок GigaAM, установка, `ffmpeg`-конвейер, очередь и три таблицы, скачивание во временный каталог, команды `tg voice`, `tg chat voice`, `tg message transcribe`, источники (события, синхронизация, backfill), конфиг, статус, документы, решения.
- Вне объёма: распознавание нерусской речи (GigaAM только русский), Whisper как второй движок, расшифровка собственных исходящих голосовых, автоматические запросы `messages.transcribeAudio`, перевод речи, разделение собеседников, расшифровка звука из других типов вложений (документы-аудио, `audio`), показ времён реплик в `tg message get`.
- Затрагиваются: `internal/voice` (новый), `internal/tgclient` (скачивание), `internal/store`, `internal/daemon`, `internal/config`, `internal/cli`, `internal/rpc`, `docs/`, `README.md`, `FEATURES.md`, `docs/skill/tg`, `.agents/decisions/`.

## Main invariant

Без `[voice_transcription] enabled = true` демон не ставит заданий сам и не делает ни одного нового запроса к Telegram (ручная команда по просьбе делает). Ни один путь, кроме явного `--telegram`, не вызывает `messages.transcribeAudio`. Закрытые `access.json` чаты не попадают в очередь и не скачиваются. Скачанные файлы лежат только во временном закрытом каталоге демона и удаляются на любом исходе. Тексты расшифровок помечены как недоверенные данные Telegram везде, где их выдают. Сторонние программы запускаются без оболочки с фиксированными аргументами; архивы проверяются по SHA-256 до распаковки.

## Risks and strategy

- Риск: нагрузка на Telegram (скачивание файлов до 500 МБ). Решение: пределы владельца, один рабочий, пауза, суточный бюджет 2 ГБ и 200 файлов, дроссель `File` с фоновой меткой, видео только по команде или метке; первые сутки владелец смотрит `tg voice status` и `throttled_ms`.
- Риск: двухчасовое видео занимает рабочего на неизвестное время (скорость GigaAM не измерена). Решение: тайм-аут по длительности и потолок 3 часа, ручные задания и входящие идут перед ним в очереди (между заданиями), отмена командой; первая живая расшифровка видео даёт скорость, и пределы пересматриваются.
- Риск: архив sherpa-onnx подменён или испорчен. Решение: закреплённые ссылки и SHA-256, проверка до распаковки, защита распаковки от выхода за каталог, права файлов; обновление версии только правкой кода.
- Риск: `ffmpeg` отсутствует или другой версии. Решение: `tg voice status` проверяет, очередь приостанавливается с причиной `engine_missing`, а не заваливает отказами.
- Риск: переполнение диска временными файлами. Решение: каталог `tmp-voice/` удаляется после каждого задания, чистится при старте, один рабочий, потолок размера по пределам, 500 МБ видео и WAV около 230 МБ на 2 часа проверяются перед запуском (свободное место не меньше удвоенного размера, иначе `failed: disk`).
- Риск: русский движок на нерусской речи даёт мусор. Решение: честное предупреждение в документах; мусорный текст попадает в поиск как есть (Q4).
- Риск: миграция необратима. Решение: копия базы перед деплоем.

## Decision log

| Date       | Decision | Reason | Confirmed |
| ---------- | -------- | ------ | --------- |
| 2026-11-20 | Всё идёт через локальную модель, `messages.transcribeAudio` автоматически не вызывается | Требование владельца: не нагружать Telegram API | yes |
| 2026-11-20 | Все задания идут через одну постоянную очередь `voice_queue` с приоритетами | Требование владельца | yes |
| 2026-11-20 | Метка «расшифровывать голосовые» хранится в локальной базе и ставится `tg chat voice on|off` | Выбор владельца | yes |
| 2026-11-20 | Пределы скачивания: голосовое 30 МБ, кружок 50 МБ, видео 500 МБ и 2 часа | Пределы владельца | yes |
| 2026-11-20 | Движок GigaAM v3 через внешнюю программу sherpa-onnx и ffmpeg, без cgo, по образцу stenograf | Выбор владельца (без спайка), Telegram не расшифровывает обычное видео | yes |
| 2026-11-20 | Временные файлы в закрытом каталоге демона, не в `downloads` владельца; предел размера 100 МиБ у `fileroot` на этот путь не действует | Файл не покидает демон и удаляется; правило `downloads` защищает каталоги владельца | yes |
| 2026-11-20 | Суточный бюджет скачиваний 2 ГБ и 200 файлов, пауза 3 секунды, повторы сети 3 раза | Осторожные значения менеджера, чисел Telegram для скачивания нет | yes |
| 2026-11-20 | Модели лежат в `<home>/models/gigaam/`, один набор на все профили | Модель около 190 МБ, одинакова для профилей | yes |
| 2026-11-20 | Своё исходящее голосовое не расшифровывается, обычное видео ставится только по команде или метке с `video` | Экономия скачиваний; 3 471 видео в базе | yes |
| 2026-11-20 | Тексты сегментов с временами хранятся в `transcriptions.raw`, в поиск идёт склеенный текст | Времена пригодятся ссылке на место в записи, поиск их не использует | yes |
| 2026-11-20 | `tg voice install` работает в процессе CLI, `internal/cli` импортирует `internal/voice/gigaam`; строка про этот импорт добавляется в `AGENTS.md` при слиянии потока worker | Установка не требует демона и Telegram; пакет не знает про Telegram и store, граница слоёв не размывается | yes |
| 2026-11-20 | Пауза между повторами скачивания (10, 60, 300 с) живёт в памяти рабочего, столбца `not_before` в `voice_queue` нет | Выбор владельца: проще, миграция не меняется; при перезапуске демона пауза теряется | yes |

## Step 0. Fix decisions before changes

- [x] Ответить на Q1-Q5 и принять строки `no` из Decision log (2026-11-20: всё принято по рекомендациям, владелец «давай голосовые»).
- [x] Ветка `feature/20261120_2002_voice_transcription_local_queue` от `main` (план поиска слит) и worktree `../tg-cli-voice-local`, записано в заголовок.
- [x] Решения в `.agents/decisions/`: `20261120_2238_voice_is_transcribed_locally_by_gigaam_through_a_persistent_queue`, `20261120_2239_voice_files_download_to_a_private_temp_dir_under_a_daily_budget`. Записи о команде `transcribe` среди решений нет (только про `raw`), `revises` не нужен.

## Step 1. Audit and baseline

- [x] Проверить текущий выпуск `k2-fsa/sherpa-onnx` (`gh api repos/k2-fsa/sherpa-onnx/releases`): имена и размеры файлов рантайма `osx-arm64` и модели `giga-am-v3-russian`, наличие `silero_vad.onnx`, SHA-256 (скачать в `var/agents/plans/<plan>/` и посчитать, закрепить в плане и коде). Записать, совпадает ли версия с `v2.0.1` из stenograf. Сделано 2026-11-20: v2.0.1 (последний выпуск v2.0.2, закреплён v2.0.1), суммы и размеры сверены, настоящие архивы распаковываются.
- [x] Проверить `ffmpeg` на машине владельца (`ffmpeg -version`), кодеки Opus и AAC (`ffmpeg -codecs | grep -i -E "opus|aac"`); если версии нет, записать вывод. ffmpeg 9.0.2, Opus и AAC есть.
- [x] Прочитать `internal/tgclient/download.go` целиком: как узнать размер и длительность до скачивания, как работает пул, можно ли подставить свой приёмник вместо `fileroot`; записать, какая правка нужна для `DownloadToFile`. Сделано потоками queue, worker, sources (отчёты в `var/agents/plans/<plan>/`).
- [x] Выяснить, как узнать `duration` и `size` голосового, кружка и видео из сохранённого сообщения (`messages.raw`) и из ответа Telegram, и что делать при отсутствии (старый формат `raw`). Сделано потоками queue, worker, sources (отчёты в `var/agents/plans/<plan>/`).
- [x] Определить места вставки источников: обработка нового сообщения в `handleEvent`, `SaveHistoryPage`, `background_sync`. Сделано потоками queue, worker, sources (отчёты в `var/agents/plans/<plan>/`).
- [-] Базовая линия: `just check` и время пакетов `internal/store`, `internal/daemon` в `var/agents/plans/20261120_2002_voice_transcription_local_queue/baseline.md`. Не делалась: время пакетов не нужно для решений плана.

## Implementation steps

- [x] Шаг 2. Миграция (`transcriptions.source`, `engine`, `duration`, `voice_chats`, `voice_queue`, `voice_usage`), методы store для очереди (постановка с приоритетом и дублем, взятие, завершение, возврат `running` в `queued`, подсчёт бюджета, метки чатов), тесты на очередь (приоритеты, дубль, перезапуск, повторы, бюджет).
- [x] Шаг 3. Установка движка: `internal/voice/gigaam` (пути, закреплённые ссылки и SHA-256, скачивание с проверкой до распаковки, распаковка `tar.bz2` без выхода за каталог, права, атомарная замена), `tg voice install [--check]`; тесты на подменном сервере (испорченный архив отвергнут, `..` и абсолютные пути отвергнуты, повторный запуск, обрыв).
- [x] Шаг 4. Запуск `ffmpeg` и `sherpa-onnx`: чтение раздельных потоков, ограничения по размеру вывода, тайм-аут по длительности, окружение, разбор строк результата; тесты на подменных исполняемых файлах (скрипты в временном каталоге), в том числе вывод с битыми байтами UTF-8 и с вклинившейся служебной строкой.
- [x] Шаг 5. Скачивание `DownloadToFile` во временный закрытый каталог демона с пределом размера, дросселем `File` и фоновой меткой; удаление на любом исходе и очистка при старте; тесты на подменном клиенте (предел, отмена, обрыв, повтор).
- [x] Шаг 6. Рабочий очереди и задание: проверки, цепочка скачивание, `ffmpeg`, `sherpa-onnx`, сохранение (`source = 'local'`), сигнал индексатору, статусы и причины; `tg message transcribe` через очередь с `--wait` и `--telegram`; `tg voice status|cancel|retry|backfill`; тесты на подменных звеньях.
- [x] Шаг 7. Конфиг `[voice_transcription]` (проверка, переменные, `config show`, `tg schema`), тип настроек демона.
- [x] Шаг 8. Источники: входящие события, `SaveHistoryPage` и `background_sync`, метки `tg chat voice on|off|list [--video] [--backfill]`, правила чатов (личные, с меткой, закрытые, свои); тесты.
- [x] Шаг 9. Статус: блок `voice` в `daemon status`, `dashboard`, строка `Voice` в `tg status`, эталоны.
- [x] Шаг 10. Документы: `docs/cli`, `docs/contracts.md`, README (раздел «Everything installed»: `ffmpeg`, `sherpa-onnx` и модель GigaAM, `tg voice install`), `FEATURES.md` (18.1 и новые пункты), скилл `docs/skill/tg`, журнал изменений; `just skill-check` с новым бинарником.

## Streams

| Поток | Шаги | Основные файлы |
| ----- | ---- | -------------- |
| queue | 2, 5 | `internal/store` (миграция, очередь), `internal/tgclient` (скачивание) |
| engine | 3, 4 | `internal/voice/gigaam`, `internal/voice/exec` |
| worker | 6, 7 | `internal/daemon` (рабочий, задание), `internal/config`, `internal/cli` (`tg voice`, `message transcribe`) |
| sources | 8, 9 | `internal/daemon` (события, история), `internal/cli` (`tg chat voice`), статус |
| docs | 10 | документы, скилл, журнал |

Волны: queue и engine параллельно, затем worker, затем sources, затем docs.

## Verification

| Check | Command | Expected result | Status |
| ----- | ------- | --------------- | ------ |
| Очередь | `go test -race ./internal/store -run VoiceQueue -count=1` | приоритеты, дубль, перезапуск, повторы, бюджет, возврат `running` | not_run |
| Установка | `go test ./internal/voice/... -run Install -count=1` | испорченный архив, выход за каталог и обрыв отвергнуты, права верны | not_run |
| Запуск программ | `go test ./internal/voice/... -run Exec -count=1` | раздельные потоки, тайм-аут, битый UTF-8, лимит вывода | not_run |
| Скачивание | `go test -race ./internal/tgclient -run DownloadToFile -count=1` | предел размера, отмена, очистка временных файлов | not_run |
| Рабочий | `go test -race ./internal/daemon -run VoiceWorker -count=1` | вся цепочка на подменах, `source = 'local'`, нулевой вызов `messages.transcribeAudio`, очистка | not_run |
| Источники | `go test -race ./internal/daemon ./internal/cli -run VoiceSource -count=1` | входящие, синхронизация, метки, закрытый чат и свои не ставятся | not_run |
| Инвариант | тест без `enabled` | ни одного задания и запроса к Telegram от источников | not_run |
| Всё | `just check` и `go test -race -count=1 ./...` в изоляции | зелёные | not_run |
| Живая проверка (по команде владельца) | `tg voice install`, `tg message transcribe` на одном своём голосовом в чате «Браин Тест» или своём личном | текст получен, `source = 'local'`, скорость и время записаны | not_run |

## Deploy / rollback

- [ ] Копия `~/.config/tg-cli/db/default`: миграция необратима для старых бинарников.
- [ ] `just install`, затем `tg voice install` (скачивание около 190 МБ рантайма и модели), проверка `tg voice status`.
- [ ] Включение владельцем: `[voice_transcription] enabled = true`, метки `tg chat voice on <чат>` для нужных чатов, перезапуск демона; первые сутки следить за `tg voice status` и `throttled_ms`.
- [ ] Откат: `enabled = false` и перезапуск, очередь остаётся в базе и не обрабатывается; движок можно удалить (`<home>/models/gigaam`); откат миграции только из копии базы.

## Cleanup

- [ ] Временные файлы в `var/agents/plans/20261120_2002_voice_transcription_local_queue/`, не в git.
- [ ] Каталог `tmp-voice/` в профиле пуст после остановки демона.

## Definition of done

- [ ] Код изменён только в объёме плана, инвариант выполняется, проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]` и временных файлов в `.agents/plans`.
- [ ] Решения записаны, документы и `FEATURES.md` соответствуют коду, README требует `ffmpeg` и `sherpa-onnx`.

## Known facts

- Telegram: `messages.transcribeAudio` доступна только пользователям; Premium без недельного предела; ошибки перечислены в разделе Context.
- Сейчас в `fileroot` предел 100 МиБ на файл, в `tgclient` пять рабочих на загрузку и не больше двух медиапередач на демон, поэтому видео до 500 МБ требует отдельной функции, а не правки константы для всех.
- Стоимость скачивания 500 МБ при части 512 КБ-1 МБ составляет порядка 500-1000 запросов `getFile` под дросселем 18 запросов в секунду, десятки секунд при отсутствии `FLOOD_WAIT` (оценка, не замер).
- Размеры: WAV 16 кГц моно PCM16 около 115 МБ в час, около 230 МБ на 2 часа.

## Open questions

| Question | Why it blocks | Options | Recommendation | Status |
| -------- | ------------- | ------- | -------------- | ------ |
| Q1. Где лежат модели | Определяет путь и обновление | A: `<home>/models/gigaam/`, один набор на все профили. B: в каталоге профиля. C: переиспользовать установку stenograf (`~/Library/Application Support/Stenograf/gigaam`) по настройке | A | decided: A (2026-11-20, принято владельцем по рекомендации) |
| Q2. Откуда `ffmpeg` | Нужен для WAV из Opus и MPEG-4 | A: требуем `ffmpeg` в `PATH` или по настройке, при отсутствии `engine_missing`. B: поставлять свой | A | decided: A (2026-11-20, принято владельцем по рекомендации) |
| Q3. Глубина синхронизации | Старые голосовые при догрузке истории дают сотни заданий | A: при синхронизации ставить только сообщения младше 90 дней, остальное `--backfill`. B: все. C: никакие, только входящие | A | decided: A (2026-11-20, принято владельцем по рекомендации) |
| Q4. Нерусская речь | GigaAM только русский | A: расшифровывать как есть и предупредить в документах. B: пропускать, если ffmpeg-проба языка не поддерживается (нет надёжного способа) | A | decided: A (2026-11-20, принято владельцем по рекомендации) |
| Q5. Свои исходящие голосовые | Свой голос тоже бывает нужен в поиске | A: не расшифровывать (как в плане). B: расшифровывать с флагом `include_own = true` | A | decided: A (2026-11-20, принято владельцем по рекомендации) |
