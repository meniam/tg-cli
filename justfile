# Single entry point of tg-cli. Help: `just`; every recipe accepts `-h` or `--help`.
#
# Layout: help texts live in bin/just/ (root help.txt and <recipe>.txt in
# bin/just/root/; a module has its own folder bin/just/<module>/ with mod.just,
# help.txt and <recipe>.txt), rendered by bin/lib/help.sh; recipe help is assembled
# by bin/lib/recipe-help.sh. When you change a command, update its help text too.
#
# Install: brew install just. The changelog and atlas modules need uv: brew install uv

# Changelog: records of changes visible outside the code
mod changelog 'bin/just/changelog/mod.just'

# Atlas: code graph and docs index for AI agents (a copy of the build, do not edit)
mod atlas 'bin/apps/atlas/justfile'

# Show help
[default]
[arg("help", long="help", short="h", value="1")]
help help="":
    @bash bin/lib/help.sh bin/just/help.txt

# Build the binary into dist/tg
[group('build')]
[arg("help", long="help", short="h", value="1")]
build help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh build
    set -x
    go build -o dist/tg ./cmd/tg-cli

# Build, install into ~/.local/bin/tg and restart the daemon
[group('build')]
[arg("help", long="help", short="h", value="1")]
install help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh install
    set -x
    go build -o dist/tg ./cmd/tg-cli
    # Exit code 3 means the daemon was not running: nothing to stop.
    dist/tg daemon stop || [[ $? -eq 3 ]]
    mkdir -p "$HOME/.local/bin"
    install -m 755 dist/tg "$HOME/.local/bin/tg"
    "$HOME/.local/bin/tg" daemon start

# Run the tool without building: just run <arguments>
[group('build')]
[positional-arguments]
run *args:
    #!/usr/bin/env bash
    set -euo pipefail
    [[ $# -ne 1 || ( $1 != -h && $1 != --help ) ]] || exec bash bin/lib/recipe-help.sh run
    set -x
    go run ./cmd/tg-cli "$@"

# Format the code with gofmt
[group('checks')]
[arg("help", long="help", short="h", value="1")]
fmt help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh fmt
    set -x
    gofmt -w cmd internal

# Static analysis with go vet
[group('checks')]
[arg("help", long="help", short="h", value="1")]
vet help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh vet
    set -x
    go vet ./...

# Run the tests
[group('checks')]
[arg("help", long="help", short="h", value="1")]
test help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh test
    set -x
    go test ./...

# Format check, vet and tests in a row, as before a commit
[group('checks')]
[arg("help", long="help", short="h", value="1")]
check help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh check
    unformatted="$(gofmt -l cmd internal)"
    if [[ -n "$unformatted" ]]; then
        printf 'Not formatted (run just fmt):\n%s\n' "$unformatted" >&2
        exit 1
    fi
    set -x
    go vet ./...
    go test ./...

# Rebuild generated CLI documentation tables
[group('maintenance')]
[arg("help", long="help", short="h", value="1")]
docs help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh docs
    set -x
    go test ./internal/cli -run TestDocsGenerated -update-docs

# Tidy go.mod and go.sum
[group('maintenance')]
[arg("help", long="help", short="h", value="1")]
tidy help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh tidy
    set -x
    go mod tidy

# Remove build output from dist/
[group('maintenance')]
[arg("help", long="help", short="h", value="1")]
clean help="":
    #!/usr/bin/env bash
    set -euo pipefail
    [[ -z "{{ help }}" ]] || exec bash bin/lib/recipe-help.sh clean
    set -x
    rm -rf dist
