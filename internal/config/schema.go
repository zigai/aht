package config

import _ "embed"

// Schema is the JSON Schema for [Config]. TestSchemaMatchesConfig regenerates it
// with -update-schema.
//
//go:embed schema.json
var Schema []byte
