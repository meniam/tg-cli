# Step-by-step plan: отчёт о полноте базы в `tg chat sync status`

ID: 20261123_0355_sync_completeness_report
Created: 2026-11-23
Owner: Мязин Евгений
Branch: feature/20261123_0355_sync_completeness_report (создаётся позже от ветки, в которую влита догонка; см. «Предусловие» и шаг 0)
PR: —

Тип плана: Product / refactoring (новое поле ответа и новый флаг команды, меняется `tg schema`). Схема базы не меняется.

## Status

- Current status: draft
- Last update: 2026-11-23
- Next safe step: владелец отвечает на Q1-Q6; код не начинается, пока не выполнено «Предусловие».

## Запрос владельца

«Надо теперь добавить что бы там был отчет о полноте базы». «Там» значит `tg chat sync status`. Повод: владелец спросил, сколько людей, чатов, каналов и ботов загружены полностью, и `status` ответить не смог.

## Предусловие (точка перебазирования)

Код этого плана пишется только поверх работы догонки: плана `20261122_2245_chat_catchup_after_downtime` (worktree `../tg-cli-catchup`, ветка `feature/20261122_2245_chat_catchup_after_downtime`, агент `dev-catchup`, статус `in_progress`). Решение владельца от 2026-11-23: ждать догонку и ответвляться от неё.

Условие начала кода, проверяется в шаге 0:

- ветка догонки влита (в `feat/history-limits` или в `main`, куда её вольёт владелец), и ветка этого плана создана от результата слияния;
- в базе ветки есть миграция `internal/store/migrations/0014_sync_gap.sql` с колонками `sync_state.gap_from`, `sync_state.gap_to`;
- поля `history_gap_from`, `history_gap_to` есть в `chat overview` (шаг 9 плана догонки);
- `rpc.HistorySyncJob` получил `gap_from`, `gap_to` (там же).

Пока условие не выполнено, план остаётся `draft` или `approved`, но не `in_progress`. Если владелец захочет начать раньше, это новое решение: тогда шаги 2-4 пишутся без `with_gap` и правятся после слияния.

## Context and sources

- Прочитано: `AGENTS.md`, `.agents/rules/plans.md`, `.agents/rules/decisions.md`, `.agents/rules/project/AGENTS.md`, скилл `scout`; бриф `var/agents/plans/20261123_0355_sync_completeness_report/planner_brief.md`.
- Код (ветка `feat/history-limits`, `bd457af`): `internal/daemon/history_sync.go`, `internal/daemon/history_sync_depth.go`, `internal/daemon/chat_limit.go`, `internal/daemon/overview.go`, `internal/daemon/access.go`, `internal/daemon/persist.go`, `internal/store/overview.go`, `internal/store/sync_state.go`, `internal/store/chat_limit.go`, `internal/store/dashboard.go`, `internal/store/background_sync.go`, `internal/store/chat_filter.go`, `internal/rpc/api.go`, `internal/rpc/overview.go`, `internal/cli/chat_sync_cmd.go`, `internal/cli/read_cmd.go`, `internal/output/fields.go`, миграции `0002`, `0008`, `0010`.
- Документы: `docs/cli/chats.md` (разделы «Loading a whole chat», `tg chat sync status`), `docs/contracts.md:12-18`, `docs/README.md:49`, `docs/skill/tg/references/history.md`, `FEATURES.md:461-465`.
- Решения: `20261114_1353_deep_history_sync_is_a_background_job_behind_the_shared_throttle`, `20261122_1410_full_history_load_stops_at_a_date_or_a_message_count`.
- План догонки (только чтение): `../tg-cli-catchup/.agents/plans/20261122_2245_chat_catchup_after_downtime.md`, миграция `../tg-cli-catchup/internal/store/migrations/0014_sync_gap.sql` (коммит `d44cc85`).
- Поиск: `bin/atlas query` по `chats_state`, `history_complete` и по решениям; `rg` по `MarkHistoryLimitReached`, `DELETE FROM messages`, `saveChats(`, `HistorySyncJobs`.
- Не проверено: живая база и живой Daemon (правило владельца: без живых проверок). Числа из пункта «Замер владельца» ниже взяты из брифа, не из моего запуска.

## Known facts

Снимок на 2026-11-23, ветка `feat/history-limits` (`bd457af`).

