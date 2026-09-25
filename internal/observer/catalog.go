package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	harnesscatalog "github.com/zigai/aht/v2/internal/harness/catalog"
)

const (
	catalogJSONEnv  = "AHT_CATALOG_JSON"
	catalogFileEnv  = "AHT_CATALOG_FILE"
	maxCatalogBytes = 1 << 20
)

var (
	errCatalogEntryHarness = errors.New("catalog entry has no harness")
	errCatalogSessionsType = errors.New("catalog sessions must be an array")
	errCatalogTooLarge     = errors.New("catalog exceeds 1 MiB limit")
)

// DefaultCatalogList reads the optional machine-readable catalog configured by
// AHT_CATALOG_JSON or AHT_CATALOG_FILE. The payload is
// either an array of catalog entries or an object with a sessions array.
func DefaultCatalogList(ctx context.Context) ([]CatalogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("catalog context: %w", err)
	}
	payload, err := catalogPayload()
	if err != nil {
		return nil, err
	}
	if payload == "" {
		return nil, nil
	}
	entries, err := decodeCatalog([]byte(payload))
	if err != nil {
		return nil, err
	}
	for index := range entries {
		if entries[index].Harness == "" {
			return nil, fmt.Errorf("catalog entry %d: %w", index, errCatalogEntryHarness)
		}
		normalized, err := harnesscatalog.Normalize(string(entries[index].Harness))
		if err != nil {
			return nil, fmt.Errorf("catalog entry %d: %w", index, err)
		}
		entries[index].Harness = normalized
	}
	return entries, nil
}

func catalogPayload() (string, error) {
	payload := strings.TrimSpace(os.Getenv(catalogJSONEnv))
	if len(payload) > maxCatalogBytes {
		return "", errCatalogTooLarge
	}
	if payload != "" {
		return payload, nil
	}
	path := strings.TrimSpace(os.Getenv(catalogFileEnv))
	if path == "" {
		return "", nil
	}
	data, err := readCatalogFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readCatalogFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("opening catalog file %q: %w", path, err)
	}
	file, err := root.Open(filepath.Base(path))
	if err != nil {
		closeErr := root.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("opening catalog file %q: %w; closing root: %w", path, err, closeErr)
		}
		return nil, fmt.Errorf("opening catalog file %q: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	closeErr := file.Close()
	rootCloseErr := root.Close()
	if readErr != nil {
		return nil, fmt.Errorf("reading catalog file %q: %w", path, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("closing catalog file %q: %w", path, closeErr)
	}
	if rootCloseErr != nil {
		return nil, fmt.Errorf("closing catalog root for %q: %w", path, rootCloseErr)
	}
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("reading catalog file %q: %w", path, errCatalogTooLarge)
	}
	return data, nil
}

func decodeCatalog(data []byte) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	if err := json.Unmarshal(data, &entries); err == nil && entries != nil {
		return entries, nil
	}
	var envelope struct {
		Sessions []CatalogEntry `json:"sessions"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decoding catalog JSON: %w", err)
	}
	if envelope.Sessions == nil {
		return nil, fmt.Errorf("decoding catalog JSON: %w", errCatalogSessionsType)
	}
	return envelope.Sessions, nil
}
