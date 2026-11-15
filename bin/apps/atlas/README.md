# Atlas 0.1.0

Сборка от 2026-11-15.
Не правь здесь: изменения вносятся в репозиторий Atlas и копируются заново.

Папка лежит в bin/apps/atlas проекта. Запуск от корня проекта идёт через bin/atlas,
это обёртка, а не симлинк: uv ищет пакет рядом со ссылкой, а не с её целью.

    cat >bin/atlas <<'SH'
    #!/usr/bin/env bash
    exec "$(dirname "$0")/apps/atlas/atlas" "$@"
    SH
    chmod +x bin/atlas

    bin/atlas schema       # схема .atlas.json для корня проекта
    bin/atlas sync --full

.atlas.json ищется вверх от пускового файла, поэтому находится в корне проекта.

Модуль just: строка в justfile проекта, дальше справка по `just atlas`.

    mod atlas 'bin/apps/atlas/justfile'

Нужен только uv: Python 3.14 и зависимости он поставит сам при первом запуске.
