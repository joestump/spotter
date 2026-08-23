package services

import (
	"testing"
)

// Governing: SPEC track-matching REQ-TM-031 (similarity measured in rune units)
// Issue #330: similarity() previously divided a rune-based Levenshtein
// distance by a byte-based max length, inflating scores 2-3x for non-ASCII
// titles. These tests pin the rune-based behavior.

func runeSim(a, b string) float64 {
	return similarity([]rune(a), []rune(b))
}

func TestSimilarity_CJK_DissimilarTitlesScoreZero(t *testing.T) {
	// "夜に駆ける" (5 runes) vs "群青" (2 runes): every rune differs.
	// Rune-based: distance 5 / maxLen 5 = similarity 0.0.
	// Byte-based maxLen (15 bytes) would have inflated this to ~0.67.
	score := runeSim("夜に駆ける", "群青")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar CJK titles, got %f", score)
	}

	// "君の名は" vs "千本桜": no runes in common.
	score = runeSim("君の名は", "千本桜")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar CJK titles, got %f", score)
	}
}

func TestSimilarity_Cyrillic_DissimilarTitlesScoreLow(t *testing.T) {
	// "калинка" vs "катюша": shares only leading "ка" and trailing "а".
	// Rune-based maxLen is 7; byte-based would have been 14, halving the
	// apparent distance ratio.
	score := runeSim("калинка", "катюша")
	if score > 0.45 {
		t.Fatalf("expected low similarity for dissimilar Cyrillic titles, got %f", score)
	}

	// Fully dissimilar Cyrillic strings should score ~0.0.
	score = runeSim("жизнь", "туман")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar Cyrillic titles, got %f", score)
	}
}

func TestSimilarity_Identical(t *testing.T) {
	for _, s := range []string{"hello", "夜に駆ける", "калинка", ""} {
		if got := runeSim(s, s); got != 1.0 {
			t.Fatalf("expected 1.0 for identical strings %q, got %f", s, got)
		}
	}
}

func TestSimilarity_Empty(t *testing.T) {
	if got := runeSim("", "abc"); got != 0.0 {
		t.Fatalf("expected 0.0 for empty vs non-empty, got %f", got)
	}
	if got := runeSim("abc", ""); got != 0.0 {
		t.Fatalf("expected 0.0 for non-empty vs empty, got %f", got)
	}
}

func TestSimilarity_ASCII_UnchangedByRuneFix(t *testing.T) {
	// ASCII behavior must be identical to the previous implementation
	// (bytes == runes for ASCII): "kitten" vs "sitting" has distance 3,
	// maxLen 7 -> 1 - 3/7.
	got := runeSim("kitten", "sitting")
	want := 1.0 - 3.0/7.0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected %f for kitten/sitting, got %f", want, got)
	}
}

func TestLevenshtein_RuneUnits(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"夜に駆ける", "群青", 5},    // all 5 runes replaced/deleted
		{"君の名は", "君の名は", 0},   // identical CJK
		{"кошка", "мошка", 1}, // single rune substitution
	}
	for _, c := range cases {
		if got := levenshtein([]rune(c.a), []rune(c.b)); got != c.want {
			t.Fatalf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestFindBestFuzzyMatch_SuffixVariantsStillMatch(t *testing.T) {
	// Known remaster/suffix variants must still clear the 0.7 default
	// threshold after normalization (SPEC track-matching REQ-TM-041).
	variants := []struct {
		source  string
		library string
	}{
		{"Song Title (Remastered)", "Song Title"},
		{"Amazing Song (Radio Edit)", "Amazing Song"},
		{"Track Name - Remastered", "Track Name"},
		{"Ballad [Live]", "Ballad"},
	}
	for _, v := range variants {
		src := normalizeForMatch(v.source)
		lib := normalizeForMatch(v.library)
		score := runeSim(src, lib)
		if score < 0.99 {
			t.Fatalf("expected suffix variant %q to normalize to match %q, got similarity %f",
				v.source, v.library, score)
		}
	}
}
