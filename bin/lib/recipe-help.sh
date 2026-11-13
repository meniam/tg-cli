#!/usr/bin/env bash
# Help for one recipe: `just <recipe> -h`. The text lives in bin/just/root/<recipe>.txt,
# the banner with the command is built here, rendering is shared with help.sh.
set -euo pipefail

recipe="${1:?Pass the recipe name}"
dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
file="$dir/bin/just/root/$recipe.txt"
command="just $recipe"
title="tg-cli · $recipe"

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
