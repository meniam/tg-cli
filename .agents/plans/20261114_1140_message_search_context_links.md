# Пошаговый план: поиск в Telegram, контекст, комментарии, ссылки и отметка прочитанного

ID: 20261114_1140_message_search_context_links
Created: 2026-11-14
Owner: Мязин Евгений
Branch: feature/20261114_1140_message_search_context_links
PR: —

Исполнитель Go-кода (человек или любой агент) обязан загрузить скилл `expert-golang` перед первым изменением кода и держать его загруженным до конца плана.

## Status

- Current status: in_progress
- Last update: 2026-11-14
- Next safe step: завершение шага 5 (документы) и слияние ветки в `main`. Шаги 1-4 и 6 выполнены.

## Context and sources

- Read: `FEATURES.md` пункты 3.10 (ссылки), 4.3 (поиск), 4.4 (комментарии), 4.5 (контекст), 4.7 (`mark read`); `internal/chatref`, `internal/tgclient/history.go`, `internal/daemon` (`read.go`, `access.go`, `write.go`), `internal/cli` (`read_cmd.go`, `write_cmd.go`), `internal/store`; планы [списки чатов](20261114_0800_chat_access_lists_and_untrusted_text.md) (фильтр чтения) и [запись](20261114_1056_message_write_commands.md) (список `write`); gotd/td v0.166.0: `messages.search`, `messages.searchGlobal`, `messages.getReplies`, `messages.getHistory` со смещением, `messages.readHistory`, `channels.readHistory`, `channels.exportMessageLink`.
- Verified by commands: чтение истории и тем уже идёт через `query.Messages(api).GetReplies(...)` и `GetHistory` в `internal/tgclient/history.go`; локальный поиск по `messages_fts` есть (`internal/store/search.go`); класс записи и список `write` есть.
- Not verified: параметры фильтра типа медиа в `messages.search`, поведение `searchGlobal` и его лимиты на аккаунте, как достать обсуждение поста канала (`messages.getReplies` с `msg_id` поста или `getDiscussionMessage`), форма ссылок `t.me/c/...` в gotd, что возвращает `messages.readHistory` (Step 1).

## Goal

Агент ищет сообщения в Telegram (в чате и глобально), получает контекст вокруг сообщения, читает комментарии к посту канала, пользуется ссылками на сообщения как входом и выходом и помечает чат прочитанным; закрытые чаты остаются невидимыми, отметка прочитанным требует разрешения записи.

## Problem and contract

Тип плана: product (новые команды чтения и одна запись состояния).

- Ссылки на сообщения (3.10). Пакет `chatref` разбирает ссылки `https://t.me/<username>/<id>`, `https://t.me/<username>/<topic>/<id>`, `https://t.me/c/<channel>/<id>` и `https://t.me/c/<channel>/<topic>/<id>` (также `tg://privatepost`, если это дёшево) в пару «чат, сообщение» (идентификатор канала в форме Bot API `-100<channel>`). `message get <ссылка>` принимает такую ссылку вместо пары «чат, id». Команда `message link <чат> <id>` печатает ссылку: для публичного канала или группы `https://t.me/<username>/<id>`, для приватного канала и супергруппы `https://t.me/c/<channel>/<id>`; для личных чатов и обычных групп ссылки нет (код 2 с пояснением).
- Поиск (4.3). `message search <чат> <запрос> [--media photo|video|document|voice|audio|link|gif|pinned] [--limit N] [--cursor C]` ищет в Telegram (`messages.search`) внутри чата, возвращает сообщения, как история, с курсором для продолжения. `message search --public <запрос>` ищет глобально (`messages.searchGlobal`, хэштеги как текст запроса) и возвращает попадания с `chat_id`; попадания из закрытых чатов отбрасываются до ответа. Локальный `search` не меняется.
- Контекст (4.5). `message context <чат> <id> [--before N] [--after N] [--local]`: по умолчанию по 5 сообщений до и после (максимум по 50), включая само сообщение, в порядке от старых к новым; `--local` берёт их из локальной базы без обращения к Telegram и без догрузки (чего нет в базе, того нет в ответе).
- Комментарии (4.4). `message comments <чат> <id> [--limit N] [--cursor C]`: комментарии к посту канала с обсуждением (`messages.getReplies` по идентификатору поста); без обсуждения ошибка `not_found` с пояснением.
- Отметка прочитанным (4.7). `chat mark-read <чат>`: помечает чат прочитанным (`messages.readHistory` или `channels.readHistory`); класс безопасности «запись в аккаунт», разрешён только для чатов из списка `write` (как отправка): иначе код 9, закрытый чат `not_found`; поддерживает `--dry-run`.
- Права доступа. Всё чтение идёт через существующую проверку закрытых чатов (`not_found`, тот же ответ); результаты поиска и контекста не сохраняются в базу для закрытых чатов; прочитанное сохраняется в базу как обычная история.
- Поля чужого текста помечаются `untrusted`, как у истории.
- Дроссель: запросы идут через общий бакет; `searchGlobal` не повторяется автоматически дольше порога, как и остальное.
- Релиз одним слоем; откат: откат ветки.

