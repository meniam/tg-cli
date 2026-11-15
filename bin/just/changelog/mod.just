# Module `just changelog`: records of changes in .agents/changelog/ - list, add, archive.
# Recipes run from the repository root: the paths below are relative to it.

set working-directory := '../../..'

# Show module help
[default]
[arg("help", long="help", short="h", value="1")]
help help="":
    @bash bin/lib/help.sh bin/just/changelog/help.txt

# List records, newest first by day: --days, --all, --type, --breaking
[positional-arguments]
list *args:
    #!/usr/bin/env bash
    set -euo pipefail
    [[ $# -ne 1 || ( $1 != -h && $1 != --help ) ]] || exec bash bin/lib/recipe-help.sh changelog list
    exec uv run --quiet bin/changelog.py list "$@"

# Add a record: type, text up to 200 characters, --breaking, --decision, --slug
[positional-arguments]
add *args:
    #!/usr/bin/env bash
    set -euo pipefail
    [[ $# -ne 1 || ( $1 != -h && $1 != --help ) ]] || exec bash bin/lib/recipe-help.sh changelog add
    exec uv run --quiet bin/changelog.py add "$@"

# Move records of past months into YYYY-MM/ subfolders; --dry-run only shows them
[arg("help", long="help", short="h", value="1")]
[arg("dry", long="dry-run", value="--dry-run")]
archive help="" dry="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh changelog archive
    exec uv run --quiet bin/changelog.py archive {{ dry }}