1. `chat sync status` без чата отвечает `rpc.HistorySyncJobs{Jobs: d.sync.snapshots()}`: только задачи памяти этого запуска Daemon (`internal/daemon/history_sync.go:643-659`). С чатом при отсутствии задачи отдаёт `idle` из `sync_state` (`idleHistorySyncJob`, `:679-693`). Сводки по всем чатам нет.
2. `snapshots()` пропускает чаты, закрытые списками доступа (`history_sync.go:345-360`, проверка `policy.Allowed`).
3. Решение `20261114_1353` держит задачи в памяти намеренно; строка 48 говорит, что `status` без чата показывает только задачи этого запуска. Сводку оно не запрещает: это пробел, не решение.
4. `chat overview` отдаёт `history_complete` по чату, но не больше 500 строк за вызов и без курсора (`internal/store/overview.go:48-51`, `internal/daemon/daemon.go:35-36`): точных итогов по типам из него не получить.
5. Замер владельца 2026-11-23 (`chat overview --limit 500 --type <t>`): user 500+ (в выборке 398 полных; `chat sync queue --dry-run` увидел 128 незагруженных и 448 загруженных, всего 576), bot 133 (3), group 304 (1), supergroup 111 (2), channel 187 (9). Порядок масштаба: около 1300 чатов.
6. Типы чатов: `user`, `bot`, `group`, `supergroup`, `channel`; их держит `CHECK` таблицы `chats` (`internal/store/migrations/0002_schema.sql:3`) и карта `chatTypes` (`internal/daemon/read.go:23`).
7. `sync_state`: `chat_id`, `oldest_id`, `newest_id`, `complete`, `synced_at` (`0002_schema.sql:94-100`), `first_message_date` (`0010_first_message_date.sql:4`). Догонка добавляет `gap_from`, `gap_to` (`0014_sync_gap.sql` в worktree догонки). Представление `chats_state` питает `chat overview` и в `0014` получает `history_gap_from`, `history_gap_to`.
8. Чат, дошедший до предела загрузки, помечен в базе как `complete = 1`, а не `depth_reached`. `history_limit.since`, `history_limit.messages` (`history_sync.go:537-538`) и лимит чата `chat limit` (`chat_limit.go:76`) зовут `store.MarkHistoryLimitReached` (`internal/store/sync_state.go:167-190`): `complete = 1`, `first_message_date` не трогается (пустой у чата, который раньше не был полным). Решение `20261122_1410` прямо отвергло отдельную колонку «остановлен по пределу» и велело считать такой чат полностью загруженным.
9. `depth_reached` есть только у задачи в памяти с `--messages` или `--since` (`internal/rpc/api.go:538-542`, `history_sync.go:176, 405-416`); `complete` у такого чата остаётся `0`.
10. `first_message_date` при `complete` равен самой ранней дате среди строк сообщений чата, удалённые включены (`sync_state.go:63-67`). Физически строки сообщений удаляет только подчистка лимита чата (`internal/store/chat_limit.go:188`); остальное удаление ставит `deleted_at`.
11. Фильтр доступа для SQL: `chatFilter(d.policy())` (`internal/daemon/access.go:100-103`) даёт `store.ChatFilter`, а `ChatFilter.condition` встраивает условие в запрос (`internal/store/chat_filter.go:13-50`); при `Hide` условие `1=0`. Так фильтруют `ChatsOverview` (`overview.go:67`) и `DashboardStats` (`internal/store/dashboard.go:36`).
12. Прецедент сводки: `store.DashboardStats` уже считает по группам планировщика `COUNT(*)` и `SUM(s.complete = 1)` над `chats LEFT JOIN sync_state` с фильтром доступа (`dashboard.go:33-58`). Он считает предел и (после догонки) дыру как полную загрузку.
13. Строки `chats` пишут `saveChats` и `saveChatInfo` (`internal/daemon/persist.go:40-117`); их зовут `chat list`, `chat resolve`, `chat unread`, `chat common`, `chat create` и действия администратора (`daemon.go:882`, `read.go:72`, `unread.go:67`, `chat_lists.go:190`, `chat_admin.go:280, 447`). Значит, в `chats` есть и чаты, найденные `resolve` или `common`, которых нет в списке диалогов. Признака «вышел из чата» в `chats` нет; `archived` есть только в `dialog_state.data` и только после `chat state`.
14. CLI: `chat sync status` объявлен с `Result: rpc.HistorySyncJobs`, `ChatResult: rpc.HistorySyncJob`, `ItemPath: "jobs"` (`internal/cli/chat_sync_cmd.go:25-31`). В `jsonl` печатаются только элементы `jobs` (`emitListAtPath`, `internal/cli/read_cmd.go:21-31`): объект рядом со списком в `jsonl` теряется. `--format jsonl` с чатом уже даёт код 2 (`chat_sync_cmd.go:143-145`). `--fields` с `ItemPath` сначала ищет путь в корне результата (`internal/output/fields.go:180-196`), поэтому `--fields summary` выберет объект сводки.
15. Тесты схемы: `internal/cli/schema_fields_test.go` (`TestSchemaFieldsMatchRealResponses`, `TestEveryCommandResponseIsCompared`, `TestSchemaListItemsMatchJSONL`) и `internal/cli/docs_test.go` (раздел в `docs/cli/` для каждой команды и флага). Таблицы флагов в `docs/cli/` собирает `just docs` (`docs/README.md:49`).
16. Индексы для подзапросов по чату: `messages_chat_date_idx (chat_id, date DESC)` (`0002_schema.sql:60`), частичный `messages_chat_out_idx (chat_id, out) WHERE deleted_at IS NULL` (`0008_chats_state.sql:9`).
17. Последний пункт раздела 10 `FEATURES.md` сейчас 10.20 (`FEATURES.md:461`); догонка занимает 10.21 (шаг 11 её плана). Этот план берёт следующий свободный номер после слияния, ожидаемо 10.22.
18. Пример сводки по форме из раздела «Контракт» занимает 629 байт, порядка 200 токенов; одна задача в списке `jobs` занимает 232 байта. Сводка стоит примерно как три задачи списка.

