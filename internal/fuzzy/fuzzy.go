// Package fuzzy is the single implementation of Spotter's track-name
// similarity scoring: normalization, Levenshtein-based similarity and the
// weighted best-candidate search.
//
// It exists because two consumers used to carry their own copy of the same
// scoring - the playlist-sync track matcher and the vibes mixtape generator -
// and the copies drifted: the matcher measured distance in runes while the
// generator divided a rune distance by a byte length, and the generator's
// normalization dropped every non-ASCII letter. Matching the same song from
// the same library therefore scored differently depending on which feature
// asked.
//
// Governing: ADR-0014 (three-tier track matching), SPEC track-matching
// REQ-TM-031 and REQ-TM-032, issue #330 (distance and max length in rune
// units), issue #340 (one stack for every consumer).
package fuzzy

import (
	"slices"
	"strings"
	"unicode"
)

// editionSuffixes are the version markers users paste into a library that say
// nothing about the recording itself, so they must not count against
// similarity.
var editionSuffixes = []string{
	"(remastered)",
	"(remaster)",
	"(deluxe)",
	"(deluxe edition)",
	"(bonus track)",
	"(bonus tracks)",
	"(radio edit)",
	"(single version)",
	"(album version)",
	"(explicit)",
	"(clean)",
	"(live)",
	"(acoustic)",
	"(remix)",
	"[remastered]",
	"[remaster]",
	"[deluxe]",
	"[deluxe edition]",
	"[bonus track]",
	"[bonus tracks]",
	"[radio edit]",
	"[single version]",
	"[album version]",
	"[explicit]",
	"[clean]",
	"[live]",
	"[acoustic]",
	"[remix]",
	" - remastered",
	" - remaster",
	" - deluxe",
	" - live",
	" - acoustic",
}

// Candidate is a match target whose keys were normalized once, up front, so
// searching a library never re-normalizes the same entry for every lookup.
// Match is whatever the caller wants back: a library track, an available
// track, an index into a slice.
type Candidate[T any] struct {
	Match       T
	TitleRunes  []rune
	ArtistRunes []rune
}

// NewCandidate normalizes title and artist once for a match target.
func NewCandidate[T any](match T, title, artist string) Candidate[T] {
	return Candidate[T]{
		Match:       match,
		TitleRunes:  NormalizeRunes(title),
		ArtistRunes: NormalizeRunes(artist),
	}
}

// Normalize lowercases s, drops edition suffixes, and keeps letters, numbers
// and single spaces. Letters are kept per rune, so non-ASCII titles survive
// normalization instead of collapsing to an empty string.
func Normalize(s string) string {
	s = strings.ToLower(s)

	for _, suffix := range editionSuffixes {
		s = strings.TrimSuffix(s, suffix)
	}

	var result strings.Builder
	lastWasSpace := false

	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			result.WriteRune(r)
			lastWasSpace = false
		} else if unicode.IsSpace(r) && !lastWasSpace {
			result.WriteRune(' ')
			lastWasSpace = true
		}
	}

	return strings.TrimSpace(result.String())
}

// NormalizeRunes is Normalize in the rune-slice form the scorer wants.
func NormalizeRunes(s string) []rune {
	return []rune(Normalize(s))
}

// Similarity compares two rune slices and returns 0.0 (completely different)
// to 1.0 (identical).
//
// Both the edit distance and the maximum length are measured in RUNES. Mixing
// a rune-based distance with a byte-based max length inflated similarity 2-3x
// for non-ASCII (CJK/Cyrillic) titles, letting wrong tracks clear the fuzzy
// threshold.
// Governing: SPEC track-matching REQ-TM-031 (issue #330)
func Similarity(a, b []rune) float64 {
	// Fast path: identical strings (including both-empty) skip the DP matrix.
	if slices.Equal(a, b) {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := Levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return 1.0 - float64(distance)/float64(maxLen)
}

// Levenshtein returns the edit distance between two rune slices, measured in
// rune edits.
func Levenshtein(aRunes, bRunes []rune) int {
	if len(aRunes) == 0 {
		return len(bRunes)
	}
	if len(bRunes) == 0 {
		return len(aRunes)
	}

	d := make([][]int, len(aRunes)+1)
	for i := range d {
		d[i] = make([]int, len(bRunes)+1)
	}

	for i := 0; i <= len(aRunes); i++ {
		d[i][0] = i
	}
	for j := 0; j <= len(bRunes); j++ {
		d[0][j] = j
	}

	for i := 1; i <= len(aRunes); i++ {
		for j := 1; j <= len(bRunes); j++ {
			cost := 1
			if aRunes[i-1] == bRunes[j-1] {
				cost = 0
			}

			d[i][j] = min(
				d[i-1][j]+1,      // deletion
				d[i][j-1]+1,      // insertion
				d[i-1][j-1]+cost, // substitution
			)
		}
	}

	return d[len(aRunes)][len(bRunes)]
}

// Best scores every candidate against already-normalized source runes and
// returns the index of the highest scorer and its score. It returns (-1, 0.0)
// when the candidate list is empty or nothing scores above zero, so callers
// treat "no candidate" and "no similarity" the same way.
//
// Title outweighs artist 0.6/0.4, and a candidate that is close on both gets
// a bonus, because an artist that matches is strong evidence the title's
// remaining differences are edition noise.
// Governing: SPEC track-matching REQ-TM-031, REQ-TM-032
func Best[T any](sourceTitle, sourceArtist []rune, candidates []Candidate[T]) (int, float64) {
	bestIndex := -1
	bestScore := 0.0

	for i, candidate := range candidates {
		titleScore := Similarity(sourceTitle, candidate.TitleRunes)
		artistScore := Similarity(sourceArtist, candidate.ArtistRunes)

		score := (titleScore * 0.6) + (artistScore * 0.4)

		if titleScore > 0.8 && artistScore > 0.8 {
			score = score + 0.1
			if score > 1.0 {
				score = 1.0
			}
		}

		if score > bestScore {
			bestScore = score
			bestIndex = i
		}
	}

	return bestIndex, bestScore
}
