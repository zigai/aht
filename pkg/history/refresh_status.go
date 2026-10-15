package history

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/zigai/strata"
	"github.com/zigai/strata/codec"
)

var errRefreshStatusInvalid = errors.New("history refresh status is stale or invalid; run aht manage history refresh")

// RefreshStatus describes the last completed explicit index refresh, including
// partial refreshes. Searches do not publish status.
type RefreshStatus struct {
	CompletedAt time.Time       `json:"completed_at"`
	Refresh     []SourceRefresh `json:"refresh"`
	Incomplete  bool            `json:"incomplete"`
	IssueCount  int             `json:"issue_count"`
}

type refreshStatusFile struct {
	RefreshStatus

	IndexVersion  int    `json:"index_version"`
	IndexIdentity string `json:"index_identity"`
}

// RefreshStatus reads the last refresh status without opening, creating, or
// repairing the index. Missing, corrupt, or stale status returns an error.
func (c Catalog) RefreshStatus(ctx context.Context) (RefreshStatus, error) {
	var empty RefreshStatus
	if err := ctx.Err(); err != nil {
		return empty, fmt.Errorf("read history refresh status: %w", err)
	}
	path, err := historyIndexPath(c.IndexPath)
	if err != nil {
		return empty, err
	}
	data, err := os.ReadFile(path + ".refresh.json")
	if err != nil {
		return empty, fmt.Errorf("read history refresh status: %w", err)
	}
	var saved refreshStatusFile
	if err = codec.NewJSON().Decode(data, &saved); err != nil {
		return empty, fmt.Errorf("decode history refresh status: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return empty, fmt.Errorf("inspect history index for refresh status: %w", err)
	}
	if !saved.validForIndex(info) {
		return empty, errRefreshStatusInvalid
	}
	if err = ctx.Err(); err != nil {
		return empty, fmt.Errorf("read history refresh status: %w", err)
	}
	return saved.RefreshStatus, nil
}

func (c Catalog) saveRefreshStatus(ctx context.Context, result Result, incomplete bool) error {
	path, err := historyIndexPath(c.IndexPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect history index for refresh status: %w", err)
	}
	identity, _ := fileIdentity(info)
	saved := refreshStatusFile{
		CompletedAt:   time.Now().UTC(),
		Refresh:       result.Refresh,
		Incomplete:    incomplete,
		IssueCount:    len(result.Issues) + result.OmittedIssues,
		IndexVersion:  indexVersion,
		IndexIdentity: identity,
	}
	if err = ctx.Err(); err != nil {
		return fmt.Errorf("save history refresh status: %w", err)
	}
	if err = strata.Save(path+".refresh.json", saved, strata.WithFileMode(0o600)); err != nil {
		return fmt.Errorf("save history refresh status after committed refresh: %w", err)
	}
	return nil
}

func (saved refreshStatusFile) validForIndex(info os.FileInfo) bool {
	identity, _ := fileIdentity(info)
	return saved.IndexVersion == indexVersion && saved.IndexIdentity != "" && saved.IndexIdentity == identity && !saved.CompletedAt.IsZero() && saved.IssueCount >= 0 && info.Mode().IsRegular()
}
