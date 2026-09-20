package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

var errUnknownFieldInConfig = errors.New("unknown field in config")

// readBounded reads up to limit bytes from r. If more than limit bytes are
// available, it returns ErrConfigFileTooLarge.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if int64(len(contents)) > limit {
		return nil, ErrConfigFileTooLarge
	}
	return contents, nil
}

// readBoundedFile reads a file bounded by maxConfigFileSize.
func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = file.Close() }()
	contents, err := readBounded(file, maxConfigFileSize)
	if err != nil {
		if errors.Is(err, ErrConfigFileTooLarge) {
			return nil, fmt.Errorf("%w: %s", ErrConfigFileTooLarge, path)
		}
		return nil, err
	}
	return contents, nil
}

// decodeTOML decodes TOML bytes into target with strict unknown field rejection.
func decodeTOML(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		if strictErr, ok := errors.AsType[*toml.StrictMissingError](err); ok {
			return fmt.Errorf("%w: %s", errUnknownFieldInConfig, strictErr.String())
		}
		return fmt.Errorf("decode toml: %w", err)
	}
	return nil
}