## Контракты плана догонки, на которые опирается этот план

Не дублируются и не меняются; ссылки по ID строк Decision log того плана.

- D2: одна дыра на чат, колонки `gap_from`, `gap_to` в `sync_state`; дыра видна в `chat sync status` и `chat overview`. Отсюда: чат с открытой дырой не считается полностью загруженным, даже при `complete = 1`.
- D-A3: отметка дыры чату без строки `sync_state` создаёт строку с пустыми `oldest_id`, `newest_id` и `complete = 0`. Отсюда: дыра проверяется раньше `complete` и раньше признака «нет сообщений».
- D-A4: дыра закрывается или сужается только в транзакции `SaveHistoryPage`; `complete` и `first_message_date` при закрытии не сбрасываются. Отсюда: после закрытия дыры чат возвращается в свою корзину без отдельного кода в отчёте.

## Goal

`tg chat sync status --summary` (форма по Q1) отвечает без Telegram и без входа: сколько видимых чатов каждого типа и всего загружены до первого сообщения, остановлены пределом, имеют дыру, загружены частично и не загружены вовсе, и сколько задач этого запуска в каждом состоянии. Закрытые списками доступа чаты не попадают ни в одно число.

## Scope

В scope:

- новое чтение в `internal/store` (агрегат над `chats`, `sync_state`, `messages` с фильтром доступа);
- типы RPC сводки, параметр `summary` у `history_sync.status`;
- обработчик `historySyncStatus` в `internal/daemon`;
- флаг CLI, описание в `tg schema`, тесты схемы;
- документы: `docs/cli/chats.md`, `docs/contracts.md`, `FEATURES.md`, навык `docs/skill/tg/`, запись changelog, файл решения.

Out of scope:

- дашборд веб-просмотрщика (`store.DashboardStats`) остаётся на прежнем `SUM(s.complete = 1)`; выравнивание со сводкой отдельной задачей (Q6);
- `chat overview`: его поля и лимит 500 не меняются;
- новая колонка «остановлен по пределу» и любая миграция (Q3);
- учёт `chat.history_mode` (`skip`, `first`) в корзинах (Q5);
- точный признак «вышел из чата» и деление по архиву (Q4);
- код и документы догонки.

Файлы: `internal/store/sync_summary.go` (новый) и `sync_summary_test.go`; `internal/rpc/sync_summary.go` (новый); `internal/rpc/api.go` (одно поле в `HistorySyncStatusParams` и одно в `HistorySyncJobs`); `internal/daemon/history_sync.go` (`historySyncStatus`) и тест; `internal/cli/chat_sync_cmd.go` и тесты; документы из шага 6.

## Main invariant

- Без нового флага ответ `chat sync status` в форматах `json`, `jsonl`, `toon`, `human` байт в байт прежний; `chat sync status <чат>` не меняется.
- Ни один чат, закрытый списками доступа, не входит ни в одно число сводки; при `Hide` все числа нули.
- Сводка не делает запросов к Telegram и не требует входа.
- Схема базы не меняется: миграций нет.
- В каждой строке сводки `total = complete + limited + with_gap + partial + empty` (при ответе Q2 по рекомендации).

## Контракт (предложение; окончательная форма после Q1-Q3)

