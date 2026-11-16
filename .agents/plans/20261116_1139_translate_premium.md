# Пошаговый план: перевод сообщений средствами Telegram и Premium-форматирование со структурным отказом

ID: 20261116_1139_translate_premium
Created: 2026-11-16
Owner: Мязин Евгений
Branch: feature/20261116_1139_translate_premium (создана от `main` `0336dff`, worktree `../tg-cli-translate_premium`)
PR: —

Исполнитель Go-кода (Codex Terra, `gpt-6.1-terra`) загружает скилл `expert-golang` перед первым изменением кода и держит его до конца плана.

## Status

- Current status: done
- Last update: 2026-11-16
- Next safe step: менеджер принимает рекомендованные варианты Q1 до Q8 из «Open questions» (владелец делегировал решения), записывает их в Decision log, ставит `approved`; затем шаг 1 (аудит, Codex Terra в worktree `../tg-cli-translate_premium`).

## Context and sources

- Read: `AGENTS.md`, `INTENT.md`, `FEATURES.md` (18.1 до 18.4, 5.4, 6.1.1.1, строки статуса 5.4, 6.1, 18.1, строка «Не начато, Could», список «не проверено на живом аккаунте»), `.agents/rules/plans.md`, решения `20261115_1800_remaining_could_features_are_split_into_take_wont_and_defer.md`, `20261116_0415_stories_and_business_are_not_done_and_semantic_search_goes_last.md`, `20261114_2040_text_formatting_through_parse_and_entities_flags_with_server_parsed_rich_messages.md`, `20261116_0017_config_toml_holds_paths_file_names_behavior_and_profile_settings.md`; планы `20261115_0439_media_export.md` (образец), `20261115_1747_small_features.md` (18.1, вопросы Q1, Q2, Q5 про Premium), `20261116_0225_debts.md` (пункт 4 и строки 48, 49 Decision log про предел длины).
- Код (разведка `pathfinder` по Atlas и `rg`, ссылки проверены чтением): `internal/tgclient/transcribe.go:39-57` (отказ Premium из `PREMIUM_ACCOUNT_REQUIRED` в `rpc.Error{Data: {"reason": "premium_required"}}`), `internal/daemon/transcribe.go` (образец обработчика: `listChat`, проверка видимости, интерфейс клиента), `internal/rpc/transcribe.go` (константа метода и типы в своём файле, тег `untrusted`), `internal/cli/transcribe_cmd.go`, `internal/cli/read_cmd.go:110` (регистрация в группе `message`), `internal/daemon/daemon.go:492-493`, `internal/output/output.go:62-71,98-108` (`ErrorBody.Reason`, перенос `reason` из `Data`), `internal/cli/client.go:91-131` (`translate`, `exitForRPC`: `CodeTelegram` даёт код 5), `internal/tgclient/errors.go:17-80` (`mapError` без Premium-ветки; `mapActionError`), `internal/tgclient/write.go:265-273` (`writeFormatError` заменяет любую ошибку Telegram rich-режима текстом `Telegram rejected rich message`), `internal/tgclient/write.go:313-345` (`writtenMessages` читает из ответа только id и дату), `internal/tgclient/write_format.go:332-336` (`custom_emoji` в `--entities`), `internal/daemon/write.go:16-44` и `internal/tgclient/write.go:24-34` (предел 4096 рун), `internal/daemon/send_media.go:43-44`, `internal/tgclient/send_media.go:19,64`, `internal/daemon/message_extras.go:143-144` (предел подписи 1024 руны в трёх местах), `internal/tgclient/client.go:177-187` (`Self` через `Auth().Status`, признак `Premium` у `tg.User` отбрасывается в `toUser`), `internal/tgclient/channels_profile_read.go:65` (`account profile` читает `premium` живым запросом), `internal/tgclient/channels_profile_write.go:142-143` и `internal/tgclient/saved_write.go:16-17` (свои переводы `PREMIUM_ACCOUNT_REQUIRED`), `internal/tgclient/readonly_test.go:13-23` (`writeVerbs`, `readFiles`), `internal/tgclient/throttle.go:17-47,80` (`classifyRequest`, по умолчанию `throttle.General`).
- gotd v0.166.0 (`GOMODCACHE`): `tg.MessagesTranslateTextRequest{Peer, ID []int, Text []TextWithEntities, ToLang, Tone}` (`tg/tl_messages_translate_text_gen.go:43-71`), ответ `MessagesTranslateResult{Result []TextWithEntities}`; комментарий метода: «Styled text entities will only be preserved for Telegram Premium users», `ToLang`: «Two-letter ISO 639-1 language code»; ошибки из комментария (Go-констант нет в `tl_messages_translate_text_gen.go`, но есть в `tg/tl_errors_gen.go:682-688`): `INPUT_TEXT_EMPTY`, `INPUT_TEXT_TOO_LONG`, `MSG_ID_INVALID`, `PEER_ID_INVALID`, `TO_LANG_INVALID`, `TRANSLATE_REQ_FAILED` (500), `TRANSLATE_REQ_QUOTA_EXCEEDED`, `TRANSLATIONS_DISABLED` (406), `TRANSLATION_TIMEOUT` (500). Есть `messages.translateRichMessage` (`Text []InputRichMessage`, ответ `messages.translatedRichMessage{result: Vector<RichMessage>}`) без списка ошибок. Признак `translations_disabled` у `userFull`, `chatFull`, `channelFull`. У `messages.sendMessage` и `messages.sendMedia` в списке ошибок `PREMIUM_ACCOUNT_REQUIRED` (403) и `PRIVACY_PREMIUM_REQUIRED`; у `sendMedia` `MEDIA_CAPTION_TOO_LONG`. `messageEntityCustomEmoji`: «must wrap exactly one regular emoji … otherwise the server will ignore it». HTML-разборщик gotd понимает `<tg-emoji emoji-id>` (`telegram/message/html/parser.go:49,168`), Markdown понимает `tg://emoji?id=` (`telegram/message/markdown/parser.go`).
- Документация Telegram (WebFetch 2026-11-16): `core.telegram.org/api/translation`: `translateText` переводит сообщения чата (`peer` и `id`) или произвольный текст (`text`); сущности возвращаются «only for Telegram Premium users»; Premium нужен для перевода всего чата в реальном времени, про отдельный вызов ограничения не сказано; клиентам советуют пачки «by specifying multiple message IDs in `id`»; предела числа id и длины нет. `core.telegram.org/api/premium`: предел подписи задают ключи appConfig `caption_length_limit_default` и `caption_length_limit_premium` (числа на странице не даны); Premium даёт custom emoji и эффекты сообщений. `core.telegram.org/api/custom-emoji`: emoji с флагом `free` доступны без Premium; что сервер делает с не бесплатным custom emoji от аккаунта без Premium, не написано.
- Сборка ветки и `tg schema` (бинарь собран из этого worktree в scratchpad): команд `message translate` и перевода в схеме нет; `message transcribe` класс `read`; `message send-media`, `message edit-media`, `message send`, `message edit` класс `account_write`.
- Миграции на `main`: последняя `0007_transcriptions.sql`. В worktree соседнего плана `20261116_0136_db_next_to_config` уже есть `0008_chats_state.sql`; тот же план правит `internal/daemon/daemon.go` и `internal/daemon/access_test.go` (по `git status` его ветки).
- Not verified: `just check` целиком не запускался (шаг 1). Не проверено: что `translateText` отвечает на сообщение без текста (медиа без подписи, служба, опрос) и на RichMessage; есть ли у сервера предел числа id за вызов; даёт ли `translateText` ошибку Premium аккаунту без Premium; что сервер делает с не бесплатным `custom_emoji` от аккаунта без Premium (ошибка, молча убирает сущность или пропускает); возвращает ли ответ `sendMessage` сущности сообщения (`UpdateShortSentMessage.entities`, `UpdateNewMessage`) так, чтобы по нему было видно удаление сущности; настоящие значения `caption_length_limit_*`; тайм-аут метода JSON-RPC по умолчанию (`internal/rpc/server.go:297`). Живых проверок план не предусматривает (владелец их отменил): всё перечисленное уходит в список «не проверено на живом аккаунте» `FEATURES.md`.

