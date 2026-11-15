#!/usr/bin/env bash
# wrap-up-check.sh: сбор фактов для ритуала wrap-up, только чтение.
#
# Печатает по порядку: состояние репозитория, worktree и stash, изменённые
# и неотслеживаемые файлы, подозрительные файлы, отладочный мусор и возможные
# секреты в добавленных строках, документы, не менявшиеся при изменении кода,
# найденные команды quality gates и процессы, похожие на процессы проекта.
#
# Использование:
#   wrap-up-check.sh [--base <ref>] [--no-procs] [--no-secrets] [--max-lines N]
#
# --base       База для diff. По умолчанию merge-base с origin/main, origin/master,
#              main, master (первый существующий), иначе HEAD.
# --no-procs   Пропустить сканирование процессов и портов (на некоторых машинах медленно).
# --no-secrets Пропустить скан секретов.
# --max-lines  Максимум строк на секцию (по умолчанию 40).
#
# Ничего не изменяет. Совместим с macOS /bin/bash 3.2.

set -u

BASE=""
BASE_LABEL=""
DO_PROCS=1
DO_SECRETS=1
MAX_LINES=40

while [ $# -gt 0 ]; do
  case "$1" in
    --base) BASE="$2"; shift 2 ;;
    --no-procs) DO_PROCS=0; shift ;;
    --no-secrets) DO_SECRETS=0; shift ;;
    --max-lines) MAX_LINES="$2"; shift 2 ;;
    -h|--help) sed -n '2,19p' "$0"; exit 0 ;;
    *) echo "неизвестная опция: $1" >&2; exit 2 ;;
  esac
done

section() { printf '\n== %s ==\n' "$1"; }
note() { printf '   %s\n' "$1"; }
capped() { head -n "$MAX_LINES"; }

IN_GIT=0
ROOT="$(pwd)"
if command -v git >/dev/null 2>&1 && git rev-parse --show-toplevel >/dev/null 2>&1; then
  IN_GIT=1
  ROOT="$(git rev-parse --show-toplevel)"
  cd "$ROOT" || exit 1
fi

section "Контекст"
note "cwd: $ROOT"
note "дата: $(date '+%Y-%m-%d %H:%M')"
if [ "$IN_GIT" -eq 0 ]; then
  note "не git-репозиторий: секции про git пропущены"
fi

