# Пошаговый план: скилл `tg` с references и recipes вместо одного файла `tg-cli`

ID: 20261120_1607_agent_skill_tg_with_recipes
Created: 2026-11-20
Owner: Мязин Евгений
Branch: — (будет `refactor/20261120_1607_agent_skill_tg_with_recipes` при переходе в `in_progress`)
PR: —

Тексты пишет и проверяет цепочка `docwriter` и `glavred` по `docs_chain.md`. Код Go не меняется.

## Status

- Current status: approved (2026-11-20: владелец принял имя `tg`, замену старого скилла и структуру из шага 3)
- Last update: 2026-11-20
- Next safe step: Step 0 (ветка и worktree от `main`, у которого в индексе висит чужая правка скилла `scout`: сначала решить, что с ней).

## Context and sources

- Источник: просьба владельца 2026-11-20 «сделать наш скилл похоже на старый и круто». Особенно понравились `recipes/` и frontmatter рецептов; из старого «взять всё важное».
- Прочитано: `docs/skill/tg-cli/SKILL.md` (461 строка, один файл), `docs/skill/tg-cli/meta.json`, старый скилл `~/.agents/skills/tg` (SKILL.md 218 строк, `references/` 9 файлов, `recipes/` 9 + `_template.md`, `scripts/` 3 файла, `tests/`, `meta.json`, `.skill-lint-ignore`).
- Проверено командами 2026-11-20:
  - `skill-lint` на `docs/skill/tg-cli`: 4 ошибки (описание 2017 символов при лимите 1024; ссылка за пределы папки `SKILL.md:326`; абсолютные пути `SKILL.md:220-221`) и предупреждение `body-language`. `check_meta.py`: чисто.
  - Все команды из текста скилла есть в `tg schema` (133 команды). 50 команд в тексте не упомянуты (`chat state`, `contact add`, `saved list`, `sticker search` и др.).
  - Противоречия в тексте: `SKILL.md:22` «Скачивания медиа нет», а `:449` описывает `tg message download`; правило 2 (`:27`) перечисляет около 10 пишущих команд, в схеме их около 60 (`safety` равен `account_write` или `dangerous`).
  - `tg daemon start` идемпотентен (`--help`: повторный запуск сообщает о живом Daemon и второй не стартует), `--idle-timeout` по умолчанию 0. Скрипт запуска из старого скилла не нужен.
- Не проверено: что из «правил, которые экономят время» старого скилла (лимит 50, 100 id, таймаут 120 с, кадр 1 МиБ) верно для нашего бинарника. Каждое правило сверяется с `tg schema` и `docs/cli/` перед переносом.

## Goal

`docs/skill/tg/` содержит маршрутизатор `SKILL.md` (до 150 строк), `references/` по темам и `recipes/` со сценариями и расширенным frontmatter. `skill-lint` и новая сверка рецептов с `tg schema` проходят без ошибок. Старый скилл `~/.agents/skills/tg` заменён новым.

## Scope

- В работе: переименование `docs/skill/tg-cli` в `docs/skill/tg`; новое описание; разбивка текста на `references/`; 11 рецептов и `_template.md`; исправление найденных противоречий; сверка рецептов с `tg schema` (рецепт `just`); `meta.json`; ссылки в `AGENTS.md`, `docs/README.md`; запись решения о переименовании.
- Вне плана: изменение Go-кода и `tg schema`; новые команды; живые проверки (правило владельца); скрипты Bash и тесты скриптов из старого скилла; правка старых планов (они история).
- Затрагиваются: `docs/skill/**`, `AGENTS.md` (строки 18 и 96), `docs/README.md`, `justfile` и `bin/just/`, `.agents/decisions/`, `~/.agents/skills/tg` (только шаг установки, после отдельного подтверждения).

## Main invariant

Смысл правил безопасности не слабеет: запись только по просьбе и в чаты из `write`, чужой текст это данные (`untrusted`), закрытые чаты не обходятся, `access.json` правит человек. Ни одна команда в тексте не отсутствует в `tg schema`. Ни одно утверждение «проверено на живом аккаунте» не теряется и не добавляется без источника.