## Goal

Агент переводит одно или несколько сообщений видимого чата на нужный язык одним вызовом средствами Telegram, без записи в аккаунт, и получает понятный код и причину, когда перевод недоступен. Агент пишет с Premium-форматированием (custom emoji, длинная подпись Premium-аккаунта, RichMessage) и, когда Premium нет, получает структурный отказ `error.reason: premium_required` вместо голого кода Telegram, а при молчаливом удалении сущности сервером видит это в ответе.

## Problem and contract

Тип плана: product (новая команда, новые поля ответа, новая причина в конверте ошибки, изменённый предел подписи).

### Что уже есть (по коду и `tg schema` на 2026-11-16)

| Пункт | Состояние | Где |
| --- | --- | --- |
| 18.2 перевод | нет: ни `translateText`, ни команды, ни типов | `rg -i translat internal docs` пусто |
| 18.3 custom emoji при отправке | уже есть во всех трёх способах: `--entities` (`custom_emoji` с `custom_emoji_id`), `--parse html` (`<tg-emoji emoji-id="…">` разбирает gotd), `--parse markdown` (`![👍](tg://emoji?id=…)` разбирает gotd) | `internal/tgclient/write_format.go:332-336`, `write_format.go:89-109`, `docs/cli/messages-write.md:214` |
| 18.3 RichMessage | уже есть: `--parse rich-html|rich-markdown`, разбирает сервер | решение `20261114_2040`, `internal/tgclient/write_format.go:64-75` |
| 18.3 структурная причина `reason` в конверте | уже есть для транскрипции и правки профиля; поле `error.reason` в `output.ErrorBody` | `internal/tgclient/transcribe.go:42-46`, `internal/tgclient/channels_profile_write.go:142-143`, `internal/output/output.go:67` |
| 18.3 отказ Premium при записи сообщений | нет: `mapError` переводит `PREMIUM_ACCOUNT_REQUIRED` в код 5 с текстом-именем ошибки без `reason`; в rich-режиме `writeFormatError` заменяет любую ошибку текстом `Telegram rejected rich message` | `internal/tgclient/errors.go:38-43`, `internal/tgclient/write.go:265-273` |
| 18.3 длинная подпись Premium | нет: предел 1024 руны в трёх местах, Premium не проверяется (решение плана debts) | `internal/daemon/send_media.go:43`, `internal/tgclient/send_media.go:19,64`, `internal/daemon/message_extras.go:143` |
| Признак Premium своего аккаунта | только живым запросом `account profile` (`users.getFullUser`); `Self()` получает `tg.User` с `Premium`, но отбрасывает его | `internal/tgclient/channels_profile_read.go:65`, `internal/tgclient/client.go:177-187` |

Для custom emoji и RichMessage новый способ отправки не пишется: план добавляет только структурный отказ, признак удалённой сущности и подсказку `--dry-run`.

