// Command release gates a version tag on CI and tracked regressions and manages its GitHub release.
//
// Each subcommand is safe to rerun: a draft is owned by the workflow run that
// created it through a marker in its body, so retries reuse it and cleanup never
// touches a published release or one created by another run.
package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/zigai/aht/v2/internal/tools/githubapi"
)

const usage = "usage: release require-ci|prepare-draft|publish-draft|cleanup-draft"

var (
	errRelease = errors.New("release")
	shaRE      = regexp.MustCompile(`^[a-f0-9]{40}$`)
	tagRefRE   = regexp.MustCompile(`^refs/tags/(v[^/]+)$`)
	repoRE     = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
	runIDRE    = regexp.MustCompile(`^[0-9]+$`)
)

// target identifies the tagged commit and the workflow run acting on it.
type target struct {
	repo   string
	tag    string
	sha    string
	marker string
}

type application struct {
	api    *githubapi.Client
	getenv func(string) string
	stdout io.Writer
}

func main() {
	if err := runCommandLine(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCommandLine() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	api := githubapi.New(cmp.Or(os.Getenv("GITHUB_API_URL"), githubapi.DefaultBaseURL), cmp.Or(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN")))
	app := application{api: api, getenv: os.Getenv, stdout: os.Stdout}
	return app.run(ctx, os.Args[1:])
}

func (a application) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: %s", errRelease, usage)
	}
	release, err := a.target()
	if err != nil {
		return err
	}
	switch args[0] {
	case "require-ci":
		return a.requireCI(ctx, release)
	case "prepare-draft":
		notesPath := cmp.Or(a.getenv("AHT_RELEASE_NOTES"), "dist/CHANGELOG.md")
		notes, err := os.ReadFile(notesPath)
		if err != nil {
			return fmt.Errorf("read release notes: %w", err)
		}
		return a.prepareDraft(ctx, release, string(notes))
	case "publish-draft":
		id, err := strconv.ParseInt(a.getenv("AHT_RELEASE_ID"), 10, 64)
		if err != nil {
			return fmt.Errorf("%w: AHT_RELEASE_ID must be a release id: %w", errRelease, err)
		}
		return a.publishDraft(ctx, release, id)
	case "cleanup-draft":
		return a.cleanupDraft(ctx, release)
	default:
		return fmt.Errorf("%w: unknown command %q; %s", errRelease, args[0], usage)
	}
}

// target reads the release identity from the Actions environment. A release
// requires a resolved commit and a version tag.
func (a application) target() (target, error) {
	repo, ref, runID, sha := a.getenv("GITHUB_REPOSITORY"), a.getenv("GITHUB_REF"), a.getenv("GITHUB_RUN_ID"), a.getenv("AHT_RELEASE_SHA")
	tag := tagRefRE.FindStringSubmatch(ref)
	if !shaRE.MatchString(sha) || tag == nil || !repoRE.MatchString(repo) || !runIDRE.MatchString(runID) {
		return target{}, fmt.Errorf("%w: a release requires GITHUB_REPOSITORY, GITHUB_RUN_ID, a version tag GITHUB_REF, and a resolved commit AHT_RELEASE_SHA", errRelease)
	}
	marker := fmt.Sprintf("<!-- aht-release:%s:%s:%s -->", repo, runID, sha)
	return target{repo: repo, tag: tag[1], sha: sha, marker: marker}, nil
}

func (a application) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(a.stdout, format+"\n", args...)
}

// output records a step output for later workflow steps.
func (a application) output(name, value string) error {
	text := name + "=" + value + "\n"
	path := a.getenv("GITHUB_OUTPUT")
	if path == "" {
		_, err := io.WriteString(a.stdout, text)
		return err //nolint:wrapcheck // Local runs print outputs; the writer error needs no context.
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_, writeErr := file.WriteString(text)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}

func (t target) repoPath(format string, args ...any) string {
	return "/repos/" + t.repo + fmt.Sprintf(format, args...)
}

func trimNotes(notes string) string {
	return strings.TrimRight(notes, " \t\r\n")
}
