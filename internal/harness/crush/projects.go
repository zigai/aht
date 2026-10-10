package crush

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zigai/aht/v2/internal/harness/transcript"
)

const databaseName = "crush.db"

type crushProject struct {
	Path    string `json:"path"`
	DataDir string `json:"data_dir"`
}

func crushDataHome(home string) string {
	if value := strings.TrimSpace(os.Getenv("CRUSH_GLOBAL_DATA")); value != "" {
		return value
	}
	if home == "" && os.Getenv("XDG_DATA_HOME") == "" {
		return ""
	}

	return filepath.Join(transcript.DataHome(home), "crush")
}

func projectsPath(home string) string {
	dataHome := crushDataHome(home)
	if dataHome == "" {
		return ""
	}

	return filepath.Join(dataHome, "projects.json")
}

func readProjects(path string) ([]crushProject, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Crush projects: %w", err)
	}
	var list struct {
		Projects []crushProject `json:"projects"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse Crush projects %s: %w", path, err)
	}

	return list.Projects, nil
}

func (project crushProject) databasePath() string {
	dataDir := project.DataDir
	if dataDir == "" {
		return ""
	}
	if !filepath.IsAbs(dataDir) {
		if !filepath.IsAbs(project.Path) {
			return ""
		}
		dataDir = filepath.Join(project.Path, dataDir)
	}

	return filepath.Join(filepath.Clean(dataDir), databaseName)
}

func projectDatabases(projects []crushProject) []string {
	seen := make(map[string]bool, len(projects))
	databases := make([]string, 0, len(projects))
	for _, project := range projects {
		database := project.databasePath()
		if database == "" || seen[database] {
			continue
		}
		seen[database] = true
		databases = append(databases, database)
	}

	return databases
}