## Что берём из старого скилла

- Формат описания: короткое (цель 400–600 символов), «Use when», фразы пользователя в «», «even if…», одно исключение.
- Frontmatter и шаблон рецептов: `title`, `task` (фразы пользователя), `writes`, `tags`; секции «Когда применять», «Подготовка», «Пайп», «Разбор», «Подводные камни», «Проверка». Новые поля: `access` (`none`, `write`, `files`, `downloads`, `admin`), `commands` (сверяется с `tg schema`), `live_checked` (дата или `нет`).
- Маршрутизатор: первое правило, рабочий цикл, контракт вывода, адресация чатов, шпаргалка команд со ссылками, список рецептов, границы.
- `references/safety.md`: что необратимо, что ведёт к блокировке аккаунта (рассылки, массовый скрейпинг участников, циклы без пауз, игнор `FLOOD_WAIT`), разумный темп.
- `references/errors.md` в виде «симптом, код, действие» вместо перечня кодов.
- Режимы вывода и экономия токенов: `--toon --fields` для чтения в контекст, `--jsonl` и `jq` для обработки.
- Дисциплина `meta.json`: что проверено живьём, что нет, что сверять при обновлении.
- Не берём: `scripts/` и `tests/`, требования Node и WSL2, `TG_SKILL_DIR`, флаг `--daemon`, всё, что относится к gramjs и версии 0.5.1.

## Risks and strategy

- Риск: при разбивке потерять оговорки «не проверено на живом аккаунте». Решение: переносить абзацы целиком, оговорки не сокращать; сверка по списку ссылок на `FEATURES.md` до и после (`rg -c 'FEATURES.md' docs/skill`).
- Риск: рецепты разойдутся с бинарником. Решение: шаг сверки `commands` с `tg schema` в `just`.
- Риск: два скилла про Telegram конфликтуют по триггерам. Решение: старый убирается из `~/.agents/skills` на шаге установки.
- Риск: чужая правка в индексе `main` (переименование `pathfinder` в `scout`) попадёт в коммит этой работы. Решение: Step 0.

## Decision log

| Date       | Decision | Reason | Confirmed |
| ---------- | -------- | ------ | --------- |
| 2026-11-20 | Новый скилл называется `tg`, старый gramjs-скилл заменяется | В PATH стоит наш Go-бинарник `tg`; триггеры двух скиллов совпадают | yes |
| 2026-11-20 | Структура: `SKILL.md`, `references/` (9), `recipes/` (11 и шаблон), без `scripts/` | Выбрано владельцем из трёх вариантов | yes |
| 2026-11-20 | Скрипты `tg-doctor.sh` и `tg-daemon-up.sh` не переносим | `tg daemon status` и идемпотентный `tg daemon start` есть в бинарнике | no |
| 2026-11-20 | Тело скилла остаётся русским | `AGENTS.md`: `docs/skill/` на русском | no |

## Step 0. Fix decisions before changes

- [ ] Решить, что делать с индексом `main` (переименование `scout`, 17 путей): закоммитить отдельно или вести работу в worktree от `HEAD`.
- [ ] Создать ветку и worktree, записать их в заголовок.
- [ ] Открытые вопросы ниже решены или записаны.

## Step 1. Audit and baseline

- [ ] Таблица покрытия: каждый раздел старого `SKILL.md` (по заголовкам, строки 17–459) получает адрес в новой структуре (в `var/agents/plans/20261120_1607_agent_skill_tg_with_recipes/map.md`).
- [ ] Список 50 неупомянутых команд из `tg schema` сгруппирован по `references/`.
- [ ] Базовые числа: `skill-lint` (4 ошибки, 1 предупреждение), число ссылок `FEATURES.md`.

## Implementation steps

