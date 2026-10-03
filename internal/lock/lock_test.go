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

func TestScore(t *testing.T) {
	target := "aB3$eF7&iJ1!mN0pQr5t" // 20 chars
	if got := score(target, target); got != 1 {
		t.Errorf("perfect input scored %v; want 1", got)
	}
	// Two errors (a substitution and a skipped character) over 20: 90%.
	if got := score("aB3$eF7&iJ1!mN0pqr5", target); got < 0.899 || got > 0.901 {
		t.Errorf("two-error input scored %v; want 0.90", got)
	}
	if got := score("", target); got != 0 {
		t.Errorf("empty input scored %v; want 0", got)
	}
	// Garbage can't go below zero.
	if got := score(strings.Repeat("z", 100), target); got != 0 {
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

func TestEchoWrapping(t *testing.T) {
	const w = 4
	// Replaying echoAppend for "abcdefghi" wraps after every 4 characters.
	var out strings.Builder
	typed := []byte{}
	for _, c := range []byte("abcdefghi") {
		typed = append(typed, c)
		out.WriteString(echoAppend(typed, w))
	}
	if got := out.String(); got != "abcd\r\nefgh\r\ni" {
		t.Errorf("echoAppend output = %q", got)
	}

	// Removing a character that was alone on its line goes back up and
	// redraws the previous full line.
	if got := echoBackspace([]byte("abcdefgh"), w); got != ansiClearLine+ansiUp+"\refgh" {
		t.Errorf("echoBackspace at line start = %q", got)
	}
	// Otherwise just the current line is redrawn.
	if got := echoBackspace([]byte("abcdef"), w); got != ansiClearLine+"ef" {
		t.Errorf("echoBackspace mid-line = %q", got)
	}
	if got := echoBackspace([]byte(""), w); got != ansiClearLine {
		t.Errorf("echoBackspace to empty = %q", got)
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