Запрос: `tg chat sync status --summary` (RPC `history_sync.status` с `{"summary": true}`). Ответ: прежний `jobs` плюс объект `summary`:

```json
{"ok":true,"data":{"jobs":[],"summary":{
  "all":{"total":1311,"complete":398,"limited":15,"with_gap":0,"partial":300,"empty":598},
  "by_type":{
    "user":{"total":576,"complete":398,"limited":3,"with_gap":0,"partial":100,"empty":75},
    "bot":{"total":133,"complete":3,"limited":0,"with_gap":0,"partial":20,"empty":110},
    "group":{"total":304,"complete":1,"limited":0,"with_gap":0,"partial":100,"empty":203},
    "supergroup":{"total":111,"complete":2,"limited":4,"with_gap":0,"partial":40,"empty":65},
    "channel":{"total":187,"complete":9,"limited":8,"with_gap":0,"partial":40,"empty":130}},
  "jobs":{"queued":120,"running":1,"waiting_flood":0,"done":12,"failed":0,"cancelled":0}}}}
```

Числа в примере выдуманы для формы, кроме `total` по типам (замер владельца).

Корзины взаимно исключают друг друга; чат попадает в первую подходящую по порядку:

| Корзина | Условие |
| --- | --- |
| `with_gap` | `sync_state.gap_from IS NOT NULL` (D2, D-A3) |
| `limited` | `complete = 1`, у чата есть строка в `messages`, и `first_message_date IS NULL` или самая ранняя дата строк сообщений чата больше `first_message_date` (подчистка лимита удалила начало) |
| `complete` | `complete = 1` (дошёл до первого сообщения; пустой полностью загруженный чат тоже здесь) |
| `partial` | `complete = 0` или строки нет, есть хотя бы одно неудалённое сообщение |
| `empty` | остальное: ни одного неудалённого сообщения |

Поля:

- `all` и пять ключей `by_type` всегда есть, тип без чатов даёт нули: агенту не нужно проверять наличие ключа.
- `total` значит все строки `chats`, которые показывают списки доступа (Q4).
- «Загружен полностью» по решению `20261122_1410` значит `complete + limited`; документация и навык говорят это одной строкой.
- `summary.jobs` считает задачи из того же `snapshots()`, что и список `jobs`: шесть состояний, всегда все шесть (`idle` у задач списка не бывает).

Ошибки: `--summary` с чатом и `--summary` с `--format jsonl` дают код 2 (`usage`) до вызова Daemon. Совет агенту в навыке: `tg chat sync status --summary --fields summary`, тогда длинный список задач не печатается.

## Write and read paths

Запись: нет. Чтение:

1. CLI `chatSyncStatus` проверяет флаги, вызывает `history_sync.status` с `summary: true`.
2. Daemon `historySyncStatus`: при `p.Summary` и пустом `p.Chat` считает `d.sync.snapshots()` по состояниям и вызывает `d.store.SyncSummary(ctx, chatFilter(d.policy()))`. `requireAccount` не вызывается (как сейчас для `status` без чата и как у `chat overview`).
3. `store.SyncSummary` выполняет один `SELECT` над `chats c LEFT JOIN sync_state s` с `CASE` корзины и `GROUP BY c.type`; фильтр доступа через `filter.condition("c.id", false)` внутри запроса. Подзапросы `EXISTS` и `MIN(date)` по `chat_id` идут по индексам из факта 16. Представление `chats_state` не используется: оно считает тексты первого и последнего сообщения для каждой строки.
4. Пакеты: SQL живёт в `internal/store`, `store` достижим только через `daemon`; `internal/cli` видит только типы `internal/rpc`.

## Risks and strategy

- Риск: конфликт слияния с догонкой в `internal/rpc/api.go` и `internal/daemon/history_sync.go`. Решение: код после слияния догонки («Предусловие»); новые типы в отдельном файле `internal/rpc/sync_summary.go`, в `api.go` только два поля.
- Риск: эвристика `limited` ошибается (Q3). Чат, у которого полная загрузка пришла на пустой странице без сообщений (`first_message_date` пуст), а потом пришли живые сообщения, попадёт в `limited` вместо `complete`. Ошибка двигает чат между двумя корзинами «загружен полностью», итог `complete + limited` верен. Тест фиксирует этот случай как известное поведение.
- Риск: подчистка лимита чата, который был полным, оставляет `first_message_date`; правило «ранняя дата строк больше `first_message_date`» ловит его. Если появится другой путь физического удаления строк сообщений, правило даст ложный `limited`. Тест на подчищенный полный чат.
- Риск: стоимость запроса на большой базе. Решение: тест-замер на 1500 чатах и 100 000 сообщений во временной базе, ожидание меньше 200 мс на машине разработчика; при превышении шаг 2 останавливается и вопрос идёт владельцу (индекс значит миграцию `0015`, если догонка не займёт номер; номер брать следующим свободным после последней миграции в базе ветки).
- Риск: расхождение числа «полных» в сводке и в дашборде (факт 12). Решение: документация называет оба определения; выравнивание вынесено в Q6.
- Риск: агенты, которые разбирают `jobs` в `jsonl`. Решение: без флага ответ прежний; `--summary` с `jsonl` запрещён кодом 2.