### Контракт для рекомендованных вариантов Q1 до Q8

Другой ответ меняет отмеченные места.

**Перевод (18.2).**

- Команда: `tg message translate <чат> <id>... --to ЯЗЫК` или `tg message translate <ссылка t.me на сообщение> --to ЯЗЫК`; ссылки разбирает `chatref`, как у `message get`. Класс безопасности `read`, метод JSON-RPC `message.translate`, класс доступа `chat-ref`: закрытый чат отвечает `not_found` до Telegram. Нового права в `access.json` нет: запрос ничего не пишет в аккаунт и не передаёт Telegram нового текста.
- Источник текста (Q1): только сообщения по `peer` и `id`; текст читает сам Telegram. Текст из локальной базы и произвольный текст в Telegram не отправляются.
- Язык (Q4): `--to` обязателен. Локальная проверка: две строчные латинские буквы ISO 639-1 (`en`, `ru`); заглавные приводятся к строчным; иначе код 2 до Telegram. `TO_LANG_INVALID` от сервера тоже даёт код 2 `target language is not supported by Telegram`.
- Пределы (Q3): от 1 до 20 id без повторов за вызов, иначе код 2 до Telegram. Длина текста локально не проверяется; `INPUT_TEXT_TOO_LONG` даёт код 2.
- Дроссель (Q2): общий класс `throttle.General`, как у `message transcribe`; одна пачка id это один запрос. FLOOD_WAIT обрабатывается общим правилом (автоожидание до порога, иначе код 6). Повторов при ошибках сервера нет.
- Хранение (Q5): перевод не записывается в базу; миграции нет.
- RichMessage (Q6): отдельного пути нет, все id уходят в `translateText`. Что сервер вернёт для RichMessage, записывается в «не проверено на живом аккаунте».
- Ответ `TranslateResult`: `chat_id`, `to_lang`, `items` (элементы `id`, `text` с пометкой `untrusted`). Сущности не отдаются, как при чтении сообщений (решение `20261114_2040`); у аккаунта без Premium их и нет. `--format jsonl` печатает по сообщению в строке (`ItemPath: items`). Порядок элементов совпадает с порядком id в запросе; если сервер вернул другое число результатов, Daemon отвечает кодом 5 `Telegram returned an unexpected translation count` и ничего не сопоставляет наугад.
- Ошибки: `MSG_ID_INVALID` код 2 `message id is invalid` (как `mapActionError`); `INPUT_TEXT_EMPTY` код 2 `message has no text to translate`; `TRANSLATIONS_DISABLED` код 5, `reason: translations_disabled`; `TRANSLATE_REQ_QUOTA_EXCEEDED`, `TRANSLATE_REQ_FAILED`, `TRANSLATION_TIMEOUT` код 5, `reason: translation_unavailable` (временная причина, агент повторяет позже сам); `PREMIUM_ACCOUNT_REQUIRED` код 5, `reason: premium_required` (общая ветка 18.3); остальное через `mapError`.
- Тайм-аут метода: значение по умолчанию сервера JSON-RPC, если шаг 1 покажет, что оно не меньше 30 секунд; иначе 60 секунд через `SetMethodTimeout`.
- Журнал Daemon пишет метод, длительность, число id и код; язык и тексты не пишет.

**Premium-форматирование и отказ (18.3).**

- Общая причина: `mapError` переводит `PREMIUM_ACCOUNT_REQUIRED` в код 5 с `reason: premium_required` и `PRIVACY_PREMIUM_REQUIRED` в код 5 с `reason: recipient_requires_premium` для всех методов. Текст `message` прежний (имя ошибки Telegram), меняется только добавленное поле. Свои переводы транскрипции (`transcribe.go`), правки профиля (`channels_profile_write.go`) и `saved_write.go` идут раньше `mapError` и не меняются; шаг 1 подтверждает это по коду.
- RichMessage: `writeFormatError` сохраняет `reason`, если он есть у ошибки: `Telegram rejected rich message` остаётся текстом, причина Premium не теряется.
- Custom emoji без Premium (Q7): Daemon не проверяет Premium заранее. После успешной `message send` и `message edit` клиент сравнивает число сущностей `custom_emoji` в запросе с числом в сообщении из ответа Telegram. Если ответ несёт сущности сообщения и их меньше, результат получает поле `premium_dropped: ["custom_emoji"]`, код 0, подсказка в stderr. Если ответ сущностей не несёт, поле не ставится; справочник говорит, что отсутствие поля не доказывает сохранение сущности. Ошибка сервера идёт через общую причину выше.
- Длинная подпись Premium (Q8): предел 1024 руны для всех остаётся проверкой до Telegram. Подпись от 1025 до 2048 рун не отклоняется сразу: Daemon один раз за жизнь соединения читает признак Premium своего аккаунта (`users.getUsers(inputUserSelf)` через существующий `Auth().Status`, признак `premium` сохраняется в памяти клиента до переподключения или до ответа `MEDIA_CAPTION_TOO_LONG`). Без Premium код 2 `caption must be at most 1024 characters without Telegram Premium`, `reason: premium_required`; с Premium подпись уходит в Telegram. Больше 2048 рун код 2 всегда. `MEDIA_CAPTION_TOO_LONG` от сервера даёт код 2 и сбрасывает признак в памяти. Касается `message send-media` и `message edit-media` (`message_extras.go:143`). Предел текста 4096 и решение debts для `message edit` не меняются.
- `--dry-run` у `send`, `edit`, `send-media`, `edit-media` добавляет `premium_features`: список того, что требует Premium в этом сообщении (`custom_emoji`, `long_caption`), локально, без запроса. Признак Premium аккаунта `--dry-run` не читает, кроме ветки длинной подписи по Q8.
- Перевод ошибок в причину живёт в одном новом файле `internal/tgclient/premium.go`; `errors.go` получает одну строку вызова.

