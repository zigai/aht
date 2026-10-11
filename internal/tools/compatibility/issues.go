package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zigai/aht/v2/internal/tools/githubapi"
)

const (
	// issueCreator owns regression issues; issues from anyone else are never edited.
	issueCreator       = "github-actions[bot]"
	reasonAboveMaximum = "above supported maximum"
)

var issueMarkerRE = regexp.MustCompile(`(?m)^<!-- aht-compatibility:([a-z0-9-]+) -->$`)

type githubIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct{} `json:"pull_request"`
}

type issueContent struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type issueComment struct {
	Body string `json:"body"`
}

type issueClosure struct {
	State       string `json:"state"`
	StateReason string `json:"state_reason"`
}

// issues syncs the issue report written by finish: one open issue tracks each
// harness whose latest supported release fails, so known regressions stay
// visible without failing every scheduled run.
func (a application) issues(ctx context.Context) error {
	var report issueReport
	if err := readJSON(filepath.Join(a.workDirectory(), "issues.json"), &report); err != nil {
		return err
	}
	repo := a.getenv("GITHUB_REPOSITORY")
	if repo == "" {
		return fmt.Errorf("%w: GITHUB_REPOSITORY is required", errCompatibility)
	}
	api := githubapi.New(cmp.Or(a.getenv("GITHUB_API_URL"), githubapi.DefaultBaseURL), cmp.Or(a.getenv("GH_TOKEN"), a.getenv("GITHUB_TOKEN")))
	return syncIssues(ctx, api, repo, report, a.stdout)
}

func syncIssues(ctx context.Context, api *githubapi.Client, repo string, report issueReport, log io.Writer) error {
	tracked, err := trackedRegressionIssues(ctx, api, repo)
	if err != nil {
		return err
	}
	for _, item := range report.Open {
		content := issueContent{Title: issueTitle(item), Body: issueBody(item)}
		existing, ok := tracked[item.Harness]
		switch {
		case !ok:
			var created githubIssue
			if err := api.Do(ctx, http.MethodPost, "/repos/"+repo+"/issues", content, &created); err != nil {
				return fmt.Errorf("open issue for %s: %w", item.Harness, err)
			}
			_, _ = fmt.Fprintf(log, "Opened #%d for %s %s\n", created.Number, item.Harness, item.Version)
		case existing.Title != content.Title || existing.Body != content.Body:
			if err := api.Do(ctx, http.MethodPatch, issuePath(repo, existing.Number), content, nil); err != nil {
				return fmt.Errorf("update issue #%d: %w", existing.Number, err)
			}
			_, _ = fmt.Fprintf(log, "Updated #%d for %s %s\n", existing.Number, item.Harness, item.Version)
		}
	}
	for _, item := range report.Resolved {
		existing, ok := tracked[item.Harness]
		if !ok {
			continue
		}
		path := issuePath(repo, existing.Number)
		if err := api.Do(ctx, http.MethodPost, path+"/comments", issueComment{Body: issueResolution(item)}, nil); err != nil {
			return fmt.Errorf("comment on issue #%d: %w", existing.Number, err)
		}
		if err := api.Do(ctx, http.MethodPatch, path, issueClosure{State: "closed", StateReason: "completed"}, nil); err != nil {
			return fmt.Errorf("close issue #%d: %w", existing.Number, err)
		}
		_, _ = fmt.Fprintf(log, "Closed #%d for %s %s\n", existing.Number, item.Harness, item.Version)
	}
	return nil
}

// trackedRegressionIssues maps each harness to the oldest-listed open issue this
// workflow opened for it, identified by its marker line.
func trackedRegressionIssues(ctx context.Context, api *githubapi.Client, repo string) (map[string]githubIssue, error) {
	path := "/repos/" + repo + "/issues?state=open&creator=" + url.QueryEscape(issueCreator) + "&per_page=100"
	issues, err := githubapi.Paginate[githubIssue](ctx, api, path, "")
	if err != nil {
		return nil, fmt.Errorf("list regression issues: %w", err)
	}
	tracked := map[string]githubIssue{}
	for _, issue := range issues {
		if issue.PullRequest != nil || issue.User.Login != issueCreator {
			continue
		}
		match := issueMarkerRE.FindStringSubmatch(issue.Body)
		if match == nil {
			continue
		}
		if _, seen := tracked[match[1]]; !seen {
			tracked[match[1]] = issue
		}
	}
	return tracked, nil
}

func issuePath(repo string, number int) string {
	return fmt.Sprintf("/repos/%s/issues/%d", repo, number)
}

func issueMarker(harness string) string {
	return "<!-- aht-compatibility:" + harness + " -->"
}

func issueTitle(item issueStatus) string {
	return "Compatibility regression: " + item.Harness
}

func issueBody(item issueStatus) string {
	return strings.Join([]string{
		fmt.Sprintf("The scheduled release compatibility check fails for **%s %s**.", item.Harness, item.Version),
		"",
		fmt.Sprintf("- Last successful release: %s (change checks in CI pin this release)", cmp.Or(item.Successful, "none")),
		"- Supported maximum: " + cmp.Or(item.MaxVersion, "latest"),
		"- Failing run: " + cmp.Or(item.RunURL, "unknown"),
		"",
		"The check retries each newer release and closes this issue when one passes. If the failure is an",
		"upstream incompatibility, set `MaxVersion` in the adapter's distribution to the last successful release.",
		"",
		issueMarker(item.Harness),
		"",
	}, "\n")
}

func issueResolution(item issueStatus) string {
	if item.Reason == reasonAboveMaximum {
		return fmt.Sprintf("%s %s is above the supported maximum %s, so the adapter's `MaxVersion` now tracks this failure.", item.Harness, item.Version, item.MaxVersion)
	}
	return fmt.Sprintf("%s %s passed in %s.", item.Harness, item.Version, cmp.Or(item.RunURL, "the latest check"))
}
