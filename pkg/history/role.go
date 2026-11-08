package history

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleAgent     Role = "agent"
	RoleContext   Role = "context"
	RoleTool      Role = "tool"
	RoleAll       Role = "all"
)

const (
	StatusSearched    Status = "searched"
	StatusSkipped     Status = "skipped"
	StatusMissing     Status = "missing"
	StatusUnsupported Status = "unsupported"
	StatusFailed      Status = "failed"
)

// Role is the author of a message part. An excerpt's Role is user, assistant,
// context, or tool. A query also accepts agent, an alias of assistant, and all;
// an empty query Role selects user and assistant parts.
type Role string

// Status is a source's coverage in a [Result].
type Status string

// Valid reports whether r is accepted in a [Query].
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleAgent, RoleContext, RoleTool, RoleAll:
		return true
	}

	return false
}
