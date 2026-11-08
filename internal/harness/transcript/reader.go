package transcript

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/zigai/aht/v2/pkg/registry"
)

const MaxRecordBytes = 16 << 20

const (
	FormatJSONL Format = iota
	FormatDocument
	FormatDatabase
	FormatCompressed
)

const (
	TypedUserRole    = "typed-user"
	InjectedUserRole = "injected-user"
)

var (
	ErrInvalidRecord = errors.New("invalid JSON history record")
	ErrUnknownFormat = errors.New("unrecognized history format or missing native session identity")
	ErrRecordSize    = errors.New("history record exceeds 16 MiB")
)

type Format uint8

type Conversation struct {
	Harness     registry.Harness `json:"harness"`
	SessionID   string           `json:"session_id"`
	Path        string           `json:"path"`
	Title       string           `json:"title,omitempty"`
	CustomTitle string           `json:"custom_title,omitempty"`
	AITitle     string           `json:"ai_title,omitempty"`
	Prompt      string           `json:"prompt,omitempty"`
	CWD         string           `json:"cwd,omitempty"`
	ProjectRoot string           `json:"project_root,omitempty"`
	GitBranch   string           `json:"git_branch,omitempty"`
	Model       string           `json:"model,omitempty"`
	Messages    int              `json:"messages"`
	CreatedAt   time.Time        `json:"created_at,omitzero"`
	// UpdatedAt falls back to the transcript's modification time when no record has a timestamp.
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

type Decoder struct {
	Conversation *Conversation
	Recognized   *bool
	Emit         func(context.Context, string, string, string, int, time.Time)
	Issue        func(string, error)
	Projection   string
	Metadata     map[string]string
	IncludeTools bool
	UserParts    func(string) []TextPart
}
type Row struct {
	SessionID, Title, CWD, Created, Updated, MessageID, Role string
	Body                                                     sql.NullString
	Timestamp                                                string
}
type (
	RowReader func(context.Context, *Decoder, Row) error
	//exhaustruct:optional
	Reader struct {
		Patterns       []string
		DefaultFormat  Format
		FileFormat     func(string) Format
		Sources        func(string) ([]string, error)
		SkipDirectory  func(string) bool
		SkipFile       func(string) bool
		SourceMetadata func(string, bool, func(string, error)) map[string]string
		Initialize     func(*Decoder)
		Projection     func(context.Context, *os.File, int64) (string, error)
		UserParts      func(string) []TextPart
		Extra          func(string, map[string]string, func(string) string) string
		Record         func(context.Context, *Decoder, Record, int)
		FastRecord     func(context.Context, *Decoder, []byte, int) bool
		Document       func(context.Context, *Decoder, []byte) error
		Query          func(context.Context, *sql.DB) (string, RowReader, error)
		LocalTitles    bool
		// RewritableHeaders names, in order, leading metadata records whose
		// byte lengths are fixed. They are replayed after the body so their
		// current values override older metadata stored in later records.
		RewritableHeaders []string
		// Parent maps a history file that belongs to another file's
		// conversation, such as a subagent transcript, to that file's path.
		Parent func(string) string
	}
)

func (d *Decoder) Capture(
	ctx context.Context,
	role string,
	body string,
	id string,
	line int,
	at time.Time,
) {
	if (role == "user" || role == InjectedUserRole) && d.UserParts != nil {
		for _, part := range d.UserParts(body) {
			if role == InjectedUserRole && part.Role != InjectedUserRole {
				continue
			}

			if d.Emit != nil {
				d.Emit(ctx, part.Role, part.Text, id, line, at)
			}
		}

		return
	}

	if d.Emit != nil {
		d.Emit(ctx, role, body, id, line, at)
	}
}

// Format selects a parser independently of directory discovery patterns.
func (r Reader) Format(path string) Format {
	if r.FileFormat != nil {
		return r.FileFormat(path)
	}

	return r.DefaultFormat
}
