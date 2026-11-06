INTEGRATION_TESTS := "^(TestChangesMetadataFailurePublishesNoSelection|TestChangesPinLastSuccessfulRelease|TestCleanupReconcilesCreationWhoseResponseWasLost|TestCurrentHarnessLifecycle|TestDetachedHistoryCommandReturnsWhileChildRunsWithoutTerminal|TestDetectionRechecksChangedBinaryRevision|TestDraftRetriesReuseOwnershipPreserveNotesAndCleanUp|TestForeignAndPublishedReleasesCannotBeDeleted|TestHTTPFailuresAndLimits|TestIssueSyncCommentsAndClosesResolvedIssue|TestIssueSyncIgnoresIssuesAndPullRequestsItDoesNotOwn|TestIssueSyncLeavesUnchangedIssueAndUpdatesStaleOne|TestIssueSyncOpensOneIssueForUntrackedRegression|TestIssuesCommandReadsFinishReport|TestListPanesIgnoresSocketsThatAreNotLiveSessions|TestLostPublicationResponseAndRerunsPreserveThePublishedRelease|TestMalformedArtifactListingCannotResetBaseline|TestMissingStateAndAPIFailureAreDifferent|TestMissingStateStartsEmpty|TestOnlyReadsRetryTransientFailures|TestPaginateFollowsNextLinks|TestPaginateRejectsForeignNextLink|TestPaginateRequiresWrappedField|TestPrereleaseTagsCreatePrereleases|TestPublicationFailureRemainsFailureAndDraftIsRemovable|TestPublishRefusesReleaseOwnedByAnotherRun|TestReadRecoversAfterTransientFailure|TestRedirectDoesNotForwardToken|TestRegressionsListTrackedFailuresForReleaseChecks|TestRegressionsRequireReadableState|TestReleaseCancelsWhileWaitingForCI|TestReleaseGateFailsOnLaterPageErrors|TestReleaseGateFollowsPagination|TestReleasePropagatesGateAPIErrors|TestReleaseRequiresNewestExactCommitCIAndEveryGate|TestReleaseRequiresTagAndCommitIdentity|TestReleaseSources|TestReleaseWaitsForNewestCIToComplete|TestReleaseWithoutAdapterIsNotChecked|TestRequestsAuthenticateAndEncodeJSON|TestRestoreOnlyTrustedDefaultBranchState|TestStatusErrorsCarryStatusAndMessage|TestSuccessfulStateServerEscapesJSON|TestSupportedCapDoesNotHideLatestRelease|TestUnauthorizedLookupIsNotAnAbsentDraft|TestUnknownCommandShowsUsage|TestWeeklyChangesSkipReleaseState|TestWorkflowOutputsAndStateRoundTrip)$"
GOLANGCI_LINT_VERSION := "v2.15.0"
GOLANGCI_LINT := 'GOFLAGS="${GOFLAGS:-} -trimpath=false" GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/golangci-lint}/source-positions" ' + env("AHT_GOLANGCI_LINT", "golangci-lint")
ACTIONLINT_VERSION := "v1.7.12"
ACTIONLINT := "go run github.com/rhysd/actionlint/cmd/actionlint@" + ACTIONLINT_VERSION
APIDIFF_VERSION := "v0.0.0-20260908205506-85c1c2202aba"
APIDIFF := "go run golang.org/x/exp/cmd/apidiff@" + APIDIFF_VERSION

