package main

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/zigai/aht/v2/internal/tools/githubapi"
)

const (
	ciWorkflow     = "ci.yml"
	ciWorkflowPath = ".github/workflows/" + ciWorkflow
	releaseBranch  = "master"
)

// requiredChecks are the CI jobs that must succeed on the tagged commit.
var requiredChecks = []string{"verify-linux", "verify-darwin", "artifact-validation", "change-compatibility"}

type workflowRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

type workflowJob struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type release struct {
	ID              int64  `json:"id"`
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Draft           bool   `json:"draft"`
	Body            string `json:"body"`
}

type newRelease struct {
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Name            string `json:"name"`
	Body            string `json:"body"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
}

type releaseDraft struct {
	Draft bool `json:"draft"`
}

// requireCI accepts only the newest master push run of CI for the exact commit,
// and only when every required check ran and succeeded in it.
func (a application) requireCI(ctx context.Context, t target) error {
	runsPath := t.repoPath("/actions/workflows/%s/runs?event=push&branch=%s&head_sha=%s&per_page=100", ciWorkflow, releaseBranch, t.sha)
	runs, err := githubapi.Paginate[workflowRun](ctx, a.api, runsPath, "workflow_runs")
	if err != nil {
		return fmt.Errorf("list CI runs: %w", err)
	}
	runs = slices.DeleteFunc(runs, func(run workflowRun) bool {
		return run.HeadSHA != t.sha || run.Event != "push" || run.HeadBranch != releaseBranch || run.Path != ciWorkflowPath
	})
	if len(runs) == 0 {
		return fmt.Errorf("%w: CI for %s must complete successfully before release (latest: missing/none). Rerun Release after CI succeeds", errRelease, t.sha)
	}
	latest := slices.MaxFunc(runs, func(x, y workflowRun) int { return cmp.Compare(x.ID, y.ID) })
	if latest.Status != "completed" || latest.Conclusion != "success" {
		return fmt.Errorf("%w: CI for %s must complete successfully before release (latest: %s/%s). Rerun Release after CI succeeds", errRelease, t.sha, latest.Status, orNone(latest.Conclusion))
	}
	jobs, err := githubapi.Paginate[workflowJob](ctx, a.api, t.repoPath("/actions/runs/%d/jobs?filter=latest&per_page=100", latest.ID), "jobs")
	if err != nil {
		return fmt.Errorf("list jobs of CI run %d: %w", latest.ID, err)
	}
	for _, name := range requiredChecks {
		matches := slices.DeleteFunc(slices.Clone(jobs), func(job workflowJob) bool { return job.Name != name })
		if len(matches) != 1 || matches[0].Conclusion != "success" {
			return fmt.Errorf("%w: CI run %d did not successfully execute required check %s", errRelease, latest.ID, name)
		}
	}
	a.logf("Verified all required checks for %s: %s", t.sha, latest.HTMLURL)
	return nil
}

// prepareDraft creates this run's draft, or reuses it on a retry, and reports
// its id and whether it was already published.
func (a application) prepareDraft(ctx context.Context, t target, notes string) error {
	existing, err := a.findRelease(ctx, t)
	if err != nil {
		return err
	}
	if existing == nil {
		// The marker is part of creation, so cleanup can reconcile a lost response.
		create := newRelease{
			TagName:         t.tag,
			TargetCommitish: t.sha,
			Name:            t.tag,
			Body:            trimNotes(notes) + "\n\n" + t.marker + "\n",
			Draft:           true,
			Prerelease:      strings.Contains(t.tag, "-"),
		}
		var created release
		if err := a.api.Do(ctx, http.MethodPost, t.repoPath("/releases"), create, &created); err != nil {
			return fmt.Errorf("create draft release %s: %w", t.tag, err)
		}
		existing = &created
	}
	if !t.owns(*existing) {
		return fmt.Errorf("%w: release %s belongs to another run; refusing to replace it", errRelease, t.tag)
	}
	if err := a.output("release-id", strconv.FormatInt(existing.ID, 10)); err != nil {
		return err
	}
	if err := a.output("published", strconv.FormatBool(!existing.Draft)); err != nil {
		return err
	}
	if existing.Draft {
		a.logf("Prepared draft release %d", existing.ID)
	} else {
		a.logf("Already published release %d", existing.ID)
	}
	return nil
}

// publishDraft publishes this run's draft. A publication whose response was
// lost is confirmed by reading the release back.
func (a application) publishDraft(ctx context.Context, t target, id int64) error {
	path := t.repoPath("/releases/%d", id)
	var current release
	if err := a.api.Get(ctx, path, &current); err != nil {
		return fmt.Errorf("read release %d: %w", id, err)
	}
	if !t.owns(current) {
		return fmt.Errorf("%w: refusing to publish a release owned by another run", errRelease)
	}
	if !current.Draft {
		return nil
	}
	publishErr := a.api.Do(ctx, http.MethodPatch, path, releaseDraft{Draft: false}, nil)
	if publishErr == nil {
		a.logf("Published release %d", id)
		return nil
	}
	var after release
	if err := a.api.Get(ctx, path, &after); err != nil || !t.owns(after) || after.Draft {
		return fmt.Errorf("publish release %d: %w", id, publishErr)
	}
	a.logf("Publication succeeded despite a lost API response")
	return nil
}

// cleanupDraft deletes this run's unpublished draft, if one exists.
func (a application) cleanupDraft(ctx context.Context, t target) error {
	existing, err := a.findRelease(ctx, t)
	if err != nil {
		return err
	}
	if existing == nil || !existing.Draft || !t.owns(*existing) {
		a.logf("No draft owned by this run needs cleanup")
		return nil
	}
	if err := a.api.Do(ctx, http.MethodDelete, t.repoPath("/releases/%d", existing.ID), nil, nil); err != nil {
		return fmt.Errorf("delete draft release %d: %w", existing.ID, err)
	}
	a.logf("Deleted owned draft %d", existing.ID)
	return nil
}

// findRelease returns the release for the tag, or nil when none exists. Other
// failures, such as a forbidden lookup, are not treated as an absent release.
func (a application) findRelease(ctx context.Context, t target) (*release, error) {
	var found release
	err := a.api.Get(ctx, t.repoPath("/releases/tags/%s", url.PathEscape(t.tag)), &found)
	if githubapi.HasStatus(err, http.StatusNotFound) {
		return nil, nil //nolint:nilnil // An absent release is an expected, non-error result.
	}
	if err != nil {
		return nil, fmt.Errorf("find release %s: %w", t.tag, err)
	}
	return &found, nil
}

func (t target) owns(r release) bool {
	return r.TagName == t.tag && r.TargetCommitish == t.sha && slices.Contains(strings.Split(r.Body, "\n"), t.marker)
}

func orNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}