## Decision log

| Date | Decision | Reason | Confirmed |
| --- | --- | --- | --- |
| 2026-11-23 | Ветка плана создаётся от ветки, в которую влита догонка; код только после «Предусловия». | Решение владельца из брифа: ждать догонку и ответвляться от неё. | yes |
| 2026-11-23 | «Полностью загружен» в отчёте значит `complete` и нет дыры; чат с дырой в отдельной корзине `with_gap` независимо от `complete`. | D2 и D-A3 плана догонки: дыра значит потерянный интервал. | yes (бриф) |
| 2026-11-23 | Источник сводки: один SQL-агрегат в `internal/store` через `daemon`, без Telegram и без входа. | Прецедент `DashboardStats` и `chat overview`; `cli` не импортирует `store`. | no |
| 2026-11-23 | Миграции нет: сводка читает существующие колонки. Если замер шага 2 потребует индекс, это миграция со следующим свободным номером после последней в базе ветки (ожидаемо `0015`) и новый вопрос владельцу. | Схема не нужна для чтения. | no |
| 2026-11-23 | Закрытые чаты отсекает `ChatFilter.condition` внутри SQL, `summary.jobs` считается из `snapshots()`, который уже фильтрует. | Тот же путь, что у `chat overview` и дашборда; закрытый чат не влияет ни на одно число. | no |
| 2026-11-23 | `--summary` с чатом и с `--format jsonl` дают код 2. | В `jsonl` объект рядом со списком теряется (факт 14); с чатом сводка не имеет смысла. | no |
| 2026-11-23 | Чат, остановленный пределом `history_limit.*` или лимитом чата, считается по базе (`complete = 1` без даты первого сообщения), а не по `depth_reached`. Загрузка с `--messages` или `--since` оставляет чат в `partial`. | Факты 8 и 9: бриф предполагал `depth_reached`, код ставит `complete`. | no |
| 2026-11-23 | Форма ответа, корзины, определение `limited`, база `total`: Q1-Q4, затем файл решения в шаге 7. | Решение переживёт план: контракт `tg schema`. | ожидает |

## Step 0. Предусловие и решения (код не меняется)

- [ ] Ответы владельца на Q1-Q6 внесены в Decision log и в «Open questions».
- [ ] Догонка влита. Проверка из корня ветки, от которой создаётся ветка плана: `git log --oneline -1 -- internal/store/migrations/0014_sync_gap.sql` печатает коммит; `rg -n 'history_gap_from' internal/store/overview.go internal/rpc/overview.go` находит поле в обоих файлах; `rg -n 'GapFrom' internal/rpc/api.go` находит поле `HistorySyncJob`. Ожидание: все три непустые.
- [ ] Номер следующей миграции записан в Known facts: `ls internal/store/migrations | tail -1`. Ожидание: `0014_sync_gap.sql` (тогда свободен `0015`); план миграцию не добавляет, номер нужен только для риска с индексом.
- [ ] Номер пункта `FEATURES.md`: `grep -n '^   10\.' FEATURES.md | tail -1`. Ожидание: `10.21` (догонка), новый пункт `10.22`.
- [ ] Ветка `feature/20261123_0355_sync_completeness_report` создана от результата слияния, отдельный worktree; заголовок плана обновлён, статус `in_progress` только после `approved`.

## Step 1. Baseline

- [ ] `go test -count=1 ./internal/store ./internal/daemon ./internal/cli` на базе ветки. Ожидание: зелёные; результат в Run log.
- [ ] Снимок схемы команды до правок: `dist/tg schema` (после `just build`), раздел `chat sync status` сохранить в `var/agents/plans/20261123_0355_sync_completeness_report/schema_before.json`. Ожидание: файл есть; нужен для сравнения в шаге 5.

## Implementation steps

Исполнитель: Codex Terra (`gpt-6.1-terra`) в табе `S2: sync report`, `--cwd` worktree ветки плана, один кодер на поток; сначала загружает скилл `expert-golang`. Ревьюеры не остаются запущенными. Менеджер: Sonnet.

