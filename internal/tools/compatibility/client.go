package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	requestTimeout   = 30 * time.Second
	maxMetadataBytes = 2 * 1024 * 1024
	maxRedirects     = 10
	stateArtifact    = "compatibility-release-state-v1"
	workflowPath     = ".github/workflows/compatibility-releases.yml"
	artifactPageSize = 100
	maxArtifactPages = 20
)

type releaseClient struct {
	http       *http.Client
	githubBase string
	npmBase    string
	pypiBase   string
	token      string
}

type artifact struct {
	ID          int64 `json:"id"`
	Expired     bool  `json:"expired"`
	WorkflowRun struct {
		ID         int64  `json:"id"`
		HeadBranch string `json:"head_branch"`
	} `json:"workflow_run"`
}

func newReleaseClient(token string) *releaseClient {
	client := *http.DefaultClient
	client.Timeout = requestTimeout
	client.CheckRedirect = checkRedirect
	return &releaseClient{http: &client, githubBase: "https://api.github.com", npmBase: "https://registry.npmjs.org", pypiBase: "https://pypi.org/pypi", token: token}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("%w: too many redirects", errCompatibility)
	}
	// Artifact downloads redirect to object storage; do not forward the API token.
	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		req.Header.Del("Authorization")
	}
	return nil
}

func (c *releaseClient) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "aht-compatibility")
	if strings.HasPrefix(target, c.githubBase+"/") {
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-Github-Api-Version", "2026-03-10")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", target, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: GET %s: HTTP %d", errCompatibility, target, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMetadataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", target, err)
	}
	if len(body) > maxMetadataBytes {
		return nil, fmt.Errorf("%w: response from %s exceeds size limit", errCompatibility, target)
	}
	return body, nil
}

func (c *releaseClient) getJSON(ctx context.Context, target string, destination any) error {
	body, err := c.get(ctx, target)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode %s: %w", target, err)
	}
	return nil
}

func (c *releaseClient) latest(ctx context.Context, spec harnessSpec) (string, error) {
	var version string
	var err error
	switch spec.Source {
	case "npm":
		version, err = c.npmVersion(ctx, spec)
	case "pypi":
		version, err = c.pypiVersion(ctx, spec)
	case "github":
		version, err = c.githubVersion(ctx, spec)
	case "channel":
		var body []byte
		body, err = c.get(ctx, spec.URL)
		version = strings.TrimSpace(string(body))
	default:
		err = fmt.Errorf("%w: no release source for %s", errCompatibility, spec.ID)
	}
	if err != nil {
		return "", err
	}
	if _, err := parseVersion(version); err != nil {
		return "", err
	}
	return version, nil
}

func (c *releaseClient) npmVersion(ctx context.Context, spec harnessSpec) (string, error) {
	var data struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, c.npmBase+"/"+url.PathEscape(spec.Package)+"/latest", &data); err != nil {
		return "", err
	}
	if data.Name != spec.Package {
		return "", fmt.Errorf("%w: npm package identity mismatch", errCompatibility)
	}
	return data.Version, nil
}

func (c *releaseClient) pypiVersion(ctx context.Context, spec harnessSpec) (string, error) {
	var data struct {
		Info struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"info"`
		URLs []struct {
			Yanked bool `json:"yanked"`
		} `json:"urls"`
	}
	if err := c.getJSON(ctx, c.pypiBase+"/"+spec.Package+"/json", &data); err != nil {
		return "", err
	}
	if data.Info.Name != spec.Package {
		return "", fmt.Errorf("%w: PyPI package identity mismatch", errCompatibility)
	}
	for _, file := range data.URLs {
		if !file.Yanked {
			return data.Info.Version, nil
		}
	}
	return "", fmt.Errorf("%w: no non-yanked distribution", errCompatibility)
}

func (c *releaseClient) githubVersion(ctx context.Context, spec harnessSpec) (string, error) {
	var data struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"assets"`
	}
	if err := c.getJSON(ctx, c.githubBase+"/repos/"+spec.Repo+"/releases/latest", &data); err != nil {
		return "", err
	}
	if data.Draft || data.Prerelease {
		return "", fmt.Errorf("%w: not a stable published release", errCompatibility)
	}
	for _, asset := range data.Assets {
		if asset.Name == spec.Asset && asset.State == "uploaded" {
			return data.TagName, nil
		}
	}
	return "", fmt.Errorf("%w: release is missing %s", errCompatibility, spec.Asset)
}

func (c *releaseClient) restoreState(ctx context.Context, repository, branch, runID string) (releaseState, error) {
	state := emptyState()
	if repository == "" || branch == "" {
		return state, fmt.Errorf("%w: GITHUB_REPOSITORY and AHT_DEFAULT_BRANCH are required", errCompatibility)
	}
	base := c.githubBase + "/repos/" + repository + "/actions"
	for page := 1; page <= maxArtifactPages; page++ {
		var data struct {
			Artifacts []artifact `json:"artifacts"`
		}
		target := base + "/artifacts?name=" + stateArtifact + "&per_page=" + strconv.Itoa(artifactPageSize) + "&page=" + strconv.Itoa(page)
		if err := c.getJSON(ctx, target, &data); err != nil {
			return state, err
		}
		if data.Artifacts == nil {
			return state, fmt.Errorf("%w: invalid artifact listing; refusing to reset release history", errCompatibility)
		}
		slices.SortFunc(data.Artifacts, func(a, b artifact) int {
			return cmp.Compare(b.ID, a.ID)
		})
		for _, item := range data.Artifacts {
			allowed, err := c.trustedArtifact(ctx, base, item, branch, runID)
			if err != nil {
				return state, err
			}
			if !allowed {
				continue
			}
			body, err := c.get(ctx, base+"/artifacts/"+strconv.FormatInt(item.ID, 10)+"/zip")
			if err != nil {
				return state, err
			}
			return decodeStateArchive(body)
		}
		if len(data.Artifacts) < artifactPageSize {
			return state, nil
		}
	}
	return state, fmt.Errorf("%w: no usable state within artifact search limit", errCompatibility)
}

func (c *releaseClient) trustedArtifact(ctx context.Context, base string, item artifact, branch, runID string) (bool, error) {
	if item.Expired || item.WorkflowRun.HeadBranch != branch || strconv.FormatInt(item.WorkflowRun.ID, 10) == runID {
		return false, nil
	}
	var details struct {
		Path  string `json:"path"`
		Event string `json:"event"`
	}
	if err := c.getJSON(ctx, base+"/runs/"+strconv.FormatInt(item.WorkflowRun.ID, 10), &details); err != nil {
		return false, err
	}
	return details.Path == workflowPath && (details.Event == "schedule" || details.Event == "workflow_dispatch"), nil
}