## Scope

- В плане: `internal/chatref` (ссылки на сообщения), `rpc`, `tgclient`, Daemon, CLI (`message search`, `message context`, `message comments`, `message link`, `message get <ссылка>`, `chat mark-read`), схема, тесты, документы, живая проверка чтением.
- Вне плана: фильтры поиска по отправителю и дате, поиск по нескольким чатам сразу, поиск в локальной базе (уже есть), контекст по топику, закрепление, непрочитанное по топикам (4.6), чтение комментариев к комментариям.
- Затрагиваются: `internal/{chatref,rpc,tgclient,daemon,cli,schema}`, `AGENTS.md`, `FEATURES.md`, `README.md`, `docs/skill/`, `.agents/decisions/`.

## Main invariant

1. Закрытый чат невидим в новых методах так же, как в старых (`not_found`, тот же ответ), в том числе в глобальном поиске.
2. Без запроса поиска, контекста, комментариев и ссылок ничего в Telegram не пишется; единственная запись это `chat mark-read` и она требует `write`.
3. Существующие ответы `json` и `jsonl` не меняются.
4. `internal/cli` не импортирует `tgclient`, gotd, `store`.
5. Ни тест, ни проверка по умолчанию не обращаются к живому Telegram.
6. `just check` зелёный на каждом шаге.

## Risks and strategy

- Риск: `searchGlobal` резко упирается в FLOOD_WAIT. Стратегия: общий дроссель, лимит результатов по умолчанию 20, живая проверка одной-двумя пробами.
- Риск: форма ссылок `t.me/c/...` разнообразна (темы, `?thread=`, `?comment=`). Стратегия: разбирать строго известные формы, остальное код 2 с примером допустимого.
- Риск: глобальный поиск возвращает чаты вне аккаунта (публичные). Стратегия: у таких попаданий нет локального чата; они возвращаются как есть, если фильтр чтения выключен; при включённом `allow` чужие публичные чаты отбрасываются (их нет в `allow`).
- Риск: `mark-read` меняет видимое другим состояние (галочки прочтения у собеседника). Стратегия: требует `write`, `--dry-run`, предупреждение в документах.

## Decision log

Решения автора плана по рекомендациям, ожидают подтверждения владельцем.

| Date | Decision | Reason | Confirmed by |
| --- | --- | --- | --- |
| 2026-11-14 | Поиск в Telegram: `message search` в чате и `--public` глобально; локальный `search` не меняется | 4.3 | Ожидает подтверждения |
| 2026-11-14 | `chat mark-read` требует `write` | Отметка видна собеседнику, это запись в аккаунт | Ожидает подтверждения |
| 2026-11-14 | Ссылки: разбор строгих известных форм, `message link` только для каналов и супергрупп | 3.10, остальные чаты ссылок не имеют | Ожидает подтверждения |

## Step 0. Fix decisions before changes

- [x] План начат по общему указанию владельца продолжать без остановок (2026-11-14); решения ждут подтверждения при ревью ветки.
- [ ] Исполнитель Go-кода загрузил скилл `expert-golang` и подтвердил это в начале работы.
- [x] Ветка `feature/20261114_1140_message_search_context_links` создана, в заголовок записан `Branch`.

## Step 1. Audit and baseline

