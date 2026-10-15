package history

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type bytePattern struct {
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
	filter := &bytePrefilter{overlap: utf8.UTFMax}
	for _, term := range terms {
		if term.literal == "" {
			continue
		}
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(term.literal)
		data := encoded.Bytes()
		pattern := bytePattern{
			text: data[1 : len(data)-2],
			caseSensitive: term.caseSensitive,
			unicode: !isASCII([]byte(term.literal)),
			slash: strings.Contains(term.literal, "/"),
		}
		pattern.anchor = rareByte(pattern.text)
		filter.overlap = max(filter.overlap, len(pattern.text)*utf8.UTFMax)
		filter.patterns = append(filter.patterns, pattern)
	}
	return filter
}

func (filter *bytePrefilter) matches(ctx context.Context, input io.Reader) (bool, error) {
	if filter == nil || len(filter.patterns) == 0 {
		return true, ctx.Err()
	}
	found := make([]bool, len(filter.patterns))
	remaining := len(found)
	buffer := make([]byte, hashChunkBytes+filter.overlap)
	retained := 0
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		count, err := input.Read(buffer[retained:])
		chunk := buffer[:retained+count]
		if bytes.Contains(chunk, []byte(`\u`)) {
			return true, nil
		}
		ascii := isASCII(chunk)
		var folded []byte
		for index, pattern := range filter.patterns {
			if found[index] {
				continue
			}
			candidate := chunk
			if !ascii && !pattern.caseSensitive && !pattern.unicode {
				if folded == nil {
					folded = []byte(fold(string(chunk)))
				}
				candidate = folded
			}
			if (pattern.unicode && !ascii) || (pattern.slash && bytes.Contains(chunk, []byte(`\/`))) || pattern.matches(candidate) {
				found[index] = true
				remaining--
			}
		}
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
	for _, value := range data {
		if value >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func (filter *bytePrefilter) matchesFile(ctx context.Context, file *os.File) (bool, error) {
	if filter == nil || len(filter.patterns) == 0 {
		return true, ctx.Err()
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	candidate, scanErr := filter.matches(ctx, io.NewSectionReader(file, 0, info.Size()))
	return candidate, scanErr
}
