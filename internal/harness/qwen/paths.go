package qwen

import (
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

func configDirectory(home string) string {
	if value := strings.TrimSpace(os.Getenv("QWEN_HOME")); value != "" {
		return expandHomePath(value, home)
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".qwen")
}

func runtimeDirectory(home string) string {
	if value := strings.TrimSpace(os.Getenv("QWEN_RUNTIME_DIR")); value != "" {
		return expandHomePath(value, home)
	}
	return configDirectory(home)
}

func projectDirectoryName(cwd string) string {
	var name strings.Builder
	for _, codeUnit := range utf16.Encode([]rune(cwd)) {
		if codeUnit >= 'a' && codeUnit <= 'z' || codeUnit >= 'A' && codeUnit <= 'Z' || codeUnit >= '0' && codeUnit <= '9' {
			name.WriteByte(byte(codeUnit))
		} else {
			name.WriteByte('-')
		}
	}
	return name.String()
}

func expandHomePath(path, home string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	if home == "" {
		return ""
	}
	parts := strings.FieldsFunc(strings.TrimPrefix(path, "~"), func(separator rune) bool {
		return separator == '/' || separator == '\\'
	})
	return filepath.Join(append([]string{home}, parts...)...)
}