**Релиз и откат.** Один слой; откат ветки. Схема базы и `access.json` не меняются. Поля `premium_dropped`, `premium_features`, `reason` необязательные: старые разборщики агента их пропускают.

## Файлы потока

Новые файлы, которыми владеет поток (другие потоки их не трогают):

- `internal/rpc/translate.go`, `internal/rpc/translate_test.go`: константа `MethodMessageTranslate = "message.translate"`, `TranslateParams{Chat, IDs, ToLang}`, `TranslateResult`, `TranslatedMessage{ID, Text untrusted}`, проверка параметров (1 до 20 id без повторов, язык).
- `internal/tgclient/translate.go`, `internal/tgclient/translate_test.go`: `Translate(ctx, ref, ids, toLang)`, разрешение пира, `MessagesTranslateText`, разбор ответа, перевод ошибок перевода.
- `internal/tgclient/premium.go`, `internal/tgclient/premium_test.go`: `premiumError(tgErr)` для `mapError`; подсчёт удалённых `custom_emoji` по ответу; признак Premium своего аккаунта в памяти с `SelfPremium(ctx)` и сбросом.
- `internal/daemon/translate.go`, `internal/daemon/translate_test.go`, `internal/daemon/translate_fake_test.go`: интерфейс `translateTelegram`, обработчик `message.translate`, заглушки `fakeTelegram`.
- `internal/daemon/premium_caption.go`, `internal/daemon/premium_caption_test.go`: интерфейс `premiumTelegram`, проверка подписи 1025 до 2048 рун по Q8.
- `internal/cli/translate_cmd.go`, `internal/cli/translate_cmd_test.go`: команда `message translate`, флаг `--to`, аннотации схемы.
- `.agents/decisions/20261116_HHMM_translation_reads_messages_by_id_and_premium_refusals_carry_a_reason.md` (имя уточняется в шаге 7).

Общие файлы, каждая правка одной локальной вставкой (новые строки рядом, без переформатирования соседних):

| Файл | Вставка |
| --- | --- |
| `internal/tgclient/errors.go` | в `mapError` внутри ветки `tgerr.As` одна строка `if e := premiumError(tgErr); e != nil { return e }` |
| `internal/tgclient/write.go` | `writeFormatError`: перенос `Data` (причины) в новую ошибку rich-режима; после send и edit вызов `premiumDropped(...)` и запись поля в результат |
| `internal/tgclient/send_media.go` | константа 1024 остаётся; проверка подписи принимает предел параметром (вызов из Daemon) |
| `internal/tgclient/write_format.go` | `premium_features` в `TextCheck` (подсчёт `custom_emoji` после разбора) |
| `internal/tgclient/readonly_test.go` | `"translate.go"` в конец `readFiles` |
| `internal/rpc/write.go` | поле `PremiumDropped []string \`json:"premium_dropped,omitempty"\`` в конце `SendResult`; `PremiumFeatures []string \`json:"premium_features,omitempty"\`` в конце структуры `--dry-run`; `PremiumFeatures` в `TextCheck` |
| `internal/daemon/send_media.go`, `internal/daemon/message_extras.go` | ветка 1025 до 2048 рун вызывает `d.captionAllowed(ctx, caption)` вместо немедленного кода 2 |
| `internal/daemon/telegram.go` | две строки `translateTelegram`, `premiumTelegram` в конце `Telegram` |
| `internal/daemon/daemon.go` | `d.handle(rpc.MethodMessageTranslate, d.messageTranslate)` (и тайм-аут, если нужен) в конце `register()` |
| `internal/daemon/access_test.go` | одна строка в карте `classes` (`chat-ref`, как у `message.transcribe`) |
| `internal/cli/read_cmd.go` | `cmd.AddCommand(a.messageTranslateCommand())` рядом с `messageTranscribeCommand` |
| `docs/cli/messages-read.md`, `docs/cli/messages-write.md`, `docs/cli/overview.md` | свой раздел или абзац в шаге 7 |
| `docs/contracts.md`, `FEATURES.md`, `docs/skill/tg-cli/SKILL.md`, `.agents/decisions/_TOC.md` | свой абзац или строка в шаге 7 |

`internal/daemon/daemon.go` и `internal/daemon/access_test.go` правит и соседний план `20261116_0136_db_next_to_config`: вставки отдельными строками в конце блоков, при конфликте сохраняются обе стороны.

Изменения общих реестров (полный список):

1. Методы JSON-RPC: `message.translate`.
2. Интерфейс `daemon.Telegram`: `translateTelegram` (`Translate`), `premiumTelegram` (`SelfPremium`).
3. Классы доступа `TestRegisteredMethodsHaveAccessClass`: `message.translate`.
4. Дроссель: без изменений (`translateText` попадает в `throttle.General` по умолчанию, `writeRequest` не меняется).
5. `readFiles`: `translate.go`. Глагол `Translate` не входит в `writeVerbs`, тест не ослабляется.
6. Команды CLI и схема: `message translate` (`read`, флаг `--to`); поля `premium_dropped`, `premium_features`; значения `error.reason`: `premium_required`, `recipient_requires_premium`, `translations_disabled`, `translation_unavailable`.
7. `access.json`: без изменений.
8. Миграции: нет (Q5 = A). При Q5 = B номер берётся следующим свободным после слияния `db_next_to_config` (у него `0008`).

