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
