package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/zigai/aht/pkg/registry"
)

var errTrailingData = errors.New("extra trailing data after JSON object")

type renderedFileInstall struct {
	Harness                 registry.Harness
	Path                    string
	Content                 string
	CreateDirError          string
	WriteError              string
	InstalledMessage        string
	AlreadyInstalledMessage string
	DryRunMessage           string
}

type jsonHookFileInstall struct {
	Harness                 registry.Harness
	Path                    string
	Apply                   func(map[string]any) bool
	EncodeError             string
	CreateDirError          string
	WriteError              string
	InstalledMessage        string
	AlreadyInstalledMessage string
	DryRunMessage           string
}

func installRenderedFile(options Options, file renderedFileInstall) (Result, error) {
	changed, err := fileNeedsUpdate(file.Path, file.Content, options.Force)
	if err != nil {
		return Result{}, err
	}

	if err := writeInstallFile(file.Path, []byte(file.Content), changed, options.DryRun, file.CreateDirError, file.WriteError); err != nil {
		return Result{}, err
	}

	return Result{
		Harness:  string(file.Harness),
		Path:     file.Path,
		Changed:  changed,
		Message:  installStatusMessage(changed, options.DryRun, file.DryRunMessage, file.AlreadyInstalledMessage, file.InstalledMessage),
		NextStep: "",
		Snippet:  file.Content,
		Error:    "",
	}, nil
}

func installStatusMessage(changed bool, dryRun bool, dryRunMsg, alreadyInstalledMsg, installedMsg string) string {
	if dryRun {
		return dryRunMsg
	}
	if !changed {
		return alreadyInstalledMsg
	}
	return installedMsg
}

func installJSONHookFile(options Options, file jsonHookFileInstall) (Result, error) {
	config, err := readJSONObject(file.Path)
	if err != nil {
		return Result{}, err
	}

	changed := file.Apply(config)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", file.EncodeError, err)
	}
	data = append(data, '\n')

	if err := writeInstallFile(file.Path, data, changed, options.DryRun, file.CreateDirError, file.WriteError); err != nil {
		return Result{}, err
	}

	return Result{
		Harness:  string(file.Harness),
		Path:     file.Path,
		Changed:  changed,
		Message:  installStatusMessage(changed, options.DryRun, file.DryRunMessage, file.AlreadyInstalledMessage, file.InstalledMessage),
		NextStep: "",
		Snippet:  string(data),
		Error:    "",
	}, nil
}

func writeInstallFile(path string, data []byte, changed bool, dryRun bool, createDirError string, writeError string) error {
	if !changed || dryRun {
		return nil
	}

	return writeFileAtomic(path, data, createDirError, writeError)
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]any), nil
		}

		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return make(map[string]any), nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var config map[string]any
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parsing %s: %w", path, errTrailingData)
	}
	if config == nil {
		config = make(map[string]any)
	}

	return config, nil
}
