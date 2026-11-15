---
decided: 2026-11-15
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
paths: [.atlas.json, lefthook.yml, bin/atlas, bin/apps/atlas, .agents/agents/pathfinder.md, .agents/skills/pathfinder, .claude/settings.json]
---
# Поиск по коду и документам идёт через Atlas и агента pathfinder

## Context

В проекте около 20 пакетов Go, документы на двух языках (`INTENT.md`, `FEATURES.md`, решения и
`docs/skill/` по-русски, код, `docs/cli/` и `docs/contracts.md` по-английски) и журнал решений.
Поиск `rg` по такому набору не отличает замысел от реализации и не показывает цепочку вызовов.
В `4p/catalog` та же задача решена Atlas и агентом `pathfinder`; Atlas понимает Go с коммита
`feat: поддержка языка Go` в репозитории Atlas.

## Decision

- В проект скопирована сборка Atlas: `bin/apps/atlas/` (не правится, обновляется целой папкой),
  запуск `bin/atlas`, модуль `just atlas`.
- Конфиг `.atlas.json`: код `cmd` и `internal`; документы `docs`, `README.md`, `AGENTS.md`,
  `INTENT.md`, `FEATURES.md`, `.agents/rules`, `.agents/skills`; исключены `dist`, `var`,
  `testdata`, `.agents/plans`; области `decision`, `learning`, `changelog` подключаются явно.
  Индекс лежит в `var/cache/atlas` и в Git не входит.
- Агент `pathfinder` скопирован из `4p/catalog` и переписан под tg-cli: общая инструкция
  `.agents/agents/pathfinder.md`, скилл `.agents/skills/pathfinder/`, профили для Claude Code
  (Haiku) и Codex, заглушки скилла в `.claude/skills/` и `.codex/skills/`.
- Shell агента режет хук `PreToolUse` из `.claude/settings.json`
  (`.agents/skills/pathfinder/scripts/readonly-guard.py`): проходят только команды чтения.
- `lefthook.yml`: после смены ветки и `pull` индекс досинхронизируется в фоне (`bin/atlas sync`),
  без `uv` и без `bin/atlas` хук молча пропускается.
- В `AGENTS.md` добавлен раздел «Search»: любой поиск по проекту отдаётся `pathfinder`.

## Why

- Atlas отдаёт документ, затем цепочку вызовов и область влияния за один вызов, а `rg` нужен
  только для строк и того, что вне карты.
- Копия сборки, а не зависимость на соседний репозиторий: проект собирается и работает без доступа
  к репозиторию Atlas, как в `4p/catalog`.
- Поиск только на чтение не требует кодера в herdr-панели, поэтому `pathfinder` дешёвый
  субагент на Haiku, а не Codex-агент.
- Хук лежит в `.claude/settings.json`, а не во frontmatter профиля: хук профиля не срабатывает для
  субагента, вызванного через Agent tool (проверено в `4p/catalog`).

## Cost

- Правило «каждый агент работает в herdr-панели, не субагентом Claude» получило исключение для
  `pathfinder`.
- Atlas требует `uv` на машине разработчика; Python 3.14 он ставит сам.
- Граф Go неполон: вызов через интерфейс доходит до объявления, `implements` выводится по именам
  методов, часть ссылок идёт в `gotd` и стандартную библиотеку, вне проекта. Пустой ответ графа
  означает «граф не знает».
- Копия `bin/apps/atlas` стареет: обновление это новая сборка из репозитория Atlas и замена папки.
- Русские документы ищутся русскими словами, а код английскими; синонимов в `.atlas.json` нет.
  Если частый русский термин не находит код, добавить `search.synonyms`.
