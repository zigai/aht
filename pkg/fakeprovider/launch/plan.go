package launch

const (
	ProviderName = "aht-fakeprovider"

	ProtocolAnthropicMessages Protocol = "anthropic-messages"
	ProtocolOpenAIChat        Protocol = "openai-chat"
	ProtocolOpenAIResponses   Protocol = "openai-responses"
)

// Protocol names the model API wire format a launched harness speaks.
type Protocol string

type Paths struct {
	Root string
	Home string
	Work string
}

type Model struct {
	Endpoint string
	Model    string
	APIKey   string
}

type File struct {
	Path   string
	Update func(existing []byte) ([]byte, error)
}

type Service struct {
	Args    []string
	Address string
}

// Tool is the native shell tool a scripted model can call.
type Tool struct {
	Name string
	// Arguments builds the native tool input that runs command.
	Arguments func(command string) map[string]any
}

//exhaustruct:optional
type Plan struct {
	Executable    string
	Protocol      Protocol
	Env           []string
	Files         []File
	Setup         [][]string
	Services      []Service
	Headless      func(prompt string) ([]string, error)
	Interactive   func(prompt string) ([]string, error)
	ShellTool     Tool
	TitleRequests []TitleRequest
}