- [x] `just check` и `just build` на чистом дереве; результат в Known facts.
- [x] По исходникам gotd: `messages.search` (фильтры `InputMessagesFilter*`), `messages.searchGlobal`, комментарии к посту (`getReplies` и `getDiscussionMessage`), `getHistory` со смещением для контекста, `readHistory` и `channels.readHistory`, `channels.exportMessageLink`; что возвращают.
- [x] Записать итог в Known facts, непокрытое в Risks и Open questions.

## Implementation steps

- [x] 2. `internal/chatref`: разбор ссылок на сообщения и сборка ссылки (`MessageRef`, `ParseMessageLink`, `FormatMessageLink`); тесты на формы ссылок, темы, `t.me/c`, ошибки.
- [x] 3. `rpc`, `tgclient`, Daemon: методы `message.search`, `message.search_public`, `message.context`, `message.comments`, `message.link`, `chat.mark_read` (запись, `write`), проверка закрытых чатов, `untrusted`, сохранение прочитанного в базу, тест полноты пометок доступа; тесты на поддельных клиентах и `tg.Invoker`.
- [x] 4. CLI: команды и флаги, `message get <ссылка>`, схема (классы безопасности: чтение; `chat mark-read` запись в аккаунт), коды выхода, форматы; тесты.
- [ ] 5. Документы через `docwriter`: `AGENTS.md`, `FEATURES.md` (3.10, 4.3, 4.4, 4.5, 4.7), `README.md`, навык, решение в `.agents/decisions/`.
- [x] 6. Живая проверка (2026-11-14, реальный профиль, Claude; сообщения не читались, итог кодами и числами). Публичный канал с именем: `message link` дал ссылку `https://t.me/...` (код 0), `message get <ссылка>` вернул то же сообщение (тот же `id`), `message context --before 2 --after 2 <ссылка>` дал 3 сообщения (последнее сообщение канала: два до него и оно само), по возрастанию `id`, с самим сообщением; `--local` дал те же 3 сообщения без обращения к Telegram; `message search <чат> <запрос> --limit 5` дал 5 результатов, поиск с `--media photo` код 0; `message search --public --limit 5` дал 5 попаданий. `message comments` на канале без обсуждения вернул `not_found` (код 7), как задумано. `chat mark-read` без `write` дал код 9; с `write` для «Браин Тест»: `--dry-run` код 0 и `dry_run: true`, запись код 0; другой чат вне `write` код 9; `access.json` удалён. Не проверено на живом аккаунте: комментарии к посту с обсуждением, содержимое результатов глобального поиска и закрытые чаты в нём, окно контекста с удалёнными сообщениями, продолжение по курсору поиска.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Формат, vet, тесты | `just check` | зелёный | not_run |
| Пакеты плана с гонками | `go test -race -count=1 ./internal/chatref/... ./internal/rpc/... ./internal/tgclient/... ./internal/daemon/... ./internal/cli/...` | зелёный | not_run |
| Закрытый чат | тесты на каждый метод | `not_found`, равно неизвестному, нет в глобальном поиске | not_run |
| Ссылки | тесты `chatref` | формы разобраны, неверные отклонены | not_run |
| `mark-read` без `write` | тест | код 9, Telegram не вызывается | not_run |
| Живая проверка | Step 6 | числом: результатов, ошибок | not_run |

## Deploy / rollback

- [ ] Выкладки нет. Откат: откат ветки.

## Definition of done

- [ ] Код изменён только в рамках плана.
- [ ] Главный инвариант выполняется.
- [ ] Проверки зелёные или отклонения записаны.
- [ ] Нет шагов `- [0]`.
- [ ] Решение записано в `.agents/decisions/`; `AGENTS.md`, `FEATURES.md`, навык обновлены.

## Known facts

- История и темы читаются через `query.Messages(api)` в `internal/tgclient/history.go`.
- Список `write` и проверка записи уже есть (`internal/access`, `internal/daemon/write.go`).

## Open questions

| Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- |
| Лимит контекста | Размер ответа | до 50 с каждой стороны; до 100 | 50 | принято по рекомендации, ждёт подтверждения |
| `--public` по умолчанию лимит | FLOOD_WAIT | 20; 50 | 20 | принято по рекомендации, ждёт подтверждения |
