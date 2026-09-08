//go:build darwin

package service

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

func installedArguments(content []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil, errInstalledArguments
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errInstalledArguments, err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "key" {
			continue
		}
		var key string
		if err := decoder.DecodeElement(&key, &start); err != nil {
			return nil, fmt.Errorf("%w: %w", errInstalledArguments, err)
		}
		if key != "ProgramArguments" {
			continue
		}
		var args struct {
			Values []string `xml:"string"`
		}
		if err := decoder.Decode(&args); err != nil {
			return nil, fmt.Errorf("%w: %w", errInstalledArguments, err)
		}
		if len(args.Values) == 0 {
			return nil, errInstalledArguments
		}
		return args.Values, nil
	}
}
