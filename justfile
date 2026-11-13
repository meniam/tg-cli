# Single entry point of tg-cli. Help: `just`; every recipe accepts `-h` or `--help`.
#
# Layout: help texts live in bin/just/ (root help.txt and <recipe>.txt in
# bin/just/root/), rendered by bin/lib/help.sh; recipe help is assembled by
# bin/lib/recipe-help.sh. When you change a command, update its help text too.
#
# Install: brew install just

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
