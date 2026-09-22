package opencode

import _ "embed"

//go:embed assets/screen.toml
var screenManifest string

func (opencodeHarness) ScreenManifest() string {
	return screenManifest
}
