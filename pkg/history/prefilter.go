package history

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	asciiWordBytes       = 8
	asciiHighBits        = uint64(0x8080808080808080)
	unicodeEscapeBytes   = len(`\u0000`)
	surrogateEscapeBytes = 2 * unicodeEscapeBytes
)

type bytePattern struct {
	literal       string
	text          []byte
	anchor        int
	caseSensitive bool
	unicode       bool
	slash         bool
}

type bytePrefilter struct {
	patterns []bytePattern
	overlap  int
}

func newBytePrefilter(terms []matcher) *bytePrefilter {
	filter := &bytePrefilter{patterns: nil, overlap: surrogateEscapeBytes}

	for _, term := range terms {
		if term.literal == "" {
			continue
		}

		literal := asciiFragment(term.literal)

		var encoded bytes.Buffer

		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)

		if err := encoder.Encode(literal); err != nil {
			panic(err)
		}

		data := encoded.Bytes()
		pattern := bytePattern{
			literal:       literal,
			anchor:        0,
			text:          data[1 : len(data)-2],
			caseSensitive: term.caseSensitive,
			unicode:       !isASCII([]byte(literal)),
			slash:         strings.Contains(literal, "/"),
		}
		pattern.anchor = rareByte(pattern.text)
		filter.overlap = max(filter.overlap, len(pattern.text)*utf8.UTFMax)
		filter.patterns = append(filter.patterns, pattern)
	}

	return filter
}

func (filter *bytePrefilter) matches(ctx context.Context, input io.Reader) (bool, error) {
	if filter == nil || len(filter.patterns) == 0 {
		return true, nil
	}

	found := make([]bool, len(filter.patterns))
	foldedRunes := make(map[rune]bool)
	remaining := len(found)
	buffer := make([]byte, hashChunkBytes+filter.overlap)
	retained := 0

	for {
		if err := ctx.Err(); err != nil {
			return false, fmt.Errorf("prefilter history: %w", err)
		}

		count, err := input.Read(buffer[retained:])
		chunk := buffer[:retained+count]

		remaining -= filter.markChunk(chunk, found, foldedRunes)
		if remaining == 0 {
			return true, nil
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}

			return false, fmt.Errorf("prefilter history: %w", err)
		}

		retained = min(len(chunk), filter.overlap)
		copy(buffer, chunk[len(chunk)-retained:])
	}
}

func (filter *bytePrefilter) markChunk(chunk []byte, found []bool, foldedRunes map[rune]bool) int {
	escaped := bytes.Contains(chunk, []byte(`\u`))
	ascii := isASCII(chunk)
	matched := 0

	for index, pattern := range filter.patterns {
		if found[index] {
			continue
		}

		if pattern.rawCandidate(chunk, ascii, escaped) || pattern.matches(chunk) ||
			(!ascii && !pattern.caseSensitive && !pattern.unicode && pattern.foldedCandidate(chunk, foldedRunes)) {
			found[index] = true
			matched++
		}
	}

	return matched
}

func (pattern bytePattern) foldedCandidate(data []byte, foldedRunes map[rune]bool) bool {
	for offset := 0; offset < len(data); {
		offset += asciiPrefix(data[offset:])
		if offset == len(data) {
			break
		}

		character, width := utf8.DecodeRune(data[offset:])
		start := offset
		offset += width

		contributes, known := foldedRunes[character]
		if !known {
			contributes = foldsToASCII(character)
			foldedRunes[character] = contributes
		}

		if !contributes {
			continue
		}

		margin := len(pattern.text) * utf8.UTFMax

		window := data[max(0, start-margin):min(len(data), offset+margin)]
		if pattern.matches([]byte(fold(string(window)))) {
			return true
		}
	}

	return false
}

func foldsToASCII(character rune) bool {
	for _, value := range fold(string(character)) {
		if value < utf8.RuneSelf {
			return true
		}
	}

	return false
}

func (pattern bytePattern) rawCandidate(data []byte, ascii bool, escaped bool) bool {
	return (pattern.unicode && !ascii) ||
		(escaped && pattern.escapedCandidate(data)) ||
		(pattern.slash && bytes.Contains(data, []byte(`\/`)))
}

