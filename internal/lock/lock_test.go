package lock

import (
	"strings"
	"testing"
)

func TestWordList(t *testing.T) {
	if len(wordList) < 500 {
		t.Fatalf("wordList has %d words; want at least 500 so challenges stay unpredictable", len(wordList))
	}
	seen := make(map[string]bool, len(wordList))
	for _, w := range wordList {
		if len(w) < 3 || len(w) > 8 {
			t.Errorf("word %q has length %d; want 3-8", w, len(w))
		}
		for _, r := range w {
			if r < 'a' || r > 'z' {
				t.Errorf("word %q contains %q; want lowercase a-z only", w, r)
				break
			}
		}
		if seen[w] {
			t.Errorf("duplicate word %q", w)
		}
		seen[w] = true
	}
}

func TestGenerateChallengeShape(t *testing.T) {
	for _, n := range []int{MinLockChars, 200, DefaultLockChars} {
		c, err := GenerateChallenge(n)
		if err != nil {
			t.Fatalf("GenerateChallenge(%d) error: %v", n, err)
		}
		// Whole words are added until the length is reached, so it may run
		// over by at most one word plus its space.
		if len(c) < n || len(c) > n+9 {
			t.Errorf("GenerateChallenge(%d) length = %d; want %d..%d", n, len(c), n, n+9)
		}
		if strings.Contains(c, "  ") || strings.HasPrefix(c, " ") || strings.HasSuffix(c, " ") {
			t.Errorf("challenge %q has stray spaces", c)
		}
		for _, w := range strings.Split(c, " ") {
			if !contains(wordList, w) {
				t.Fatalf("challenge word %q is not in wordList", w)
			}
		}
	}
}

func TestGenerateChallengeRejectsNonPositive(t *testing.T) {
	for _, n := range []int{0, -1, -300} {
		if _, err := GenerateChallenge(n); err == nil {
			t.Errorf("GenerateChallenge(%d) = nil error; want error", n)
		}
	}
}

func TestGenerateChallengeRandomness(t *testing.T) {
	a, _ := GenerateChallenge(100)
	b, _ := GenerateChallenge(100)
	if a == b {
		t.Error("two challenges were identical; randomness is broken")
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"the quick fox", "the quik fox", 1},   // skipped letter costs 1
		{"the quick fox", "the quuick fox", 1}, // extra letter costs 1
		{"abcd", "abdc", 2},                    // transposition is two edits
		{strings.Repeat("x", 100), "abc", 100}, // oversized input: upper bound
	}
	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestScoreLines(t *testing.T) {
	lines := []string{"river cotton ask window", "planet eleven shape"} // 23 + 19 = 42 chars
	if got := scoreLines(lines, lines); got != 1 {
		t.Errorf("perfect input scored %v; want 1", got)
	}
	// Two typos over 42 chars: 1 - 2/42 ≈ 95.2%.
	typed := []string{"river coton ask window", "planet elevn shape"}
	if got := scoreLines(typed, lines); got < 0.952 || got > 0.953 {
		t.Errorf("two-typo input scored %v; want ~0.952", got)
	}
	// A missing line counts every character of it as an error.
	if got := scoreLines(lines[:1], lines); got > 0.55 {
		t.Errorf("missing line scored %v; want 23/42", got)
	}
	// Garbage can't go below zero.
	if got := scoreLines([]string{strings.Repeat("z", 100), ""}, lines); got != 0 {
		t.Errorf("garbage scored %v; want 0", got)
	}
}

func TestReportAccuracyThreshold(t *testing.T) {
	if !reportAccuracy(MinAccuracy) {
		t.Error("exactly MinAccuracy should pass")
	}
	if !reportAccuracy(1) {
		t.Error("perfect accuracy should pass")
	}
	if reportAccuracy(MinAccuracy - 0.001) {
		t.Error("below MinAccuracy should fail")
	}
}

func TestSplitLines(t *testing.T) {
	text := "alpha beta gamma delta epsilon zeta eta theta"
	lines := splitLines(text, 16)
	for _, l := range lines {
		if len(l) > 16 {
			t.Errorf("line %q is longer than 16", l)
		}
		if strings.HasPrefix(l, " ") || strings.HasSuffix(l, " ") {
			t.Errorf("line %q has edge spaces", l)
		}
	}
	if strings.Join(lines, " ") != text {
		t.Errorf("splitting changed the content: %q", lines)
	}
	// A word wider than the limit gets its own line instead of being cut.
	if got := splitLines("a verylongword b", 5); len(got) != 3 || got[1] != "verylongword" {
		t.Errorf("splitLines with an overlong word = %q", got)
	}
}

func TestRenderTypedMarksMistakes(t *testing.T) {
	got := renderTyped([]byte("rivxr"), "river")
	want := ansiClearLine + "> riv" + ansiRed + "x" + ansiReset + "r"
	if got != want {
		t.Errorf("renderTyped = %q; want %q", got, want)
	}
	// Characters past the end of the target are wrong.
	got = renderTyped([]byte("ab!"), "ab")
	want = ansiClearLine + "> ab" + ansiRed + "!" + ansiReset
	if got != want {
		t.Errorf("renderTyped overflow = %q; want %q", got, want)
	}
}

func TestMinLockChars(t *testing.T) {
	if MinLockChars <= 0 {
		t.Errorf("MinLockChars = %d; must be positive", MinLockChars)
	}
	if DefaultLockChars < MinLockChars {
		t.Errorf("DefaultLockChars (%d) must be >= MinLockChars (%d)", DefaultLockChars, MinLockChars)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
