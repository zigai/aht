package processinfo

import (
	"os"
	"strconv"
	"strings"
)

const (
	lsofControlEscapes = "bfnrt"
	lsofControlBytes   = "\b\f\n\r\t"
	lsofEscapeWidth    = 2
	lsofHexEscapeWidth = 4
	lsofHighestByte    = 0xff
)

func darwinLsofPath(name string) string {
	decoded := decodeDarwinLsofName(name)
	if decoded == name {
		return name
	}

	// lsof escapes unprintable bytes but prints a literal ^ or \ unchanged, so
	// an escape-like name is only decoded when no file has that exact name.
	if _, err := os.Lstat(name); err == nil {
		return name
	}

	return decoded
}

func decodeDarwinLsofName(name string) string {
	if !strings.ContainsAny(name, `^\`) {
		return name
	}

	var decoded strings.Builder

	for len(name) > 0 {
		char, width := decodeDarwinLsofByte(name)
		decoded.WriteByte(char)

		name = name[width:]
	}

	return decoded.String()
}

func decodeDarwinLsofByte(text string) (byte, int) {
	if len(text) < lsofEscapeWidth {
		return text[0], 1
	}

	switch text[0] {
	case '^':
		if text[1] == '?' {
			return lsofHighestByte, lsofEscapeWidth
		}

		if text[1] >= '@' && text[1] <= '_' {
			return text[1] - '@', lsofEscapeWidth
		}
	case '\\':
		if index := strings.IndexByte(lsofControlEscapes, text[1]); index >= 0 {
			return lsofControlBytes[index], lsofEscapeWidth
		}

		if text[1] == 'x' && len(text) >= lsofHexEscapeWidth {
			value, err := strconv.ParseUint(text[lsofEscapeWidth:lsofHexEscapeWidth], 16, 8)
			if err == nil {
				return byte(value), lsofHexEscapeWidth
			}
		}
	}

	return text[0], 1
}
