package cli

import (
	"fmt"
	"runtime/debug"
)

const shortCommitLen = 7

func versionText() string {
	info, _ := debug.ReadBuildInfo()

	return formatVersion(version, commit, dirty, info)
}

func formatVersion(v string, c string, modified string, info *debug.BuildInfo) string {
	if info != nil {
		if v == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}

		for _, setting := range info.Settings {
			switch {
			case setting.Key == "vcs.revision" && c == "none":
				c = setting.Value[:min(shortCommitLen, len(setting.Value))]
			case setting.Key == "vcs.modified" && modified == "":
				modified = setting.Value
			}
		}
	}

	if modified == "true" {
		c += ", dirty"
	}

	return fmt.Sprintf("aht %s (%s)", v, c)
}
