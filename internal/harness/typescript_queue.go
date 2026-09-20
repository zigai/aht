package harness

import _ "embed"

//go:embed assets/typescript_queue.ts.tmpl
var typeScriptQueueTemplate string

//go:embed assets/typescript_event_parser.ts.tmpl
var typeScriptEventParserTemplate string