### Шаг 2. Хранилище

- [ ] `internal/store/sync_summary.go`: тип `SyncSummaryCounts{Total, Complete, Limited, WithGap, Partial, Empty int}` и `func (db *DB) SyncSummary(ctx, filter ChatFilter) (map[string]SyncSummaryCounts, error)` (ключ: тип чата). Один запрос по разделу «Write and read paths», корзины по таблице «Контракт».
- [ ] Тесты `internal/store/sync_summary_test.go` на временной базе:
  - пустая база: пустой результат без ошибки;
  - по чату каждого типа в каждой корзине: числа по типам верны, `total` равен сумме корзин;
  - чат с `complete = 1` и `gap_from`, `gap_to`: `with_gap`, не `complete`;
  - строка, созданная отметкой дыры (D-A3: пустые id, `complete = 0`): `with_gap`;
  - чат после `MarkHistoryLimitReached` без `first_message_date`: `limited`;
  - полный чат с `first_message_date`, затем `TrimChatMessages` и `MarkHistoryLimitReached`: `limited`;
  - полный чат без сообщений: `complete`; тот же чат после вставки сообщения: `limited` (известное поведение эвристики, Q3);
  - чат только с удалёнными (`deleted_at`) сообщениями и `complete = 0`: `empty`;
  - фильтр: `Block` с id чата убирает его из всех чисел; `Allow` оставляет только перечисленные; `Hide` даёт пустой результат.
- [ ] Замер: тест или бенчмарк на 1500 чатах и 100 000 сообщениях. Ожидание: меньше 200 мс; результат в Run log. При превышении стоп и вопрос владельцу.
- [ ] Проверка: `go test -count=1 ./internal/store -run 'SyncSummary'`, зелёные.

### Шаг 3. RPC

- [ ] `internal/rpc/sync_summary.go`: `HistorySyncSummaryCounts` (поля `total`, `complete`, `limited`, `with_gap`, `partial`, `empty`), `HistorySyncSummaryByType` (поля `user`, `bot`, `group`, `supergroup`, `channel`), `HistorySyncJobCounts` (шесть состояний), `HistorySyncSummary{All, ByType, Jobs}`. Комментарии на английском, без `untrusted`: в сводке нет чужого текста.
- [ ] `internal/rpc/api.go`: `HistorySyncStatusParams.Summary bool json:"summary,omitempty"`; `HistorySyncJobs.Summary *HistorySyncSummary json:"summary,omitempty"`. Без флага поле отсутствует, и ответ прежний.
- [ ] Проверка: `go build ./...`, без ошибок.

### Шаг 4. Daemon

- [ ] `historySyncStatus`: при `p.Summary` и непустом `p.Chat` ошибка `CodeInvalidParams`; при `p.Summary` без чата сводка по разделу «Write and read paths». Копия из `store` в `rpc` в обработчике, как в `chatOverview`.
- [ ] Тесты в `internal/daemon` на фейковом клиенте и временной базе, без Telegram:
  - Daemon без входа отвечает сводкой (нет вызова `requireAccount`);
  - чат в `block` и чат вне `allow` не входят ни в `by_type`, ни в `all`, ни в `summary.jobs`;
  - `summary.jobs` совпадает с числом задач списка `jobs` по состояниям;
  - без `summary` ответ равен прежнему (сравнение JSON с ответом до правки);
  - `summary` с чатом даёт `invalid_params`.
- [ ] Проверка: `go test -count=1 ./internal/daemon -run 'HistorySync|SyncSummary'`, зелёные.

### Шаг 5. CLI и схема

- [ ] `internal/cli/chat_sync_cmd.go`: флаг `--summary` («add load counts of all visible chats»), `Long` дополнен одной строкой; код 2 при чате и при `--format jsonl` до соединения с Daemon.
- [ ] Вывод `human` и `toon` показывает сводку без потерь; проверка тестом CLI на фейковом сокете.
- [ ] `schema_fields_test.go`: поля `summary.*` описаны и сверены с реальным ответом; `TestSchemaListItemsMatchJSONL` зелёный.
- [ ] Сравнение схемы: новый снимок против `schema_before.json` из шага 1. Ожидание: добавлены только флаг `--summary` и поля `summary.*`.
- [ ] Проверка: `go test -count=1 ./internal/cli`, зелёные (тест документации упадёт до шага 6, это ожидаемо; шаг 5 закрывается вместе с шагом 6).

### Шаг 6. Документы