# ---------------------------------------------------------------- git state
if [ "$IN_GIT" -eq 1 ]; then
  section "Репозиторий"
  BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
  note "ветка: $BRANCH"
  UPSTREAM="$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)"
  if [ -n "$UPSTREAM" ]; then
    COUNTS="$(git rev-list --left-right --count "$UPSTREAM"...HEAD 2>/dev/null)"
    note "upstream: $UPSTREAM (behind ${COUNTS%%	*}, ahead ${COUNTS##*	})"
  else
    note "upstream: нет (ветка не запушена или tracking не настроен)"
  fi

  if [ -z "$BASE" ]; then
    for cand in origin/main origin/master main master; do
      if git rev-parse --verify -q "$cand" >/dev/null 2>&1; then
        MB="$(git merge-base HEAD "$cand" 2>/dev/null)"
        if [ -n "$MB" ]; then BASE="$MB"; BASE_LABEL="merge-base с $cand"; break; fi
      fi
    done
  else
    BASE_LABEL="$BASE"
  fi
  if [ -z "$BASE" ]; then BASE="HEAD"; BASE_LABEL="HEAD (ветка main не найдена)"; fi
  note "база diff: $BASE_LABEL"

  if [ "$BASE" != "HEAD" ] && [ "$(git rev-parse "$BASE")" != "$(git rev-parse HEAD)" ]; then
    note "коммиты после базы:"
    git log --no-merges --format='     %h %s' "$BASE"..HEAD | capped
  else
    note "коммиты после базы: нет"
  fi

  section "Worktree"
  git worktree list | while IFS= read -r line; do note "$line"; done
  WT_COUNT="$(git worktree list | wc -l | tr -d ' ')"
  if [ "$WT_COUNT" -gt 1 ]; then
    note "worktree больше одного: перед удалением проверить каждый на несмерженные коммиты"
  fi

  section "Stash"
  if [ -n "$(git stash list)" ]; then
    git stash list | while IFS= read -r line; do note "$line"; done
  else
    note "нет"
  fi

  section "Локальные ветки, не смерженные в HEAD"
  UNMERGED="$(git branch --no-merged HEAD 2>/dev/null | sed 's/^[* ] *//' | grep -v -E '^(main|master|develop)$')"
  if [ -n "$UNMERGED" ]; then
    echo "$UNMERGED" | while IFS= read -r b; do note "$b"; done
  else
    note "нет"
  fi

  # -------------------------------------------------------------- changes
  section "Рабочее дерево (git status --short)"
  STATUS="$(git status --short)"
  if [ -n "$STATUS" ]; then echo "$STATUS" | capped; else note "чистое"; fi

  section "Diff stat относительно базы (закоммиченное + staged + unstaged)"
  DIFFSTAT="$(git diff --stat "$BASE" 2>/dev/null | tail -n 30)"
  if [ -n "$DIFFSTAT" ]; then echo "$DIFFSTAT"; else note "изменений нет"; fi

  UNTRACKED="$(git ls-files --others --exclude-standard)"
  section "Неотслеживаемые файлы"
  if [ -n "$UNTRACKED" ]; then echo "$UNTRACKED" | capped; else note "нет"; fi

  section "Подозрительные файлы (в изменениях или неотслеживаемые)"
  CHANGED_ALL="$( (git diff --name-only "$BASE"; echo "$UNTRACKED") | grep -v '^$' | sort -u)"
  SUS="$(echo "$CHANGED_ALL" | grep -E -i \
    '(\.(log|orig|bak|tmp|swp|swo|pyc|DS_Store|pem|key|p12|pfx|sqlite|db)$|(^|/)\.env(\.|$)|(^|/)(credentials|secrets?)(\.|$)|\.rej$|~$|(^|/)npm-debug|(^|/)yarn-error|(^|/)coverage/|(^|/)dist/|(^|/)build/|(^|/)__pycache__/|(^|/)\.venv/|(^|/)node_modules/)' )"
  if [ -n "$SUS" ]; then
    echo "$SUS" | capped
    note "решить по каждому: коммитить, удалить или добавить в .gitignore?"
  else
    note "нет"
  fi

  section "Артефакты инструментов агента в репозитории"
  # Папки и файлы, которые оставляют браузерные и тестовые инструменты. Ищем независимо
  # от .gitignore: заигноренный мусор всё равно мусор.
  ART_FOUND=0
  for a in .playwright-cli .playwright-mcp playwright-report test-results .scratch scratch tmp .tmp screenshots .herdr; do
    [ -e "$a" ] || continue
    if git ls-files --error-unmatch "$a" >/dev/null 2>&1; then ST="отслеживается git"
    elif git check-ignore -q "$a" 2>/dev/null; then ST="в .gitignore"
    else ST="не отслеживается"; fi
    SZ="$(du -sh "$a" 2>/dev/null | cut -f1)"
    note "$a/ ($SZ, $ST)"; ART_FOUND=1
  done
  ROOT_ART_PATTERN='(^page-[0-9T:-]+\.ya?ml$|\.webm$|\.har$|trace.*\.zip$|^screenshot.*\.png$|^page.*\.png$)'
  ROOT_ART="$(for a in ./*; do
    [ -f "$a" ] || continue
    name="${a#./}"
    if [[ "$name" =~ $ROOT_ART_PATTERN ]]; then printf '   %q\n' "$name"; fi
  done)"
  if [ -n "$ROOT_ART" ]; then
    printf '%s\n' "$ROOT_ART" | capped; ART_FOUND=1
  fi
  if [ "$ART_FOUND" -eq 1 ]; then
    note "закрыть сессии инструмента (playwright-cli close) и удалить то, что не отслеживается git и не нужно проекту"
  else
    note "нет"
  fi

  section "Большие файлы в изменениях (> 1 МБ)"
  LARGE="$(echo "$CHANGED_ALL" | while IFS= read -r f; do
    [ -f "$f" ] || continue
    SZ="$(wc -c < "$f" | tr -d ' ')"
    if [ "$SZ" -gt 1048576 ]; then echo "$f ($((SZ / 1024)) КБ)"; fi
  done)"
  if [ -n "$LARGE" ]; then echo "$LARGE" | sed 's/^/   /' | capped; else note "нет"; fi

  section "Покрытие .gitignore"
  if [ -f .gitignore ]; then
    MISSING=0
    for pat in '.env' 'node_modules' '__pycache__' '.venv' 'dist' '.DS_Store'; do
      for candidate in ./"$pat"* ./*/"$pat"; do
        [ -e "$candidate" ] || [ -L "$candidate" ] || continue
        if ! git check-ignore -q -- "$candidate" 2>/dev/null; then
          note "есть в репозитории, но не заигнорено: $(printf '%q' "${candidate#./}")"
          MISSING=1
        fi
      done
    done
    [ "$MISSING" -eq 0 ] && note "ок (проверены .env, node_modules, __pycache__, .venv, dist, .DS_Store)"
  else
    note "в корне репозитория нет .gitignore"
  fi

  # ----------------------------------------------------- added-line scans
  ADDED="$(git diff "$BASE" -U0 --no-color 2>/dev/null | grep -E '^\+\+\+ |^\+[^+]' )"
  # Добавленные строки с префиксом файла для читаемости.
  ADDED_WITH_FILE="$(echo "$ADDED" | awk '
    /^\+\+\+ / { f=$2; sub(/^b\//,"",f); next }
    /^\+/ { print f ": " substr($0,2) }')"
  UNTRACKED_TEXT=""
  if [ -n "$UNTRACKED" ]; then
    UNTRACKED_TEXT="$(echo "$UNTRACKED" | while IFS= read -r f; do
      [ -f "$f" ] || continue
      case "$f" in (*.png|*.jpg|*.jpeg|*.gif|*.pdf|*.zip|*.gz|*.woff|*.woff2|*.ico|*.lock) continue ;; esac
      if [ "$(wc -c < "$f" | tr -d ' ')" -le 524288 ]; then
        awk -v f="$f" '{ print f ": " $0 }' "$f"
      fi
    done)"
  fi
  SCAN_TEXT="$(printf '%s\n%s\n' "$ADDED_WITH_FILE" "$UNTRACKED_TEXT")"

  section "Отладочный мусор в добавленных строках"
  DEBUG_HITS="$(echo "$SCAN_TEXT" | grep -E -n \
    '(console\.(log|debug|trace)\(|\bdebugger\b|\bprint\(|\bbreakpoint\(\)|\bpdb\.set_trace|\bipdb|\bdd\(|\bvar_dump\(|\bdump\(|\.only\(|\bfit\(|\bfdescribe\(|\bxit\(|\bxdescribe\(|@pytest\.mark\.skip|\bit\.skip\(|\btest\.skip\(|eslint-disable|@ts-ignore|@ts-expect-error|# type: ignore|# noqa|\bTODO\b|\bFIXME\b|\bXXX\b|\bHACK\b|binding\.pry|byebug)' \
    | grep -v -E '^[0-9]+:[^:]*(\.md|\.mdx|\.txt|\.rst|CHANGELOG|LICENSE)[^:]*: ' )"
  if [ -n "$DEBUG_HITS" ]; then
    echo "$DEBUG_HITS" | sed 's/^[0-9]*://' | cut -c1-200 | capped
    note "просмотреть каждое: оставлять только осознанное логирование в стиле проекта"
  else
    note "нет"
  fi

  if [ "$DO_SECRETS" -eq 1 ]; then
    section "Возможные секреты в добавленных строках и неотслеживаемых файлах"
    SECRET_HITS="$(echo "$SCAN_TEXT" | grep -E -n \
      '(AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{36}|gho_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{50,}|glpat-[A-Za-z0-9_-]{20}|xox[abprs]-[A-Za-z0-9-]{10,}|sk-[A-Za-z0-9_-]{20,}|sk_live_[A-Za-z0-9]{16,}|rk_live_[A-Za-z0-9]{16,}|AIza[0-9A-Za-z_-]{35}|ya29\.[0-9A-Za-z_-]+|-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY|eyJ[A-Za-z0-9_-]{20,}\.eyJ[A-Za-z0-9_-]{20,}|(postgres|postgresql|mysql|mongodb(\+srv)?|redis|amqp)://[^:/@ ]+:[^@/ ]+@|(api[_-]?key|apikey|secret|token|passw(or)?d|client[_-]?secret|private[_-]?key|access[_-]?key)["'"'"']?[[:space:]]*[:=][[:space:]]*["'"'"'][A-Za-z0-9/+_=.-]{12,}["'"'"'])' \
      | grep -v -i -E '(example|placeholder|your[_-]?|dummy|changeme|<[a-z_]+>|xxx+|\*\*\*|process\.env\.|os\.environ|getenv|\$\{|import\.meta\.env|secrets\.|\.lock: |package-lock\.json: |yarn\.lock: |pnpm-lock\.yaml: |uv\.lock: |poetry\.lock: )' )"
    if [ -n "$SECRET_HITS" ]; then
      echo "$SECRET_HITS" | sed 's/^[0-9]*://' | cut -c1-200 | capped
      note "каждое совпадение смотреть глазами: настоящее ротировать, унести в env или secret store, вычистить из истории, если уже закоммичено"
    else
      note "совпадений нет (поиск по шаблонам; чувствительные места всё равно читать в diff)"
    fi
  fi

  section "Затронуты чувствительные места (по путям или ключевым словам в добавленных строках)"
  # Ключевые слова ограничены не-буквами, чтобы authoring не считалось auth, а schematic — schema.
  SENSITIVE_PATHS="$(echo "$CHANGED_ALL" | grep -E -i '((^|[^a-z])(auth|authn|authz|authenticat[a-z]*|authoriz[a-z]*|login|logout|session|sessions|password|passwd|permission|permissions|acl|rbac|policy|policies|token|tokens|oauth|jwt|crypto|cipher|payment|payments|billing|stripe|webhook|webhooks|upload|uploads|migration|migrations|schema|cors|csp|iam|terraform|helm|k8s|kubernetes|nginx)([^a-z]|$)|\.sql$|\.tf$|Dockerfile|docker-compose|\.github/workflows|\.gitlab-ci|\.htaccess)')"
  SENSITIVE_LINES="$(echo "$ADDED_WITH_FILE" | grep -E -i \
    '(\bexec\(|\bsystem\(|\bpopen\(|subprocess\.|shell=True|child_process|\beval\(|new Function\(|pickle\.|yaml\.load\(|marshal\.|unserialize\(|innerHTML|dangerouslySetInnerHTML|document\.write|\bRAW\b.*SQL|f"(SELECT|INSERT|UPDATE|DELETE)|"(SELECT|INSERT|UPDATE|DELETE) .*" *\+|\.format\(.*(SELECT|INSERT|UPDATE|DELETE)|Access-Control-Allow-Origin.*\*|verify\s*=\s*False|rejectUnauthorized:\s*false|InsecureSkipVerify|NODE_TLS_REJECT_UNAUTHORIZED|DEBUG\s*=\s*True|chmod 777|0o777|allow_all|AllowAll)' | cut -c1-200)"
  if [ -n "$SENSITIVE_PATHS" ]; then
    note "пути:"; echo "$SENSITIVE_PATHS" | sed 's/^/     /' | capped
  fi
  if [ -n "$SENSITIVE_LINES" ]; then
    note "строки:"; echo "$SENSITIVE_LINES" | sed 's/^/     /' | capped
  fi
  if [ -z "$SENSITIVE_PATHS" ] && [ -z "$SENSITIVE_LINES" ]; then
    note "не обнаружены"
  else
    note "имеет смысл прогнать скилл expert-security по этим файлам"
  fi

  # ------------------------------------------------------------------ docs
  section "Документация против изменений кода"
  CODE_CHANGED="$(echo "$CHANGED_ALL" | grep -v -E -i '(\.(md|mdx|rst|txt|adoc)$|^docs?/|^doc/|^\.agents/changelog/|LICENSE)' | grep -c . )"
  DOCS_CHANGED="$(echo "$CHANGED_ALL" | grep -E -i '(\.(md|mdx|rst|txt|adoc)$|^docs?/|^doc/|^\.agents/changelog/)' )"
  note "изменено файлов с кодом: $CODE_CHANGED"
  if [ -n "$DOCS_CHANGED" ]; then
    note "изменённая документация:"; echo "$DOCS_CHANGED" | sed 's/^/     /' | capped
  else
    note "изменённая документация: нет"
  fi
  note "документы в репозитории (проверить, что каждый всё ещё говорит правду):"
  DOCS_FOUND=0
  for d in README.md README.rst README CHANGES.md HISTORY.md AGENTS.md CLAUDE.md CONTRIBUTING.md ARCHITECTURE.md docs doc openapi.yaml openapi.json schema.graphql; do
    if [ -e "$d" ]; then note "  $d"; DOCS_FOUND=1; fi
  done
  [ "$DOCS_FOUND" -eq 0 ] && note "  не найдены"
  JOURNALS=""
  for j in .agents/changelog .agents/decisions .agents/learnings; do
    [ -e "$j" ] && JOURNALS="$JOURNALS $j"
  done
  if [ -n "$JOURNALS" ]; then
    note "журналы проекта (обязательны к заполнению, см. шаг 5):$JOURNALS"
    JOURNALS_CHANGED="$(echo "$CHANGED_ALL" | grep -E '^\.agents/(changelog|decisions|learnings)/' || true)"
    if [ -n "$JOURNALS_CHANGED" ]; then
      note "  записи в diff:"; echo "$JOURNALS_CHANGED" | sed 's/^/     /' | capped
    elif [ "$CODE_CHANGED" -gt 0 ]; then
      note "  записей в diff нет, а код менялся: решить по каждому журналу, запись или явное «нет»"
    fi
    if [ -d .agents/rules ]; then
      note "  правила:"
      for rule in .agents/rules/*.md; do
        [ -f "$rule" ] || continue
        note "    $(printf '%q' "$rule")"
      done
    fi
  fi
  PLANS="$(
    shopt -s nocasematch
    PLAN_PATTERN='^(plan|plans|todo|roadmap)(\.md|/|$)'
    for plan in ./*; do
      [ -e "$plan" ] || continue
      name="${plan#./}"
      if [[ "$name" =~ $PLAN_PATTERN ]]; then printf '%q\n' "$name"; fi
    done
    shopt -u nocasematch
    for plan in .claude/plans/* .agents/plans/* docs/plans/*; do
      [ -e "$plan" ] || continue
      if [ "$plan" = .agents/plans/AGENTS.md ] || [ "$plan" = .agents/plans/CLAUDE.md ]; then continue; fi
      printf '%q\n' "$plan"
    done
  )"
  if [ -n "$PLANS" ]; then
    note "файлы, похожие на план (сверить статус при scope «План»):"; printf '%s\n' "$PLANS" | sed 's/^/     /' | capped
  fi
  ENV_EXAMPLES="$(for example in .env.example .env.sample .env.template env.example; do
    [ -f "$example" ] && printf '%s\n' "$example"
  done)"
  if [ -n "$ENV_EXAMPLES" ] && echo "$ADDED_WITH_FILE" | grep -q -E '(process\.env\.|os\.environ|getenv\(|import\.meta\.env\.)[A-Z_]+'; then
    note "в diff читаются новые env-переменные; проверить, что они перечислены в: $(echo "$ENV_EXAMPLES" | tr '\n' ' ')"
  fi
fi

# -------------------------------------------------------- quality gates
section "Найденные команды quality gates"
GATES=0
if [ -f package.json ]; then
  PM="npm run"
  [ -f pnpm-lock.yaml ] && PM="pnpm"
  [ -f yarn.lock ] && PM="yarn"
  if [ -f bun.lockb ] || [ -f bun.lock ]; then PM="bun run"; fi
  for s in lint typecheck type-check check test test:unit build format:check fmt:check; do
    if grep -q -E "\"$s\"[[:space:]]*:" package.json 2>/dev/null; then note "$PM $s"; GATES=1; fi
  done
fi
if [ -f pyproject.toml ]; then
  grep -q -E '^\[tool\.ruff' pyproject.toml && { note "uv run ruff check . && uv run ruff format --check ."; GATES=1; }
  grep -q -E '^\[tool\.(ty|mypy|pyright)' pyproject.toml && { note "uv run ty check  (или mypy / pyright по pyproject)"; GATES=1; }
  { grep -q -E '^\[tool\.pytest' pyproject.toml || [ -d tests ]; } && { note "uv run pytest"; GATES=1; }
fi
[ -f Cargo.toml ] && { note "cargo fmt --check && cargo clippy --all-targets && cargo test"; GATES=1; }
[ -f go.mod ] && { note "gofmt -l . && go vet ./... && go test ./..."; GATES=1; }
if [ -f Makefile ]; then
  TARGETS="$(grep -E '^[a-zA-Z_-]+:' Makefile | cut -d: -f1 | grep -E '^(lint|test|check|typecheck|fmt|format|build|ci)$' | tr '\n' ' ')"
  [ -n "$TARGETS" ] && { note "make: $TARGETS"; GATES=1; }
fi
if [ -f justfile ] || [ -f Justfile ]; then note "just --list"; GATES=1; fi
[ -f composer.json ] && { note "composer test / vendor/bin/phpstan / vendor/bin/pint --test (см. scripts в composer.json)"; GATES=1; }
[ -d .github/workflows ] && note "CI в .github/workflows: повторить то, что гоняет CI"
[ "$GATES" -eq 0 ] && note "ничего не найдено; смотреть документацию проекта или конфиг CI"

# ------------------------------------------------------------ processes
if [ "$DO_PROCS" -eq 1 ]; then
  section "Процессы, которые могут относиться к проекту"
  PROC_PATTERN='(vite|next dev|next-server|webpack|nodemon|tsx watch|ts-node-dev|tsc (-w|--watch)|turbo|storybook|astro dev|nuxt dev|remix|uvicorn|gunicorn|flask run|manage\.py runserver|fastapi|celery|pytest.*(-f|--looponfail)|jest.*--watch|vitest|playwright|chromium.*--remote-debugging|ngrok|cloudflared|localtunnel|docker compose|docker-compose|php artisan serve|rails s|puma|cargo watch|air$|watchexec|http\.server|live-server|serve )'
  if command -v pgrep >/dev/null 2>&1; then
    PROC_PIDS="$(pgrep -if -d ',' "$PROC_PATTERN" 2>/dev/null)"
    PROC_STATUS=$?
    if [ "$PROC_STATUS" -eq 0 ]; then
      PROCS="$(ps -p "$PROC_PIDS" -o pid= -o etime= -o command= 2>/dev/null | awk 'index($0, "wrap-up-check") == 0')"
      if [ -n "$PROCS" ]; then
        printf '%s\n' "$PROCS" | cut -c1-160 | capped
        note "остановить то, что запускал сам; остальное перечислить в отчёте, а не убивать"
      else
        note "выбранные процессы завершились или относятся к самой проверке"
      fi
    elif [ "$PROC_STATUS" -eq 1 ]; then
      note "совпадений нет"
    else
      note "не удалось получить список процессов (pgrep: $PROC_STATUS)"
    fi
  else
    note "pgrep недоступен: поиск процессов пропущен"
  fi
  if [ "$IN_GIT" -eq 1 ] && command -v lsof >/dev/null 2>&1; then
    REPO_PROCS="$(lsof -a -d cwd -Fpc +D "$ROOT" 2>/dev/null | awk '/^p/{pid=substr($0,2)} /^c/{print pid" "substr($0,2)}' | grep -v -E ' (bash|zsh|sh|lsof|wrap-up-check|Finder|mdworker|head|ghead|tail|gtail|awk|gawk|grep|ggrep|sed|gsed|git|ps|cut|gcut|sort|gsort|tr|gtr|find|gfind|wc|gwc|xargs)$' )"
    if [ -n "$REPO_PROCS" ]; then
      note "процессы с cwd внутри репозитория (pid, команда):"
      echo "$REPO_PROCS" | sed 's/^/     /' | capped
    fi
  fi

  section "Слушающие TCP-порты (типичный dev-диапазон)"
  if command -v lsof >/dev/null 2>&1; then
    PORTS="$(lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $9, $1, $2}' | grep -E ':(3[0-9]{3}|4[0-9]{3}|5[0-9]{3}|6[0-9]{3}|8[0-9]{3}|9[0-9]{3}|1[0-9]{4})' | sort -u)"
    if [ -n "$PORTS" ]; then echo "$PORTS" | sed 's/^/   /' | capped; else note "в диапазоне 3000-19999 ничего"; fi
  else
    note "lsof недоступен"
  fi

  if command -v docker >/dev/null 2>&1; then
    section "Запущенные Docker-контейнеры"
    DC="$(docker ps --format '{{.Names}}  {{.Image}}  {{.Status}}' 2>/dev/null)"
    if [ -n "$DC" ]; then echo "$DC" | sed 's/^/   /' | capped; else note "нет (или демон docker не запущен)"; fi
  fi
fi

# ------------------------------------------------------------ temp files
section "Временные и scratch-файлы"
SCRATCH_HITS=""
for d in /private/tmp/claude-501 /tmp; do
  [ -d "$d" ] || continue
  RECENT="$(find "$d" -maxdepth 4 -type f -mmin -720 2>/dev/null | grep -v -E '(\.sock$|/com\.apple|/powerlog|/tmux-|\.lock$)' | head -n 20)"
  [ -n "$RECENT" ] && SCRATCH_HITS="$SCRATCH_HITS
$RECENT"
done
if [ -n "$SCRATCH_HITS" ]; then
  echo "$SCRATCH_HITS" | grep -v '^$' | sed 's/^/   /' | capped
  note "(файлы, изменённые за последние 12 ч в scratch-директориях; удалить своё)"
else
  note "ничего свежего в scratch-директориях"
fi
if [ "$IN_GIT" -eq 1 ]; then
  REPO_TMP="$(find . -path ./node_modules -prune -o -path ./.git -prune -o -type f \( -name '*.orig' -o -name '*.rej' -o -name '*.bak' -o -name '*~' -o -name '.DS_Store' -o -name '*.swp' \) -print 2>/dev/null | head -n 20)"
  if [ -n "$REPO_TMP" ]; then
    note "внутри репозитория:"
    printf '     %s\n' "${REPO_TMP//$'\n'/$'\n     '}"
  fi
else
  for a in .playwright-cli .playwright-mcp playwright-report test-results; do
    [ -e "$a" ] && note "артефакт инструмента в cwd: $a/ ($(du -sh "$a" 2>/dev/null | cut -f1))"
  done
fi

section "Готово"
note "это только факты; решения и подчистка — шаги wrap-up в SKILL.md"
