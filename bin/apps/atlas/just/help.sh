#!/usr/bin/env bash
# Оформление текстовой справки; перенаправление и NO_COLOR сохраняют plain text.
set -euo pipefail

main() {
    local help_file="${1:?Pass a help file}"
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
        # Баннер: рамка ярко-белая, «Atlas» ярко-жёлтым, раздел после « · » розовым.
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
        # Команда без just, как её набирают в шелле: целиком голубым.
        elif [[ "$indent" == '    ' && ( "$text" == bin/* || "$text" == uv\ * ) ]]; then
            printf '    %s%s%s\n' "$cyan" "$text" "$reset"
        elif [[ "$text" == just\ * ]]; then
            command="${text%%  *}"
            description="${text#"$command"}"
            printf '    %s%s%s%s\n' "$cyan" "$command" "$reset" "$description"
        elif [[ "$text" =~ ^[0-9]+\.\  ]]; then
            printf '    %s\n' "$text"
        # Шесть пробелов в исходнике обозначают пояснение под командой.
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