## Scope

- В плане: 18.2 (перевод сообщений по id), 18.3 (структурный отказ Premium при записи, признак удалённого custom emoji, длинная подпись Premium, `premium_features` в `--dry-run`), схема, тесты на подменном клиенте, документы, справочник, статусы `FEATURES.md`.
- Вне плана: перевод произвольного текста и текста из базы (если Q1 = A), перевод в реальном времени и `messages.togglePeerTranslations` (это запись в аккаунт), `channels.toggleAutotranslation`, параметр `tone` (AI composer: другой продукт, свои лимиты Premium), `messages.translateRichMessage` (если Q6 = A), хранение переводов и их поиск (если Q5 = A), язык по умолчанию в `config.toml` (если Q4 = A), эффекты сообщений (`effect`), поиск id custom emoji по наборам, премиум-реакции, сообщения историй, проверка Premium до отправки custom emoji, признак Premium в `auth status`, живые проверки.
- Затрагиваются: файлы из раздела «Файлы потока».

## Main invariant

1. `message translate` не вызывает методов записи Telegram (`TestReadFilesCallNothingThatWrites` с `translate.go`) и не пишет в базу.
2. Закрытый списками доступа чат отвечает как неизвестный до запроса к Telegram.
3. Запросы существующих команд в Telegram не меняются байт в байт; единственный новый запрос при записи это `users.getUsers(inputUserSelf)` для подписи от 1025 до 2048 рун, один раз за соединение.
4. Коды выхода и тексты существующих ошибок не меняются; добавляются только необязательные поля `reason`, `premium_dropped`, `premium_features`. Отказы транскрипции и правки профиля прежние.
5. Подпись до 1024 рун и текст до 4096 рун проверяются как сейчас; подпись больше 2048 рун отклоняется до Telegram.
6. Схема базы и `access.json` не меняются; `internal/cli` не импортирует `tgclient`, gotd, `store`.
7. Ни тест, ни проверка не обращаются к живому Telegram; `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: сервер отвечает на сообщение без текста или на RichMessage не так, как в контракте (пустой текст вместо ошибки, ошибка на всю пачку). Стратегия: пустой `text` отдаётся как есть, ошибка пачки отдаётся кодом без частичного успеха; случаи записываются в «не проверено на живом аккаунте».
- Риск: массовый перевод занимает общий дроссель и тормозит другие чтения. Стратегия: 20 id за вызов, класс `General`; при жалобе отдельный вопрос о своём лимите (Q2 = B).
- Риск: `TRANSLATE_REQ_QUOTA_EXCEEDED` агент повторяет в цикле. Стратегия: причина `translation_unavailable` и текст в навыке «повторить позже, не в цикле».
- Риск: проверка удалённого custom emoji даёт ложное срабатывание, если сервер присылает сущности не всегда. Стратегия: поле ставится только при наличии сущностей в ответе и при уменьшении числа именно `custom_emoji`; тесты на оба вида ответа; поведение сервера в «не проверено на живом аккаунте».
- Риск: признак Premium в памяти устаревает (подписка кончилась). Стратегия: сброс при переподключении и при `MEDIA_CAPTION_TOO_LONG`; сервер остаётся последней проверкой.
- Риск: настоящие пределы подписи не 1024 и 2048 (`caption_length_limit_*` в appConfig). Стратегия: числа договорные, как 4096 в решении debts; справочник называет их договорными.
- Риск: общий `mapError` меняет поведение других команд. Стратегия: только добавление `Data` к той же ошибке; тест таблицей на прежние тексты и коды всех ветвей `mapError`.
- Риск: конфликт слияния с `db_next_to_config` в `daemon.go` и `access_test.go`. Стратегия: вставки отдельными строками, остальная логика в собственных файлах.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-16 | Custom emoji и RichMessage при отправке заново не делаются: они уже есть в `--entities`, `--parse html|markdown` и rich-режимах; 18.3 добавляет только отказ, признак удаления и подсказку | Проверено по коду и gotd (`write_format.go:332`, `html/parser.go:49`) | no |
| 2026-11-16 | Отказ Premium это код 5 с `error.reason`, как у транскрипции (Q1 плана `small_features`), без нового кода выхода | Тот же конверт уже разбирает агент; новый код расширил бы таблицу ради одного случая | no |
| 2026-11-16 | `PRIVACY_PREMIUM_REQUIRED` получает свою причину `recipient_requires_premium` | Отказ из-за настройки собеседника отличается от отказа своему аккаунту: Premium нужен своему аккаунту, но причина в чужой приватности | no |
| 2026-11-16 | Перевод отдаёт только простой текст без сущностей | Чтение сообщений не отдаёт сущности (решение `20261114_2040`); без Premium Telegram их и не вернёт | no |
| 2026-11-16 | Нового права в `access.json` для перевода нет, класс `read`, `chat-ref` | Запрос не пишет в аккаунт и не отправляет нового текста; закрытый чат скрыт | no |
| 2026-11-16 | `tone`, перевод в реальном времени и `togglePeerTranslations` вне плана | `tone` относится к AI composer с отдельными лимитами; переключатели это запись в аккаунт | no |
| 2026-11-16 | Язык проверяется локально как две латинские буквы ISO 639-1 | Так описан `to_lang` в gotd; неверный код ловится до запроса | no |
| 2026-11-16 | Миграция не нужна при Q5 = A | Перевод не хранится | no |
| 2026-11-16 | Q1 до Q8: принят вариант A (перевод только по `peer` и `id`, общий дроссель, 1-20 id, `--to` обязателен, переводы не хранятся, RichMessage идёт в `translateText`, custom emoji без Premium проверяется по ответу с `premium_dropped`, подпись 1024 для всех) | Владелец делегировал решения менеджеру («Решения сам прими я не разбираюсь», 2026-11-14); принят рекомендуемый вариант автора плана | yes |

## Step 0. Fix decisions before changes

- [x] Ответы на Q1 до Q8 записаны в Decision log, контракт поправлен под ответы.
- [x] Если Q1 или Q8 приняты не по рекомендации, план возвращён планировщику: меняются инварианты 1 и 3.
- [x] Исполнитель Go-кода (Codex Terra) прочитал `expert-golang` и `.agents/rules/testing.md` и подтвердил это в отчёте аудита.
- [x] Ветка `feature/20261116_1139_translate_premium` создана от `main` (`0336dff`) в worktree `../tg-cli-translate_premium`.

## Step 1. Audit and baseline (исполнитель Codex Terra)

Отчёт в `var/agents/plans/20261116_1139_translate_premium/audit.md` (в git не входит), итог в «Known facts».

- [x] `just check` и `just build` на чистом дереве ветки; результат в Known facts.
- [x] Подтвердить таблицу «Что уже есть»: нет перевода; `custom_emoji` в трёх способах; нет `reason` у ошибок записи; три места предела подписи; `Self()` получает `tg.User.Premium`.
- [x] `translateText` по исходникам gotd и документации Telegram: разбор `MessagesTranslateResult`, совпадение порядка результатов с `id`, поведение на сообщении без текста и на RichMessage (если документация молчит, записать как неизвестное), есть ли предел числа id.
- [x] Все места, где уже переводится `PREMIUM_ACCOUNT_REQUIRED` (`transcribe.go`, `channels_profile_write.go`, `saved_write.go` и другие): идут ли они до `mapError`; не изменит ли общая ветка их ответ.
- [x] Ответ `messages.sendMessage` и `messages.editMessage` в gotd: где лежат сущности итогового сообщения (`UpdateShortSentMessage.Entities`, `UpdateNewMessage`, `UpdateEditMessage`), как их достать в `writtenMessages` без нового запроса.
- [x] Признак Premium: стоимость `Auth().Status` (один `users.getUsers`), где хранить признак в памяти клиента и как его сбросить при переподключении (`client_restart`).
- [x] Тайм-аут метода по умолчанию (`internal/rpc/server.go:297`); нужен ли `SetMethodTimeout` для `message.translate`.
- [x] Где регистрируются `--dry-run` поля `send`, `edit`, `send-media`, `edit-media`; где поставить `premium_features`.
- [x] Записать итоги в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

Каждый шаг кода выполняет Codex Terra, проверяет Claude (тесты, дифф, `just check`), коммитит Claude.

- [x] 2. Перевод, слой `rpc` и `tgclient`: `rpc/translate.go`; `tgclient/translate.go`; `translate.go` в `readFiles`. Тесты на подмене invoker: запрос несёт `peer`, `id` в исходном порядке, `to_lang`, без `text` и `tone`; ответ из двух результатов ложится на два id; другое число результатов даёт код 5; `TO_LANG_INVALID`, `MSG_ID_INVALID`, `INPUT_TEXT_EMPTY`, `INPUT_TEXT_TOO_LONG` дают код 2; `TRANSLATIONS_DISABLED` и три временные ошибки дают код 5 с причиной; FLOOD_WAIT без повтора; проверка параметров: 0 и 21 id, повтор id, язык `EN` приводится к `en`, `english` и `e1` код 2.
- [x] 3. Перевод, Daemon и CLI: `translateTelegram` и строка в `Telegram`; `daemon/translate.go` (`listChat`, видимость, вызов клиента); регистрация в `daemon.go`; строка в `access_test.go`; заглушки в `translate_fake_test.go`; `cli/translate_cmd.go` (аргументы `<чат> <id>...` или ссылка, `--to` обязателен, `safetyRead`, `ItemPath: items`, `untrusted` у `text`); строка в `read_cmd.go`; заготовка раздела в `docs/cli/messages-read.md`, чтобы тест полноты был зелёным. Тесты без сокетов: закрытый чат `not_found` и ноль вызовов Telegram; без `--to` код 2; ссылка t.me разбирается в чат и id; форматы `json` и `jsonl`; запись в базу не происходит.
- [x] 4. Причина Premium: `tgclient/premium.go` (`premiumError`), строка в `mapError`, перенос причины в `writeFormatError`. Тесты: `PREMIUM_ACCOUNT_REQUIRED` на `sendMessage`, `sendMedia`, `editMessage` и rich-режиме даёт код 5, прежний текст и `reason: premium_required`; `PRIVACY_PREMIUM_REQUIRED` даёт `recipient_requires_premium`; таблица прежних ветвей `mapError` без изменений; отказы транскрипции и правки профиля прежние; CLI печатает `error.reason`.
- [x] 5. Custom emoji и `--dry-run`: подсчёт `custom_emoji` в запросе и в ответе, поле `premium_dropped`; `premium_features` в `TextCheck` и в ответе `--dry-run`. Тесты: ответ с сущностями и без одной `custom_emoji` даёт `premium_dropped`; ответ без сущностей поля не даёт; сообщение без custom emoji поля не даёт; запрос в Telegram прежний байт в байт; `--dry-run` с `<tg-emoji>`, Markdown `tg://emoji` и `--entities custom_emoji` перечисляет `custom_emoji`, без них поле пустое; `--dry-run` не вызывает Telegram.
- [x] 6. Длинная подпись Premium по Q8: `SelfPremium` в `premium.go` с памятью и сбросом; `daemon/premium_caption.go`; вставки в `send_media.go` и `message_extras.go` Daemon и в `tgclient/send_media.go`. Тесты: 1024 руны уходят без чтения Premium; 1025 без Premium код 2 с `reason: premium_required` и ноль вызовов отправки; 1025 с Premium уходит; 2049 код 2 без запросов; признак читается один раз на два вызова; `MEDIA_CAPTION_TOO_LONG` даёт код 2 и сбрасывает признак; переподключение сбрасывает признак; `--dry-run` с 1500 рунами показывает `long_caption` в `premium_features`.
- [x] 7. Документы через `docwriter` и `glavred` по `docs_chain.md`: `docs/cli/messages-read.md` (`message translate`, ошибки и причины, отсутствие хранения), `docs/cli/messages-write.md` (причины Premium, `premium_dropped` и его ограничение, предел подписи 1024 и 2048, `premium_features`), `docs/cli/overview.md` (список значений `error.reason`), `docs/contracts.md` (абзацы «Translation» и «Premium refusals»), таблицы пересобрать `go test ./internal/cli -run TestDocsGenerated -update-docs`; `FEATURES.md`: подпункты 18.2.1 (`message translate` по id, `--to`, до 20 id), 18.2.2 (причины отказа, без хранения), 18.3.1 (отказ `premium_required` и `recipient_requires_premium` при записи), 18.3.2 (`premium_dropped` и `premium_features`), 18.3.3 (подпись до 2048 рун для Premium), поправить 6.1.1.1 под Q8, строки статуса `[0] 18.2` и `[0] 18.3` (сделано, на живом аккаунте не проверено), убрать 18.2 и 18.3 из строки «Не начато, Could», дописать в список «не проверено на живом аккаунте» перечень ниже; `README.md` не меняется (документирует только проверенное вживую); `docs/skill/tg-cli/SKILL.md` (когда переводить через `tg`, а когда самому; `--to`; причины и что делать с `translation_unavailable`; `premium_dropped`; узнать Premium через `account profile`); решение в `.agents/decisions/` и строка в `_TOC.md`.
- [x] 8. Итоговая проверка Claude: `just check`, гонки по пакетам плана, `git diff --stat main -- internal/store/migrations` пуст, дифф общих файлов только вставками из таблицы «Файлы потока».

