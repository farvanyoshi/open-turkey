package lock

import (
	"strings"
	"testing"
)

func TestCharClasses(t *testing.T) {
	if len(letters) != 52 || len(digits) != 10 {
		t.Errorf("letters/digits have %d/%d characters; want 52/10", len(letters), len(digits))
	}
	// Every printable ASCII character except space is in exactly one class.
	all := letters + digits + symbols
	for c := byte('!'); c <= '~'; c++ {
		if n := strings.Count(all, string(c)); n != 1 {
			t.Errorf("character %q appears %d times across the classes; want 1", c, n)
		}
	}
	if len(all) != int('~'-'!'+1) {
		t.Errorf("classes hold %d characters; want %d", len(all), '~'-'!'+1)
	}
}

func TestGenerateChallengeShape(t *testing.T) {
	for _, n := range []int{MinLockChars, 200, DefaultLockChars} {
		c, err := GenerateChallenge(n)
		if err != nil {
			t.Fatalf("GenerateChallenge(%d) error: %v", n, err)
		}
		if len(c) != n {
			t.Errorf("GenerateChallenge(%d) length = %d", n, len(c))
		}
		for i := 0; i < len(c); i++ {
			if c[i] <= ' ' || c[i] > '~' {
				t.Fatalf("challenge has non-printable or space character %q", c[i])
			}
		}
	}
}

// TestGenerateChallengeWeights checks the class mix is roughly 60/35/5. With
// 200k samples the standard deviation is ~0.1 percentage points, so ±1 point
// never fails by chance.
func TestGenerateChallengeWeights(t *testing.T) {
	const n = 200000
	c, err := GenerateChallenge(n)
	if err != nil {
		t.Fatal(err)
	}
	var l, d, s int
	for i := 0; i < len(c); i++ {
		switch {
		case strings.IndexByte(letters, c[i]) >= 0:
			l++
		case strings.IndexByte(digits, c[i]) >= 0:
			d++
		default:
			s++
		}
	}
	for _, x := range []struct {
		name string
		got  int
		want float64
	}{{"letters", l, 60}, {"digits", d, 35}, {"symbols", s, 5}} {
		pct := float64(x.got) * 100 / n
		if pct < x.want-1 || pct > x.want+1 {
			t.Errorf("%s are %.2f%% of the challenge; want %.0f%% ± 1", x.name, pct, x.want)
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
	text := "aB3$eF7&iJ1!mN"
	lines := splitLines(text, 5)
	want := []string{"aB3$e", "F7&iJ", "1!mN"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("splitLines = %q; want %q", lines, want)
	}
	if got := splitLines("abcde", 5); len(got) != 1 || got[0] != "abcde" {
		t.Errorf("splitLines exact width = %q", got)
	}
	if got := splitLines("", 5); len(got) != 0 {
		t.Errorf("splitLines empty = %q", got)
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