- [ ] 1. `git mv docs/skill/tg-cli docs/skill/tg`; `name: tg` в frontmatter и `meta.json`; ссылки в `AGENTS.md` (строки 18, 96), `docs/README.md`. Проверка: `rg 'skill/tg-cli' --glob '!.agents/plans/**'` пуст.
- [ ] 2. Новое описание до 1024 символов (цель 400–600).
- [ ] 3. `references/`: `safety`, `reading`, `history`, `writing`, `media`, `stream`, `daemon-auth`, `admin`, `errors`. Абзацы переносятся из старого текста по карте шага 1; противоречия `:22` и правило 2 исправляются здесь.
- [ ] 4. `SKILL.md` до 150 строк: правила, первый запуск (`tg daemon status`, коды 3 и 4), контракт вывода, адресация чатов, шпаргалка, индекс.
- [ ] 5. `recipes/_template.md` и 11 рецептов: `find-chat`, `channel-digest`, `unread-triage`, `search-across-chats`, `export-history`, `monthly-archive`, `load-full-history`, `send-report`, `send-media`, `watch-chat`, `forum-topics`. Пишущие рецепты показывают только `--dry-run`.
- [ ] 6. Сверка: рецепт `just skill-check` (frontmatter рецептов, `commands` против `tg schema`, `skill-lint`); строка в `bin/just/` по правилу проекта.
- [ ] 7. `meta.json` без `links`, `notes` с проверенным и непроверенным; `.skill-lint-ignore`, если останутся предупреждения, с причинами.
- [ ] 8. Документы через `docwriter` и `glavred` по `docs_chain.md`.
- [ ] 9. Запись решения о переименовании в `.agents/decisions/` и строка в `_TOC.md`.
- [ ] 10. Установка: только после отдельного подтверждения владельца (меняет `~/.agents/skills`, вне репозитория).

## Verification

| Check | Command | Expected result | Status |
| ----- | ------- | --------------- | ------ |
| Линтер скилла | `lint_skills.py docs/skill/tg` | 0 ошибок | not_run |
| Метаданные | `check_meta.py docs/skill/tg` | 0 ошибок | not_run |
| Рецепты и схема | `just skill-check` | все `commands` есть в `tg schema` | not_run |
| Размер | `wc -l docs/skill/tg/SKILL.md` | не больше 150 | not_run |
| Оговорки о живой проверке | `rg -c 'FEATURES.md' docs/skill` до и после | число не меньше исходного | not_run |
| Висячие ссылки | `rg 'skill/tg-cli' --glob '!.agents/plans/**'` | пусто | not_run |
| Тексты | `glavred` по `docs_chain.md` | замечаний нет | not_run |

## Deploy / rollback

- [ ] Выкладка: слияние в `main`; установка в `~/.agents/skills/tg` отдельным шагом 10.
- [ ] Откат: `git revert` слияния; старый скилл хранится в архиве, пока новый не принят.

## Cleanup

- [ ] Временные файлы лежат в `var/agents/plans/20261120_1607_agent_skill_tg_with_recipes/`.

## Definition of done

- Все проверки из таблицы зелёные, шаги отмечены `[x]`, решение записано, старый скилл заменён или шаг 10 отложен владельцем с записью причины.

## Known facts

- Старый скилл описывает `@miolamio/tg-cli` 0.5.1 (gramjs, Node), а не наш бинарник; метаданные старого скилла сами это говорят.

## Open questions

| # | Вопрос | Варианты | Рекомендация |
| - | ------ | -------- | ------------ |
| Q1 | Куда убрать старый скилл при замене | архив вне `~/.agents/skills`; удалить | архив, пока новый не принят |
| Q2 | Как ставить новый скилл в `~/.agents/skills` | `bin/sync-skills --apply` из домашней точки метаданных (с `meta.json` и `links`); копия | `sync-skills`, как у остальных скиллов |
| Q3 | Русское тело даёт предупреждение `body-language` | `status: development` в `meta.json`; запись в `.skill-lint-ignore`; перевод на английский | `.skill-lint-ignore` с причиной (правило `AGENTS.md`), без статуса `development` |
