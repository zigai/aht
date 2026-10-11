package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/pelletier/go-toml/v2"
)

var errUnknownFieldInConfig = errors.New("unknown field in config")

// DecodeTOML decodes TOML bytes into target with strict unknown field rejection.
func DecodeTOML(data []byte, target any) error {
	return decodeTOML(data, target)
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