func (pattern bytePattern) escapedCandidate(data []byte) bool {
	if pattern.unicode {
		return true
	}

	for start := 0; start+unicodeEscapeBytes <= len(data); {
		offset := bytes.Index(data[start:], []byte(`\u`))
		if offset < 0 {
			return false
		}

		start += offset
		if start+6 > len(data) {
			return false
		}

		value, width := escapedRune(data[start:])
		start += width

		if pattern.escapedRune(value) {
			return true
		}
	}

	return false
}

func (pattern bytePattern) escapedRune(value rune) bool {
	if pattern.caseSensitive {
		return value >= 0 && value < utf8.RuneSelf &&
			strings.IndexByte(pattern.literal, byte(value)) >= 0
	}

	for _, letter := range fold(string(value)) {
		if letter >= 0 && letter < utf8.RuneSelf &&
			strings.IndexByte(pattern.literal, byte(letter)) >= 0 {
			return true
		}
	}

	return false
}

func escapedRune(data []byte) (rune, int) {
	value, err := strconv.ParseUint(string(data[2:unicodeEscapeBytes]), 16, 16)
	if err != nil {
		return utf8.RuneError, unicodeEscapeBytes
	}

	first := rune(value)
	if !utf16.IsSurrogate(first) || len(data) < surrogateEscapeBytes ||
		!bytes.Equal(data[unicodeEscapeBytes:unicodeEscapeBytes+2], []byte(`\u`)) {
		return first, unicodeEscapeBytes
	}

	second, err := strconv.ParseUint(
		string(data[unicodeEscapeBytes+2:surrogateEscapeBytes]),
		16,
		16,
	)
	if err != nil {
		return first, unicodeEscapeBytes
	}

	decoded := utf16.DecodeRune(first, rune(second))
	if decoded == utf8.RuneError {
		return first, unicodeEscapeBytes
	}

	return decoded, surrogateEscapeBytes
}

func asciiFragment(text string) string {
	longest, start := "", 0

	for index := 0; index <= len(text); index++ {
		if index < len(text) && text[index] < utf8.RuneSelf {
			continue
		}

		if index-start > len(longest) {
			longest = text[start:index]
		}

		start = index + 1
	}

	if longest == "" {
		return text
	}

	return longest
}

func (pattern bytePattern) matches(data []byte) bool {
	anchor := pattern.text[pattern.anchor]
	for start := 0; start+len(pattern.text) <= len(data); {
		lower := bytes.IndexByte(data[start+pattern.anchor:], anchor)
		if !pattern.caseSensitive && anchor >= 'a' && anchor <= 'z' {
			upper := bytes.IndexByte(data[start+pattern.anchor:], anchor-'a'+'A')
			if lower < 0 || (upper >= 0 && upper < lower) {
				lower = upper
			}
		}

		if lower < 0 {
			return false
		}

		start += lower
		if start+len(pattern.text) > len(data) {
			return false
		}

		if pattern.equal(data[start : start+len(pattern.text)]) {
			return true
		}

		start++
	}

	return false
}

func (pattern bytePattern) equal(data []byte) bool {
	for index, value := range data {
		if !pattern.caseSensitive && value >= 'A' && value <= 'Z' {
			value += 'a' - 'A'
		}

		if value != pattern.text[index] {
			return false
		}
	}

	return true
}

func rareByte(text []byte) int {
	const frequency = "zqxjkvbpygfwmucldrhsnioate \\\""

	anchor, rarity := 0, len(frequency)
	for index, value := range text {
		rank := strings.IndexByte(frequency, value)
		if rank < rarity {
			anchor, rarity = index, rank
		}
	}

	return anchor
}

func isASCII(data []byte) bool {
	return asciiPrefix(data) == len(data)
}

func asciiPrefix(data []byte) int {
	offset := 0
	for len(data)-offset >= asciiWordBytes {
		if binary.LittleEndian.Uint64(data[offset:])&asciiHighBits != 0 {
			break
		}

		offset += asciiWordBytes
	}

	for offset < len(data) && data[offset] < utf8.RuneSelf {
		offset++
	}

	return offset
}

func (filter *bytePrefilter) matchesFile(ctx context.Context, file *os.File) (bool, error) {
	if filter == nil || len(filter.patterns) == 0 {
		return true, nil
	}

	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("stat history candidate: %w", err)
	}

	candidate, scanErr := filter.matches(ctx, io.NewSectionReader(file, 0, info.Size()))

	return candidate, scanErr
}
