---
decided: 2026-11-20
decided_by: {name: "Eugene Myazin", email: eugene.myazin@gmail.com, github: meniam}
confirmed: yes
revises: [20261115_1213_search_goes_through_atlas_and_the_pathfinder_agent]
paths: [.agents/skills/scout, .claude/skills/scout, .codex/skills/scout, .claude/settings.json, AGENTS.md]
---
# Поиск по проекту это скилл scout без отдельного агента

## Context

Меняет [Поиск по коду и документам идёт через Atlas и агента pathfinder](20261115_1213_search_goes_through_atlas_and_the_pathfinder_agent.md)
только в части агента. Остаются в силе поставка Atlas (`bin/apps/atlas`, `bin/atlas`), `.atlas.json`,
место индекса и хук `lefthook.yml`.

Агент и скилл назывались одинаково. Агент жил в шести местах: `.agents/agents/`, профили
`.claude/agents/` и `.codex/agents/` (у Codex профиль на `gpt-6.1-terra`), хук в `.claude/settings.json`,
исключение в правиле про herdr. Скилл общий для всех клиентов, а запрет записи держался только на
хуке Claude Code, который срабатывал на `agent_type == "pathfinder"`.

## Decision

- Агента `pathfinder` нет: удалены `.agents/agents/`, `.claude/agents/pathfinder.md`,
  `.codex/agents/pathfinder.toml`, хук `readonly-guard.py` и запись `hooks` в `.claude/settings.json`.
- Скилл переименован в `scout` (`.agents/skills/scout/`, заглушки в `.claude/skills/` и
  `.codex/skills/`). Из frontmatter убраны `model` и `effort`: они не входят в общий формат скиллов и
  задавали модель и уровень рассуждения, которые остались от агента.
- Порядок работы и команды Atlas не менялись. В скилл перенесены формат отчёта и правила поиска
  (ничего не менять, не читать секреты, при коде выхода 1 или 2 остановиться), раздел
  «Answer format» и «Rules of the search».
- `AGENTS.md`, раздел «Search»: перед любым поиском главный агент загружает скилл `scout` и ищет
  сам. Исключение «кроме pathfinder» из правила про herdr убрано.

## Why

- Изоляция контекста через `context: fork` не вошла: владелец требует одинаковую работу скилла в
  Claude Code, Codex и Pi, а наличие `fork` в Codex и Pi не подтверждено.
- Жёсткий запрет записи держался на хуке Claude Code и действовал только на агента с именем
  `pathfinder`. Команды Atlas читают, а главный агент и так вправе писать, поэтому правило
  записано текстом в скилле.
- Оставить агента и только переименовать его проигрывал: дублирование в шести файлах сохранялось.

## Cost

- JSON от `explore` и `node` попадает в контекст того, кто ищет. Смягчают `explore --limit` и
  `--max-chars`, `node` без `--code` и чтение по диапазону строк. Если окно начнёт забиваться, вернуться к изоляции, когда `context: fork`
  будет в Codex и Pi.
- Запрет записи при поиске держится на дисциплине, а не на хуке.
- Поиск идёт на основной модели сессии, а не на Haiku; стоимость вызова зависит от этой модели и не измерялась.
