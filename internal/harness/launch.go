package harness

const (
	LaunchProviderName = "aht-fakeprovider"

	LaunchProtocolAnthropicMessages LaunchProtocol = "anthropic-messages"
	LaunchProtocolOpenAIChat        LaunchProtocol = "openai-chat"
	LaunchProtocolOpenAIResponses   LaunchProtocol = "openai-responses"
)

// LaunchProtocol names the model API wire format a launched harness speaks.
type LaunchProtocol string

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

//exhaustruct:optional
type LaunchPlan struct {
	Executable    string
	Protocol      LaunchProtocol
	Env           []string
	Files         []LaunchFile
	Setup         [][]string
	Services      []LaunchService
	Headless      func(prompt string) ([]string, error)
	Interactive   func(prompt string) ([]string, error)
	ShellTool     LaunchTool
	TitleRequests []TitleRequest
}

type Launchable interface {
	LaunchPlan(paths LaunchPaths, model LaunchModel) (LaunchPlan, error)
}
