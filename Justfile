golangci_lint_version := "v2.13.2"
golangci_lint := "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@" + golangci_lint_version
actionlint_version := "v1.7.12"
actionlint := "go run github.com/rhysd/actionlint/cmd/actionlint@" + actionlint_version
goreleaser_version := "v2.13.3"

_:
    @just help

# List available commands
help:
    @just --list

# Run tests
test:
    go test ./...

# Run tests that require real local system boundaries
integration:
    #!/usr/bin/env sh
    set -eu
    if ! command -v tmux >/dev/null 2>&1; then
        echo "Error: tmux is required to run integration tests. Install tmux and retry." >&2
        exit 1
    fi
    go test -count=1 -v -tags=integration ./internal/install ./internal/observer ./internal/service ./internal/systemtest ./internal/testtmux ./pkg/tmux

# Test release detection, state transitions, and workflow wiring
compatibility-tests:
    go test ./internal/tools/compatibility

# Exercise one installed current harness against an isolated local provider
compatibility harness:
    AHT_COMPAT_HARNESS="{{ harness }}" go test -count=1 -v -tags=compatibility ./internal/hostcompat -run '^TestCurrentHarnessLifecycle$' -timeout 10m

# Validate built release artifacts and optional published copies
artifacts artifact_dir="dist" published_dir="":
    AHT_ARTIFACT_DIR="{{ artifact_dir }}" AHT_PUBLISHED_ARTIFACT_DIR="{{ published_dir }}" go test -count=1 -tags=integration ./internal/systemtest -run '^TestReleaseArtifacts$'
# Run tests and display coverage
coverage:
    #!/usr/bin/env sh
    set -e
    coverage_file=$(mktemp)
    trap 'rm -f "$coverage_file"' EXIT
    go test -coverprofile="$coverage_file" ./...
    go tool cover -func="$coverage_file"

# Run tests with the race detector
race:
    go test -race ./...

# Tidy dependencies
tidy:
    go mod tidy

# Apply automatic fixes
fix:
    {{ golangci_lint }} run --fix

# Format Go source files
format:
    {{ golangci_lint }} fmt

# Check code for lint issues
lint:
    {{ golangci_lint }} run

# Scan reachable dependencies for known vulnerabilities
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
# Run all required non-mutating verification
check: lint test race integration
    {{ golangci_lint }} fmt --diff
    go mod tidy -diff
    go build -o /dev/null .
    just --fmt --check
    {{ actionlint }} .github/workflows/*.yml

# Build the project
build:
    go build -o aht .

# Install the binary and refresh existing integrations and background tracking
install:
    #!/usr/bin/env sh
    set -eu
    go install .
    aht_install_dir=$(go env GOBIN)
    if [ -z "$aht_install_dir" ]; then
        aht_go_path=$(go env GOPATH)
        aht_install_dir="${aht_go_path%%:*}/bin"
    fi
    "$aht_install_dir/aht" manage upgrade

# Install only the executable
install-binary:
    go install .

# Remove build artifacts
clean:
    rm -rf aht aht.exe dist/

# Build with local development version metadata
build-dev:
    go build -ldflags "-X github.com/zigai/aht/internal/cli.version=dev -X github.com/zigai/aht/internal/cli.commit=$(git rev-parse --short HEAD) -X github.com/zigai/aht/internal/cli.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o aht .

_goreleaser-version-check:
    #!/usr/bin/env sh
    set -eu
    expected='{{ goreleaser_version }}'
    expected=${expected#v}
    actual=$(goreleaser --version | sed -n 's/^GitVersion:[[:space:]]*//p')
    if [ "$actual" != "$expected" ]; then
        echo "Error: GoReleaser version mismatch: expected $expected, got ${actual:-<missing>}." >&2
        exit 1
    fi

# Build a release snapshot without publishing
snapshot: _goreleaser-version-check
    goreleaser release --snapshot --clean

# Build and upload a draft release
release-draft: _goreleaser-version-check
    goreleaser release --clean

_release-check:
    #!/usr/bin/env sh
    set -e
    if [ -n "$(git status --porcelain)" ]; then
        echo "Error: uncommitted changes. Commit or stash first." >&2
        exit 1
    fi
    branch=$(git branch --show-current)
    if [ "$branch" != "master" ]; then
        echo "Error: not on master branch (on $branch)" >&2
        exit 1
    fi
    git fetch origin master --tags
    local_head=$(git rev-parse HEAD)
    remote_head=$(git rev-parse origin/master)
    if [ "$local_head" != "$remote_head" ]; then
        echo "Error: local master differs from origin/master. Pull or push first." >&2
        exit 1
    fi
    latest_tag=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [ -n "$latest_tag" ]; then
        tag_commit=$(git rev-parse "$latest_tag"^{})
        if [ "$local_head" = "$tag_commit" ]; then
            echo "Error: HEAD is already tagged as $latest_tag. Make new commits first." >&2
            exit 1
        fi
    fi

# Release a new patch version
release-patch: _release-check _goreleaser-version-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
    patch=$(echo "$latest" | sed 's/v//' | cut -d. -f3)
    new="v${major}.${minor}.$((patch + 1))"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

# Release a new minor version
release-minor: _release-check _goreleaser-version-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
    new="v${major}.$((minor + 1)).0"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

# Release a new major version
release-major: _release-check _goreleaser-version-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    new="v$((major + 1)).0.0"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

alias release := release-patch

alias cov := coverage
alias fmt := format