Тексты пишет `docwriter` и проверяет `glavred` по `.agents/rules/docs_chain.md`; механические правки (`just docs`, номера, ссылки) без `glavred`.

- [ ] `just docs`: таблица флагов `tg chat sync status` пересобрана; руками маркеры не правятся.
- [ ] `docs/cli/chats.md`, раздел `### tg chat sync status`: флаг, пример `tg chat sync status --summary --fields summary`, ответ, поля `summary.*`, порядок корзин, «полностью загружен = `complete` + `limited`», коды 2, отличие от дашборда.
- [ ] `docs/contracts.md`, пункт «Deep history load» (строка 12) или новый пункт рядом с «Batch of named chats»: источник (SQL в `store`, фильтр внутри запроса), без Telegram и входа, корзины и правило `limited`, известная ошибка эвристики.
- [ ] `FEATURES.md`: пункт `10.22` (номер из шага 0) с подпунктами и отметкой «не проверено на живом аккаунте»; ID не перенумеровываются.
- [ ] Навык `docs/skill/tg/references/history.md` (раздел «Deep history load»): как ответить на вопрос «сколько загружено», команда со `--fields summary`, сумма `complete + limited`, дыра не полная загрузка; рецепт `docs/skill/tg/recipes/sync-queue-control.md` (или `sync-active-chats.md`, выбрать по тексту рецепта): сводка до и после постановки очереди. Английский текст, русские только триггерные фразы. Ссылка на `FEATURES.md` 10.22 вместо списка непроверенного.
- [ ] Проверка: `go test -count=1 ./internal/cli` и `just skill-check`, зелёные; `git diff --stat docs/cli` показывает только раздел `chat sync status`.

### Шаг 7. Решение и changelog

- [ ] Файл решения в `.agents/decisions/` по `.agents/rules/decisions.md`, имя по `TZ=Europe/Moscow date +%Y%m%d_%H%M` и утверждению (например `..._sync_status_summary_counts_visible_chats_by_load_bucket`): форма ответа (Q1), корзины (Q2), правило `limited` (Q3), база `total` (Q4); `paths: [internal/store/sync_summary.go, internal/daemon/history_sync.go, internal/rpc/sync_summary.go]`. Решение `20261122_1410` не пересматривается: отчёт только различает два вида полной загрузки. Строка в `_TOC.md`; ячейка Decision log плана ссылается на файл.
- [ ] Запись changelog через `just changelog add` (текст по-английски, тип `Добавлено`, `--decision` на файл решения).
- [ ] Проверка: `just changelog list` показывает запись.

### Шаг 8. Закрытие

- [ ] `just check` зелёный; результат в Run log.
- [ ] Ревью диффа Claude; замечания закрыты или записаны.
- [ ] Коммит через скилл `git-commit`, по-английски, без подписей; только по команде владельца.

## Verification

| Check | Command | Expected result | Status |
| --- | --- | --- | --- |
| Предусловие | `git log --oneline -1 -- internal/store/migrations/0014_sync_gap.sql` | коммит догонки | not_run |
| Хранилище | `go test -count=1 ./internal/store -run SyncSummary` | зелёные | not_run |
| Daemon | `go test -count=1 ./internal/daemon -run 'HistorySync\|SyncSummary'` | зелёные | not_run |
| CLI и документы | `go test -count=1 ./internal/cli` | зелёные | not_run |
| Навык | `just skill-check` | без расхождений | not_run |
| Схема | сравнение с `schema_before.json` | добавлены только `--summary` и `summary.*` | not_run |
| Всё | `just check` | зелёный | not_run |
| Затронутые тесты | `git diff HEAD --name-only --relative \| bin/atlas affected - -o path` | перечисленные пакеты прогнаны | not_run |

## Deploy / rollback

- [ ] Раскатка: сборка `tg` и перезапуск Daemon по обычному порядку проекта. Перезапуск сбрасывает задачи памяти (решение `20261114_1353`): предупредить владельца, если очередь не пуста.
- [ ] Откат: прежний бинарник. Миграции нет, база совместима в обе стороны.
- [ ] Кэши, очереди, воркеры: не затрагиваются.

## Cleanup

- [ ] Временные файлы только в `var/agents/plans/20261123_0355_sync_completeness_report/`, вне git.
- [ ] В `.agents/plans/` только файл плана.

## Definition of done

- [ ] Код изменён только в scope; миграций нет.
- [ ] Main invariant держится: ответ без флага прежний (тест), закрытые чаты не считаются (тест), сводка без входа (тест).
- [ ] Все проверки таблицы Verification зелёные или отклонение записано.
- [ ] Документы, навык, `FEATURES.md`, changelog и решение в том же коммите, что код.
- [ ] Строки Decision log, пережившие план, ссылаются на файл решения.
- [ ] Нет `- [0]`.

