package harness

import "github.com/zigai/aht/v2/pkg/fakeprovider"

const LaunchProviderName = "aht-fakeprovider"

type LaunchPaths struct {
	Root string
	Home string
	Work string
}

type LaunchModel struct {
	Endpoint string
	Model    string
	APIKey   string
}

type LaunchFile struct {
	Path   string
	Update func(existing []byte) ([]byte, error)
}

type LaunchService struct {
	Args    []string
	Address string
}

type LaunchTool struct {
	Name      string
	Arguments func(command string) map[string]any
}

type LaunchPlan struct {
	Executable  string
	Protocol    fakeprovider.Protocol
	Env         []string
	Files       []LaunchFile
	Setup       [][]string
	Services    []LaunchService
	Headless    func(prompt string) ([]string, error)
	Interactive func(prompt string) ([]string, error)
	ShellTool   LaunchTool
}

type Launchable interface {
	LaunchPlan(paths LaunchPaths, model LaunchModel) (LaunchPlan, error)
}
