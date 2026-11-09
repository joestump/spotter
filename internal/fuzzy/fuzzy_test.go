package fuzzy

import (
	"math"
	"testing"
)

// Governing: SPEC track-matching REQ-TM-031, REQ-TM-032; ADR-0014.
// Issue #330: similarity() used to divide a rune-based Levenshtein distance by
// a byte-based max length, inflating scores 2-3x for non-ASCII titles. Issue
// #340: the vibes generator carried its own copy of these helpers and its
// normalization dropped every non-ASCII letter. These tests pin the shared
// behavior both consumers now depend on.

func sim(a, b string) float64 {
	return Similarity([]rune(a), []rune(b))
}

func TestSimilarity_CJK_DissimilarTitlesScoreZero(t *testing.T) {
	// "夜に駆ける" (5 runes) vs "群青" (2 runes): every rune differs.
	// Rune-based: distance 5 / maxLen 5 = similarity 0.0.
	// Byte-based maxLen (15 bytes) would have inflated this to ~0.67.
	score := sim("夜に駆ける", "群青")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar CJK titles, got %f", score)
	}

	// "君の名は" vs "千本桜": no runes in common.
	score = sim("君の名は", "千本桜")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar CJK titles, got %f", score)
	}
}

func TestSimilarity_Cyrillic_DissimilarTitlesScoreLow(t *testing.T) {
	// "калинка" vs "катюша": shares only leading "ка" and trailing "а".
	// Rune-based maxLen is 7; byte-based would have been 14, halving the
	// apparent distance ratio.
	score := sim("калинка", "катюша")
	if score > 0.45 {
		t.Fatalf("expected low similarity for dissimilar Cyrillic titles, got %f", score)
	}

	// Fully dissimilar Cyrillic strings should score ~0.0.
	score = sim("жизнь", "туман")
	if score > 0.05 {
		t.Fatalf("expected ~0.0 similarity for dissimilar Cyrillic titles, got %f", score)
	}
}

func TestSimilarity_Identical(t *testing.T) {
	for _, s := range []string{"hello", "夜に駆ける", "калинка", ""} {
		if got := sim(s, s); got != 1.0 {
			t.Fatalf("expected 1.0 for identical strings %q, got %f", s, got)
		}
	}
}

func TestSimilarity_Empty(t *testing.T) {
	if got := sim("", "abc"); got != 0.0 {
		t.Fatalf("expected 0.0 for empty vs non-empty, got %f", got)
	}
	if got := sim("abc", ""); got != 0.0 {
		t.Fatalf("expected 0.0 for non-empty vs empty, got %f", got)
	}
}

func TestSimilarity_ASCII_UnchangedByRuneFix(t *testing.T) {
	// ASCII behavior must be identical to the previous implementation
	// (bytes == runes for ASCII): "kitten" vs "sitting" has distance 3,
	// maxLen 7 -> 1 - 3/7.
	got := sim("kitten", "sitting")
	want := 1.0 - 3.0/7.0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("expected %f for kitten/sitting, got %f", want, got)
	}
}