Перечень для «не проверено на живом аккаунте» (шаг 7): `message translate` на тексте, на медиа без подписи, на службе, на RichMessage и на пачке из 20 id; предел числа id сервера; ошибки `TRANSLATIONS_DISABLED` и `TRANSLATE_REQ_QUOTA_EXCEEDED`; нужна ли Premium для перевода; FLOOD_WAIT на переводе; отправка не бесплатного custom emoji без Premium (ошибка, молчаливое удаление или пропуск) и срабатывание `premium_dropped`; RichMessage без Premium; подпись от 1025 до 2048 рун с Premium и без; настоящие `caption_length_limit_default` и `caption_length_limit_premium`; `PRIVACY_PREMIUM_REQUIRED` у собеседника с ограничением.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/... ./internal/output/...` | зелёный | not_run |
| Перевод только читает | `go test ./internal/tgclient -run TestReadFilesCallNothingThatWrites` | `translate.go` в списке, тест зелёный | not_run |
| Закрытый чат скрыт | тесты `internal/daemon` (`translate_test.go`) | `not_found`, ноль вызовов Telegram | not_run |
| Прежние ошибки не изменились | тест таблицы `mapError` в `internal/tgclient` | тексты и коды прежние, `reason` только у двух Premium-ошибок | not_run |
| Запросы записи прежние | тесты `internal/tgclient` (`write_test.go`, `premium_test.go`) | запросы send, edit, send-media совпадают с прежними | not_run |
| Предел подписи | тесты `internal/daemon` (`premium_caption_test.go`) | 1024 без запроса Premium, 1025 по признаку, 2049 код 2 | not_run |
| Полнота схемы, справочника и классов | `go test ./internal/cli`, `go test ./internal/daemon -run TestRegisteredMethodsHaveAccessClass` | разделы для `message translate` и `--to`; класс у `message.translate` | not_run |
| Таблицы справочника | `go test ./internal/cli -run TestDocsGenerated -update-docs`, затем повторный запуск без флага | таблицы пересобраны, повтор зелёный | not_run |
| Миграций нет | `git diff --stat main -- internal/store/migrations` | пусто | not_run |

## Deploy / rollback

- [x] Выкладки нет. После слияния перезапустить Daemon (`tg daemon stop`, `tg daemon start`).
- [x] Откат: откат ветки. Схема базы и `access.json` не меняются, данных, мешающих откату, нет.

## Cleanup

- [x] Временные файлы лежат в `var/agents/plans/20261116_1139_translate_premium/`.
- [x] Временные файлы не в git (`/var/` в `.gitignore`).

## Definition of done

- [x] Код изменён только в рамках плана и раздела «Файлы потока».
- [x] Главный инвариант выполняется.
- [x] Проверки зелёные или отклонения записаны.
- [x] Нет шагов `- [0]`.
- [x] Нет временных файлов в `.agents/plans`.
- [x] Решение записано в `.agents/decisions/` и `_TOC.md`; `FEATURES.md`, справочник, `docs/contracts.md` и навык обновлены; непроверенное вживую перечислено в `FEATURES.md`.

## Known facts

- На 2026-11-16 `main` = `0336dff`; миграции на `main` до `0007_transcriptions.sql`; `tg schema` из ветки без `message translate`.
- Единицы длины во всех проверках текста и подписи сейчас руны (`utf8.RuneCountInString`); документация Telegram называет предел подписи «UTF-8 length». План единицы не меняет.
- `mapError` (`internal/tgclient/errors.go:38-43`) переводит любую ошибку Telegram в `rpc.NewError(CodeTelegram, tgErr.Type)`, исключение `CHAT_FORWARDS_RESTRICTED`.
- `classifyRequest` отправляет неизвестные запросы в `throttle.General` (`internal/tgclient/throttle.go:43-44`), так же идёт транскрипция.

## Open questions

| # | Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- | --- |
| Q1 | 18.2. Что переводить: сообщения из Telegram, текст из базы или произвольный текст | Определяет запрос, тест только-чтения и то, что уходит в Telegram | A: только сообщения по `peer` и `id`, текст читает Telegram; B: текст из локальной базы через поле `text`; C: A плюс `--text` для произвольного текста | A. Цена: удалённое в Telegram сообщение не перевести, даже если оно есть в базе; каждый вызов идёт в Telegram. B расходится с правками и теряет сущности, C отправляет в Telegram новый текст, который агент переведёт сам без утилиты (мерка решения `20261115_1800`) | решён: A |
| Q2 | 18.2. Как дросселировать перевод | Массовый перевод может занять общий дроссель | A: общий класс `General`, как у транскрипции, без своего лимита; B: свой лимит, например один вызов в 3 секунды, сверх общего дросселя | A. Цена: перевод большого чата замедлит другие чтения агента в это время; около 0 часов. B добавляет правку `throttle.go` и настройку, около 2 часов, без данных о реальном пределе сервера | решён: A |
| Q3 | 18.2. Пределы вызова | Определяет проверку параметров и число запросов | A: от 1 до 20 id, длина текста не проверяется локально; B: от 1 до 100 id; C: один id за вызов | A. Цена: чат из 1000 сообщений это 50 вызовов. Предел сервера неизвестен, 20 осторожнее; B рискует ошибкой всей пачки, C даёт в 20 раз больше запросов | решён: A |
| Q4 | 18.2. Язык по умолчанию | Определяет флаг и связь с `config.toml` | A: `--to` обязателен, умолчания нет; B: ключ `[behavior] translate_to` в `config.toml`, `--to` перекрывает; C: язык аккаунта или клиента (`lang_code`) | A. Цена: агент передаёт язык в каждом вызове. B правит `internal/config` и шаблон, которые ведёт соседний поток, около 2 часов; C даёт скрытое состояние, по которому агент не поймёт, на какой язык перевод | решён: A |
| Q5 | 18.2. Хранить ли переводы | Определяет миграцию и работу офлайн | A: не хранить, миграции нет; B: таблица `translations` (`chat_id`, `message_id`, `to_lang`, `text`, `edit_date`), сброс при правке сообщения, следующий свободный номер миграции | A. Цена: повтор того же перевода снова идёт в Telegram. B около 4 часов, номер миграции согласуется с `db_next_to_config` (`0008` занят), нужен сброс по правкам | решён: A |
| Q6 | 18.2. Как переводить RichMessage | `translateText` для RichMessage не описан; есть отдельный `messages.translateRichMessage` | A: отдельного пути нет, все id идут в `translateText`, ответ отдаётся как есть; B: Daemon сначала читает сообщения, RichMessage уходят в `translateRichMessage`, текст блоков через `rich_message_text.go`; C: RichMessage отклоняются кодом 2 | A. Цена: перевод RichMessage может прийти пустым, это записывается в непроверенное. B добавляет запрос на чтение и второй путь без документации ошибок, около 3 часов; C отказывает там, где сервер, возможно, справится | решён: A |
| Q7 | 18.3. Custom emoji без Premium: проверять ли заранее | Сервер, возможно, молча убирает сущность, и тогда кода ошибки нет | A: не проверять; после отправки сравнить сущности ответа и поставить `premium_dropped`; B: отказывать до запроса, если у аккаунта нет Premium (кроме «Избранного»); C: ничего не делать, только справочник | A. Цена: признак ставится только когда ответ несёт сущности, около 2 часов. B ошибается на бесплатных emoji (флаг `free` виден только отдельным запросом документов) и добавляет запрос; C оставляет агента без сигнала | решён: A |
| Q8 | 18.3 и решение debts. Подпись длиннее 1024 рун у Premium-аккаунта | План debts зафиксировал 1024 без проверки Premium; Premium-аккаунт теряет свою подпись до 2048 | A: оставить 1024 для всех, отказ кодом 2 с `reason: premium_required`; B: для 1025 до 2048 рун один раз за соединение читать признак Premium своего аккаунта, без Premium код 2 с причиной, с Premium отправка; C: снять локальный предел выше 1024 и отдавать `MEDIA_CAPTION_TOO_LONG` от Telegram | B. Цена: один `users.getUsers` за соединение и только при длинной подписи; меняется строка 48 Decision log плана debts (нужна запись решения); около 3 часов. A не даёт Premium-аккаунту его возможность, C ломает обещание debts «отказ до Telegram» для аккаунта без Premium | решён: A |
