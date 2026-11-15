#!/usr/bin/env bash
# Help for one recipe: `just <recipe> -h` or `just <module> <recipe> -h`. The text lives in
# bin/just/<module>/<recipe>.txt (root recipes: bin/just/root/), the banner with the command
# is built here, rendering is shared with help.sh.
set -euo pipefail

if [[ $# -ge 2 ]]; then
    module="$1"
    recipe="$2"
    command="just $module $recipe"
    title="tg-cli · $module $recipe"
else
    module=root
    recipe="${1:?Pass the recipe name}"
    command="just $recipe"
    title="tg-cli · $recipe"
fi
dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
file="$dir/bin/just/$module/$recipe.txt"

if [[ ! -f "$file" ]]; then
    printf 'No help for %s: expected %s\n' "$command" "${file#"$dir/"}" >&2
    exit 1
fi

# The width is computed from the ASCII command: ${#title} with the middle dot depends on the locale.
# "tg-cli · " is 9 characters instead of "just " (5), plus two spaces at each border.
width=$(( ${#command} - 5 + 9 + 4 ))
border="$(printf '─%.0s' $(seq 1 "$width"))"
{
    printf '╭%s╮\n│  %s  │\n╰%s╯\n\n' "$border" "$title" "$border"
    cat "$file"
} | bash "$dir/bin/lib/help.sh" /dev/stdin
