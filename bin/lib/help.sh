#!/usr/bin/env bash
# Renders a help text file; redirected output and NO_COLOR keep it plain text.
set -euo pipefail

main() {
    local help_file="${1:?Pass the help file path}"
    local line text indent command description
    local bold='' cyan='' frame='' banner='' accent='' reset=''
    local inner left right

    if [[ -t 1 && -z ${NO_COLOR:-} && ${TERM:-dumb} != dumb ]]; then
        bold=$'\033[1;97m'
        cyan=$'\033[36m'
        frame=$'\033[1;97m'
        banner=$'\033[1;93m'
        accent=$'\033[1;95m'
        reset=$'\033[0m'
    fi

    printf '\n'
    while IFS= read -r line || [[ -n "$line" ]]; do
        indent="${line%%[! ]*}"
        text="${line#"$indent"}"
        if [[ -z "$text" ]]; then
            printf '\n'
        # Banner: bright white frame, product name bright yellow, section after " · " pink.
        elif [[ -z "$indent" && "$text" == │*│ ]]; then
            inner="${text#│}"
            inner="${inner%│}"
            if [[ "$inner" == *' · '* ]]; then
                left="${inner%% · *}"
                right="${inner#* · }"
                printf '  %s│%s%s · %s%s%s│%s\n' "$frame" "$banner" "$left" "$accent" "$right" "$frame" "$reset"
            else
                printf '  %s│%s%s%s│%s\n' "$frame" "$banner" "$inner" "$frame" "$reset"
            fi
        elif [[ -z "$indent" && "$text" == [╭╰]* ]]; then
            printf '  %s%s%s\n' "$frame" "$text" "$reset"
        # Snippet command as typed in a container: all cyan.
        elif [[ "$indent" == '    ' && "$text" == bin/* ]]; then
            printf '    %s%s%s\n' "$cyan" "$text" "$reset"
        elif [[ "$text" == just\ * ]]; then
            command="${text%%  *}"
            description="${text#"$command"}"
            printf '    %s%s%s%s\n' "$cyan" "$command" "$reset" "$description"
        elif [[ "$text" =~ ^[0-9]+\.\  ]]; then
            printf '    %s\n' "$text"
        # Six leading spaces in the source mark an explanation under a command.
        elif [[ "$indent" == '      ' ]]; then
            printf '      %s\n' "$text"
        elif [[ -n "$text" && ( -z "$indent" || ( "$indent" == '  ' && "$text" != *:* && "$text" != *. ) ) ]]; then
            printf '  %s%s%s\n' "$bold" "$text" "$reset"
        else
            printf '  %s\n' "$text"
        fi
    done < "$help_file"
    printf '\n'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
