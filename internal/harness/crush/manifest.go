package crush

import _ "embed"

//go:embed assets/screen.toml
var screenManifest string

func (crushHarness) ScreenManifest() string {
	return screenManifest
}
