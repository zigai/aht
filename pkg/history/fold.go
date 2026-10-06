package history

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

// Case-insensitive matching happens only through the functions in this file.
// Readers and the index writer store fold(body), index queries fold the needle,
// matchText folds the body it searches, and excerpt windows anchored on a folded
// offset are mapped back with foldRuneIndex. Keeping normalization in one seam
// means a change to the folding contract cannot leave the direct scan, the
// disposable index, and the CLI disagreeing about what matches.

// foldCaser implements full Unicode case folding. cases.Fold documents the caser
// as stateless and safe for concurrent use, so one shared value is enough.
var foldCaser = cases.Fold()

// fold normalizes text for case-insensitive literal matching. Simple lowercasing
// runs first so Turkish dotted İ and ASCII I keep matching ASCII i (full case
// folding alone would map İ to i followed by a combining dot), then full Unicode
// case folding equates ß with ss, final and medial sigma with each other, and
// ligatures such as ﬃ with ffi. Folding can lengthen its input, so callers must
// translate offsets with foldRuneIndex instead of comparing rune counts.
func fold(text string) string {
	return foldCaser.String(strings.ToLower(text))
}

// foldRuneIndex maps a rune offset in fold(text) to the smallest rune index i
// such that fold(text[:i]) contains at least foldedOffset runes. An expanding
// fold such as ß -> ss advances the folded offset faster than the unfolded rune
// index, so excerpt windows anchored on folded text are mapped through this
// function. The walk is linear in the length of text and never panics: negative
// offsets clamp to zero, offsets beyond the folded text clamp to the rune count,
// and the result is always a valid index into []rune(text).
func foldRuneIndex(text string, foldedOffset int) int {
	if foldedOffset <= 0 {
		return 0
	}
	covered, index := 0, 0
	for _, character := range text {
		if character < utf8.RuneSelf {
			covered++
		} else {
			covered += utf8.RuneCountInString(fold(string(character)))
		}
		index++
		if covered >= foldedOffset {
			return index
		}
	}
	return index
}

// foldWithOffsets folds text rune by rune and maps each byte of the folded
// result to the byte offset in text of the rune that produced it, plus a final
// entry for the end of text. Matches found in folded text map back through it.
func foldWithOffsets(text string) (string, []int) {
	var folded strings.Builder
	folded.Grow(len(text))
	starts := make([]int, 0, len(text)+1)
	for offset, character := range text {
		mapped := fold(string(character))
		folded.WriteString(mapped)
		for range len(mapped) {
			starts = append(starts, offset)
		}
	}
	starts = append(starts, len(text))
	return folded.String(), starts
}

// originalEnd maps the exclusive end of a nonempty folded match to the end of
// the original rune that produced its last byte.
func originalEnd(text string, starts []int, end int) int {
	start := starts[end-1]
	_, size := utf8.DecodeRuneInString(text[start:])
	return start + size
}