## Mismatches found

- Бриф предполагал, что чат, остановленный лимитом, получает `depth_reached`, а не `complete`. Код: `history_limit.*` и лимит чата ставят `complete = 1` через `MarkHistoryLimitReached` (`internal/store/sync_state.go:167-190`, `internal/daemon/history_sync.go:537-538`, `internal/daemon/chat_limit.go:76`); `depth_reached` только у задачи с `--messages` или `--since` и только в памяти. План следует коду.
- `docs/contracts.md:14` говорит, что `background_sync.enabled` выключен по умолчанию; навык (`references/history.md:61`) и план догонки говорят «включён». Правит план догонки (шаг 11); здесь не трогается.

## Assumptions

- Догонка вольётся с теми именами колонок и полей, что в её разделе «New data model and contract». Если имена изменятся, шаг 2 берёт фактические из базы ветки.
- Около 1300 чатов (замер владельца) достаточно для оценки стоимости запроса; число сообщений неизвестно.

## Open questions

| Question | Why it blocks | Options | Recommendation | Status |
| --- | --- | --- | --- | --- |
| Q1. Как сводка появляется в `chat sync status`? | Определяет контракт `tg schema` и цену каждого опроса | (a) объект `summary` всегда в ответе без чата; (b) только с флагом `--summary`; (c) отдельная команда `chat sync report` | (b). Цена: агент должен знать флаг (навык и `tg schema` это говорят); ответ без флага не меняется, опрос прогресса не платит около 200 токенов (факт 18) и SQL по всем чатам. (a) буквальнее просьбы «в status», но добавляет 200 токенов к каждому опросу и меняет ответ всем потребителям. (c) против слов владельца | open |
| Q2. Корзины взаимно исключают друг друга или `limited` и `with_gap` идут подмножеством `complete`? | Определяет, как агент складывает числа | (a) пять непересекающихся корзин, `total` равен сумме; (b) `complete` по базе (как `history_complete`) плюс счётчики «из них по пределу» и «из них с дырой» | (a). Цена: `complete` сводки меньше числа `history_complete: true` из `chat overview` и дашборда; документация говорит «полностью загружен = `complete` + `limited`». (b) путает агента двойным счётом | open |
| Q3. Как отличить остановку по пределу от загрузки до первого сообщения? | Решение `20261122_1410` отвергло колонку; без неё нужен вывод из данных | (a) правило из таблицы «Контракт» без миграции; (b) миграция `0015` с колонкой `sync_state.limit_reached`, которую ставит `MarkHistoryLimitReached`, и заполнение по правилу (a); пересматривает `20261122_1410` | (a). Цена: чат, полностью загруженный пустым и получивший потом сообщения, попадёт в `limited` вместо `complete`; сумма «полностью загружен» верна. (b) точнее, но добавляет миграцию, правку чужого решения и второе состояние, которое должны учитывать все читатели `complete` | open |
| Q4. Что входит в `total`? | Число «всего чатов» в отчёте | (a) все строки `chats`, видимые списками доступа, архив и вышедшие чаты внутри; (b) только чаты с записью `dialog_state`; (c) (a) плюс отдельный счётчик архивных | (a). Цена: в `total` попадают чаты из `chat resolve` и `chat common`, которых нет в диалогах, и чаты, из которых владелец вышел: признака выхода в базе нет (факт 13). (b) теряет чаты без `chat state`. (c) неточен: `archived` есть только после `chat state` | open |
| Q5. Учитывать ли `chat.history_mode = skip`? | Владелец исключил такие чаты из планировщика, а сводка покажет их как `empty` или `partial` | (a) не учитывать; (b) отдельный счётчик `skipped` поверх корзин | (a) в этом плане. Цена: исключённые владельцем чаты выглядят незагруженными; (b) добавляет поле и тесты, можно отдельной задачей | open |
| Q6. Выравнивать ли дашборд веб-просмотрщика на те же корзины? | Дашборд считает предел и дыру полной загрузкой (факт 12); два разных числа «полных» | (a) отдельной задачей после этого плана; (b) в этом плане, `DashboardStats` зовёт `SyncSummary` | (a). Цена: до той задачи два определения, документация называет оба. (b) расширяет scope на `internal/web` и его тесты | open |

## Run log

- 2026-11-23: план создан по брифу `var/agents/plans/20261123_0355_sync_completeness_report/planner_brief.md`; код не менялся, коммита нет.
