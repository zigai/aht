package processinfo

import "strings"

func normalizeDarwinTTY(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "??" || raw == "-" {
		return ""
	}

	if strings.HasPrefix(raw, "/") {
		return raw
	}

	return "/dev/" + raw
}