# Run fast Go unit tests without live fixtures
[positional-arguments]
test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if (( $# )); then go test -skip '{{ INTEGRATION_TESTS }}' "$@"; else go test -skip '{{ INTEGRATION_TESTS }}' ./...; fi

# Run extended simulations and bounded fuzz exploration
explore fuzztime="30s":
    #!/usr/bin/env bash
    set -euo pipefail
    AHT_EXTENDED_PARITY=1 go test -count=1 ./pkg/registry ./internal/observer ./internal/harness/catalog
    for package in ./internal/agentstate ./internal/processinfo ./internal/harness/catalog ./pkg/tmux ./pkg/registry ./internal/harness/transcript ./internal/config ./internal/cli; do
        targets=$(go test "$package" -list '^Fuzz')
        while IFS= read -r target; do
            [[ "$target" =~ ^Fuzz[A-Za-z0-9_]+$ ]] || continue
            go test "$package" -run '^$' -fuzz "^${target}$" -fuzztime '{{ fuzztime }}' -parallel 2 -timeout 0
        done <<< "$targets"
    done

# Run tests that require real local system boundaries
integration:
    #!/usr/bin/env sh
    set -eu
    if ! command -v tmux >/dev/null 2>&1; then
        echo "Error: tmux is required to run integration tests. Install tmux and retry." >&2
        exit 1
    fi
    go test -count=1 -tags=integration ./internal/install ./internal/observer ./internal/service ./test/systemtest ./internal/testtmux ./pkg/tmux
    go test -run '{{ INTEGRATION_TESTS }}' ./...

# Test CI tooling, release detection, state transitions, and workflow wiring
compatibility-tests:
    go test ./internal/tools/...
    go test -count=1 -race -tags=compatibility ./test/hostcompat -skip '^TestCurrentHarnessLifecycle$'

# Exercise one installed current harness against an isolated local provider
compatibility harness:
    AHT_COMPAT_HARNESS="{{ harness }}" go test -count=1 -v -tags=compatibility ./test/hostcompat ./test/interactive -run '^TestCurrentHarness(Lifecycle|Interactive)$' -timeout 15m

# Validate built release artifacts and optional published copies
artifacts artifact_dir="dist" published_dir="":
    AHT_ARTIFACT_DIR="{{ artifact_dir }}" AHT_PUBLISHED_ARTIFACT_DIR="{{ published_dir }}" go test -count=1 -tags=integration ./test/systemtest -run '^TestReleaseArtifacts$'

# Measure all local test suites and print coverage
coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf coverage
    mkdir -p coverage
    coverage_package=$(awk '$1 == "module" {gsub(/"/, "", $2); print $2; exit}' go.mod)
    go test -coverpkg=./... ./... -args -test.gocoverdir="$PWD/coverage"
    go test -coverpkg=./... -count=1 -tags=integration ./internal/install ./internal/observer ./internal/service ./test/systemtest ./internal/testtmux ./pkg/tmux -args -test.gocoverdir="$PWD/coverage"
    go tool covdata percent -i coverage -pkg "$coverage_package/..."
    go tool covdata textfmt -i coverage -o coverage/profile.txt -pkg "$coverage_package/..."
    go tool cover -func coverage/profile.txt | awk '$1 == "total:"'

# Run tests with the race detector
race:
    go test -race ./...

# Apply safe lint autofixes, then format every supported file type
fix: && format
    {{ GOLANGCI_LINT }} run --fix

# Format every supported first-party file type
format:
    @{{ GOLANGCI_LINT }} run --fix --enable-only whitespace --issues-exit-code=0
    @{{ GOLANGCI_LINT }} fmt
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | grep -v /testdata/ | xargs -r -d '\n' shfmt -w
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do just --justfile "$file" --fmt; done
    @just markdown fmt --exclude internal/install/testdata

# Run every configured linter on the whole project, stopping on failure
lint:
    @{{ GOLANGCI_LINT }} run

# Print incompatible public API changes from a base revision to the working tree
apidiff base="origin/master":
    #!/usr/bin/env bash
    set -euo pipefail
    scratch=$(mktemp -d "${TMPDIR:-$HOME/.cache}/aht-apidiff.XXXXXX")
    trap 'rm -rf "$scratch"' EXIT
    mkdir "$scratch/base"
    git archive '{{ base }}' | tar -x -C "$scratch/base"
    (cd "$scratch/base" && {{ APIDIFF }} -m -w "$scratch/base.api" "$(go list -m)")
    {{ APIDIFF }} -m -w "$scratch/current.api" "$(go list -m)"
    {{ APIDIFF }} -m -incompatible "$scratch/base.api" "$scratch/current.api"

# Scan reachable dependencies for known vulnerabilities
audit:
    go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

# Run the commit checks
check: (run-checks "formatcheck" "lockcheck" "workflow-check" "justfile-check" "lint" "build-check" "fakeprovider-build-check" "test")

# Verify formatting, module hygiene, workflow syntax, and builds
source-checks: (run-checks "formatcheck" "lockcheck" "workflow-check" "justfile-check" "build-check" "fakeprovider-build-check")

# Build the project with version info
build:
    go build -ldflags "-X github.com/zigai/aht/v2/internal/cli.version=dev -X github.com/zigai/aht/v2/internal/cli.commit=$(git rev-parse --short HEAD) -X github.com/zigai/aht/v2/internal/cli.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o bin/aht .

# Install the binary and refresh existing integrations and background tracking
install: && (report-install home_directory() / ".local/bin/aht")
    #!/usr/bin/env sh
    set -eu
    GOBIN="$HOME/.local/bin" go install -trimpath -ldflags='-s -w' .
    "$HOME/.local/bin/aht" manage upgrade

# Install only the executable
install-binary: && (report-install home_directory() / ".local/bin/aht")
    GOBIN="$HOME/.local/bin" go install -trimpath -ldflags='-s -w' .

# Install the fake model provider command
install-fakeprovider: && (report-install home_directory() / ".local/bin/fakeprovider")
    GOBIN="$HOME/.local/bin" go install -trimpath -ldflags='-s -w' ./cmd/fakeprovider

# Remove generated output and caches
clean: (remove "bin" "aht" "aht.exe" "dist" "coverage.out" "*.test" "*.prof" ".ruff_cache" "coverage")

# Check the same files and tools without writing
formatcheck:
    @{{ GOLANGCI_LINT }} run --enable-only whitespace
    @{{ GOLANGCI_LINT }} fmt --diff
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | grep -v /testdata/ | xargs -r -d '\n' shfmt -d
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do just --justfile "$file" --fmt --check; done
    @just markdown check --exclude internal/install/testdata

# Run the workflow check checks
workflow-check:
    {{ ACTIONLINT }} .github/workflows/*.yml

# Run the justfile check checks
justfile-check:
    sed ':a;N;$!ba;s/\[private\]\n\[positional-arguments\]/[positional-arguments]\n[private]/g' Justfile | just --justfile /dev/stdin --fmt --check

# Compile the project without installing it
build-check:
    go build -o /dev/null .

# Run the fakeprovider build check checks
fakeprovider-build-check:
    go build -o /dev/null ./cmd/fakeprovider

# Run the commit checks and slow suites
checkfull: (run-checks "formatcheck" "lockcheck" "workflow-check" "justfile-check" "lint" "build-check" "fakeprovider-build-check" "test" "race" "integration" "compatibility-tests" "explore")

# Synchronize dependency locks without upgrading dependencies
[no-exit-message]
lock:
    @go mod tidy

# Verify dependency manifests and locks without changing files
[no-exit-message]
lockcheck:
    @go mod tidy -diff

# Install repository dependencies
setup:
    @go mod download

# List available recipes
[default]
help:
    @just --list

alias cov := coverage
alias fmt := format

[positional-arguments]
[private]
remove +paths:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob globstar
    size() { LC_ALL=C numfmt --from-unit=1024 --to=iec-i --suffix=B --format=%.1f "$1"; }
    rows=() total=0
    for pattern in "$@"; do
        found=()
        for path in $pattern; do
            if [[ -e $path || -L $path ]]; then found+=("$path"); fi
        done
        if (( ${#found[@]} == 0 )); then continue; fi
        kib=$(du -sck -- "${found[@]}" | tail -n1 | cut -f1)
        rm -rf -- "${found[@]}"
        label=$pattern
        if (( ${#found[@]} > 1 )); then label+=" (${#found[@]})"; fi
        rows+=("removed  $label|$(size "$kib")")
        total=$(( total + kib ))
    done
    if [[ -n ${CLEAN_TOTAL_FILE:-} ]]; then echo "$total" >> "$CLEAN_TOTAL_FILE"; fi
    if (( ${#rows[@]} == 0 )); then echo "nothing to clean"; exit 0; fi
    printf '%s\n' "${rows[@]}" "total freed|$(size "$total")" | column -t -s '|' -R 2

[positional-arguments]
[private]
report-install +paths:
    #!/usr/bin/env bash
    set -euo pipefail
    rows=() warnings=() status=0
    for path in "$@"; do
        name=$(basename "$path") real=$(realpath "$path")
        sized=$real venv=$(dirname "$(dirname "$real")")
        [[ -f $venv/pyvenv.cfg ]] && sized=$venv
        if ! version=$("$path" --version 2>&1); then
            "$path" --help >/dev/null 2>&1 || { echo "failed     $path does not start" >&2; status=1; continue; }
            version=-
        fi
        rows+=("installed|$name|${path/#$HOME/\~}|$(du -sk "$sized" | cut -f1 | LC_ALL=C numfmt --from-unit=1024 --to=iec-i --suffix=B --format=%.1f)|${version%%$'\n'*}")
        first=$(command -v "$name")
        [[ $(realpath "$first") == "$real" ]] || warnings+=("warning    $name: PATH finds ${first/#$HOME/\~} first")
    done
    if (( ${#rows[@]} )); then printf '%s\n' "${rows[@]}" | column -t -s '|' -R 4; fi
    if (( ${#warnings[@]} )); then printf '%s\n' "${warnings[@]}"; fi
    exit "$status"

[positional-arguments]
[private]
run-checks +steps:
    #!/usr/bin/env bash
    set -uo pipefail
    LC_NUMERIC=C
    log_dir="${XDG_CACHE_HOME:-$HOME/.cache}/just-check"
    mkdir -p "$log_dir"
    log=$(mktemp "$log_dir/step.XXXXXX")
    trap 'rm -f "$log"' EXIT
    width=0 failed=()
    for step in "$@"; do (( ${#step} > width )) && width=${#step}; done
    for step in "$@"; do
        read -ra command <<< "$step"
        printf 'check  %-*s  ' "$width" "$step"
        start=$EPOCHREALTIME
        run=(just --justfile {{ quote(justfile()) }} "${command[@]}")
        if [[ -t 1 ]]; then run=(script -qefc "${run[*]@Q}" /dev/null); fi
        if "${run[@]}" < /dev/null > "$log" 2>&1; then status=ok; else status=FAIL; failed+=("$step"); fi
        printf '%-4s  %7.1fs\n' "$status" "$(LC_NUMERIC=C awk -v a="$start" -v b="$EPOCHREALTIME" 'BEGIN { print b - a }')"
        if [[ "$status" == FAIL ]]; then sed 's/\r$//; s/^/    /' "$log"; fi
    done
    if (( ${#failed[@]} )); then printf '%d of %d failed: %s\n' "${#failed[@]}" "$#" "${failed[*]}"; exit 1; fi
    printf 'all %d passed\n' "$#"

[private]
markdown *args:
    rumdl {{ args }} --disable MD001,MD013,MD024,MD025,MD026,MD031,MD032,MD033,MD034,MD036,MD041,MD058 --config 'MD060.enabled=true' --config 'MD060.style="aligned"' --config 'MD003.style="atx"' --config 'MD004.style="dash"' --config 'MD029.style="ordered"' --config 'MD046.style="fenced"' --config 'MD048.style="backtick"' --config 'MD049.style="asterisk"' --config 'MD050.style="asterisk"' --config 'MD055.style="leading_and_trailing"'
