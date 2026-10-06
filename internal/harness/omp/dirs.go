package omp

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zigai/aht/v2/internal/harness"
)

const defaultConfigDirName = ".omp"

func ompAgentDir() string {
	return agentDirUnder(harness.HomeDir())
}

func agentDirUnder(home string) string {
	profile := activeProfile()
	if profile == "" {
		if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
			return value
		}
	}

	return filepath.Join(profileRoot(home, profile), "agent")
}

func sessionsDirUnder(home string) string {
	profile := activeProfile()
	agentDir := agentDirUnder(home)
	if agentDir == filepath.Join(profileRoot(home, profile), "agent") {
		if root := xdgDataRoot(profile); root != "" {
			return filepath.Join(root, "sessions")
		}
	}

	return filepath.Join(agentDir, "sessions")
}

func configRoot(home string) string {
	name := strings.TrimSpace(os.Getenv("PI_CONFIG_DIR"))
	if name == "" {
		name = defaultConfigDirName
	}

	return filepath.Join(home, name)
}

func profileRoot(home string, profile string) string {
	root := configRoot(home)
	if profile == "" {
		return root
	}

	return filepath.Join(root, "profiles", profile)
}

func activeProfile() string {
	value, ok := os.LookupEnv("OMP_PROFILE")
	if !ok {
		value = os.Getenv("PI_PROFILE")
	}
	value = strings.TrimSpace(value)
	if value == "default" {
		return ""
	}

	return value
}

func xdgDataRoot(profile string) string {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if base == "" {
		return ""
	}
	relative := "omp"
	if profile != "" {
		relative = filepath.Join(relative, "profiles", profile)
	}
	if !filepath.IsLocal(relative) {
		return ""
	}
	dataHome, err := os.OpenRoot(base)
	if err != nil {
		return ""
	}
	_, statErr := dataHome.Stat(relative)
	closeErr := dataHome.Close()
	if statErr != nil || closeErr != nil {
		return ""
	}

	return filepath.Join(base, relative)
}