func TestSimilarity_SimilarStrings(t *testing.T) {
	for _, pair := range [][2]string{{"hello", "hallo"}, {"hello", "Hello"}} {
		score := sim(pair[0], pair[1])
		if score < 0.7 || score > 0.9 {
			t.Fatalf("expected %q/%q to score between 0.7 and 0.9, got %f", pair[0], pair[1], score)
		}
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
		{"hello", "hallo", 1}, // one substitution
		{"abc", "xyz", 3},     // completely different
	}
	for _, c := range cases {
		if got := Levenshtein([]rune(c.a), []rune(c.b)); got != c.want {
			t.Fatalf("Levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Song Title (Remastered)", "song title"},
		{"Amazing Song (Radio Edit)", "amazing song"},
		{"Track Name - Remastered", "track name"},
		{"Ballad [Live]", "ballad"},
		{"Track Name (live)", "track name"},
		{"Song [remastered]", "song"},
		{"Song! @Name#", "song name"},
		{"Track 42", "track 42"},
		{"  Song Name  ", "song name"},
		{"Bohemian   Rhapsody", "bohemian rhapsody"},
		{"Don't Stop Me Now", "dont stop me now"},
	}
	for _, c := range cases {
		if got := Normalize(c.input); got != c.expected {
			t.Errorf("Normalize(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestNormalize_KeepsNonASCIILetters(t *testing.T) {
	// The vibes copy stripped everything outside a-z0-9, which normalized a
	// CJK library title to "" and made it unmatchable.
	for _, input := range []string{"夜に駆ける", "калинка", "Café"} {
		if got := Normalize(input); got == "" {
			t.Fatalf("Normalize(%q) collapsed to empty; non-ASCII letters must survive", input)
		}
	}
	if got := Normalize("Café"); got != "café" {
		t.Errorf("Normalize(\"Café\") = %q, want %q", got, "café")
	}
}

func TestNormalize_SuffixVariantsCollapse(t *testing.T) {
	// Known remaster/suffix variants must still clear the 0.7 default
	// threshold after normalization (SPEC track-matching REQ-TM-041).
	variants := []struct{ source, library string }{
		{"Song Title (Remastered)", "Song Title"},
		{"Amazing Song (Radio Edit)", "Amazing Song"},
		{"Track Name - Remastered", "Track Name"},
		{"Ballad [Live]", "Ballad"},
	}
	for _, v := range variants {
		score := Similarity(NormalizeRunes(v.source), NormalizeRunes(v.library))
		if score < 0.99 {
			t.Fatalf("expected suffix variant %q to normalize to match %q, got similarity %f",
				v.source, v.library, score)
		}
	}
}

func TestNewCandidate_PreservesNonASCIIKeys(t *testing.T) {
	c := NewCandidate(7, "夜に駆ける", "YOASOBI")
	if string(c.TitleRunes) != "夜に駆ける" {
		t.Fatalf("candidate title = %q, want %q", string(c.TitleRunes), "夜に駆ける")
	}
	if string(c.ArtistRunes) != "yoasobi" {
		t.Fatalf("candidate artist = %q, want %q", string(c.ArtistRunes), "yoasobi")
	}
	if c.Match != 7 {
		t.Fatalf("candidate match = %d, want 7", c.Match)
	}
}

func TestBest_WeightsTitleOverArtist(t *testing.T) {
	candidates := []Candidate[int]{
		NewCandidate(1, "Hello", "Adele"),
		NewCandidate(2, "Halo", "Adele"),
	}

	// Same artist for both, so the weighted title score decides the winner.
	i, score := Best(NormalizeRunes("Halo"), NormalizeRunes("Beyonce"), candidates)
	if i != 1 {
		t.Fatalf("expected candidate 1 (index 1), got %d at score %f", i, score)
	}

	i, score = Best(NormalizeRunes("Hello"), NormalizeRunes("Beyonce"), candidates)
	if i != 0 {
		t.Fatalf("expected candidate 0 (index 0), got %d at score %f", i, score)
	}
}

func TestBest_AppliesDualConfidenceBonus(t *testing.T) {
	candidates := []Candidate[string]{NewCandidate("near", "Yesterday", "Beatles")}

	// One rune differs in each key, so both scores clear 0.8 and the
	// dual-confidence bonus applies (SPEC track-matching REQ-TM-032).
	titleRunes := NormalizeRunes("Yesterda")
	artistRunes := NormalizeRunes("Beattles")
	base := 0.6*Similarity(titleRunes, candidates[0].TitleRunes) +
		0.4*Similarity(artistRunes, candidates[0].ArtistRunes)

	i, score := Best(titleRunes, artistRunes, candidates)
	if i != 0 {
		t.Fatalf("expected the only candidate, got index %d", i)
	}
	if score < base+0.09 || score > base+0.11 {
		t.Fatalf("expected the dual-confidence bonus (+0.1) on top of %f, got %f", base, score)
	}
}

func TestBest_NoBonusWhenOneKeyIsWeak(t *testing.T) {
	candidates := []Candidate[string]{NewCandidate("near", "Yesterday", "The Beatles")}

	// The artist score is far below 0.8, so the score stays the plain
	// weighted average.
	titleRunes := NormalizeRunes("Yesterda")
	artistRunes := NormalizeRunes("Beattles")
	base := 0.6*Similarity(titleRunes, candidates[0].TitleRunes) +
		0.4*Similarity(artistRunes, candidates[0].ArtistRunes)

	_, score := Best(titleRunes, artistRunes, candidates)
	if math.Abs(score-base) > 1e-9 {
		t.Fatalf("expected no bonus: got %f, weighted average is %f", score, base)
	}
}

func TestBest_NoCandidateScores(t *testing.T) {
	candidates := []Candidate[string]{
		NewCandidate("a", "完全な別人", "別のアーティスト"),
	}

	i, score := Best(NormalizeRunes("Hello"), NormalizeRunes("Adele"), candidates)
	if i != -1 {
		t.Fatalf("expected no winner, got index %d at score %f", i, score)
	}
	if score != 0.0 {
		t.Fatalf("expected zero score with no candidate, got %f", score)
	}

	if i, score := Best[string](NormalizeRunes("Hello"), NormalizeRunes("Adele"), nil); i != -1 || score != 0.0 {
		t.Fatalf("expected no winner for an empty candidate list, got %d at %f", i, score)
	}
}

func TestBest_ScoreIsCappedAtOne(t *testing.T) {
	candidates := []Candidate[string]{NewCandidate("exact", "Hello", "Adele")}

	_, score := Best(NormalizeRunes("Hello"), NormalizeRunes("Adele"), candidates)
	if score != 1.0 {
		t.Fatalf("expected an exact match to score exactly 1.0, got %f", score)
	}
}
