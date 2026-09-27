package lock

import (
	"strings"
	"testing"
)

func TestGenerateChallengeLengthAndCharset(t *testing.T) {
	const n = 200
	c, err := GenerateChallenge(n)
	if err != nil {
		t.Fatalf("GenerateChallenge error: %v", err)
	}
	if len(c) != n {
		t.Fatalf("length = %d; want %d", len(c), n)
	}
	for i := 0; i < len(c); i++ {
		if !strings.ContainsRune(charset, rune(c[i])) {
			t.Fatalf("character %q at %d not in charset", c[i], i)
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

func TestFinishAndCompare(t *testing.T) {
	if !finishAndCompare("abc123", "abc123") {
		t.Error("matching input should return true")
	}
	if finishAndCompare("abc", "abc123") {
		t.Error("too-short input should return false")
	}
	if finishAndCompare("abc123xy", "abc123") {
		t.Error("too-long input should return false")
	}
	if finishAndCompare("abX123", "abc123") {
		t.Error("mismatched input should return false")
	}
}

func TestFormatChallengeWraps(t *testing.T) {
	text := strings.Repeat("a", 150)
	got := formatChallenge(text, 70)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines for 150 chars at width 70, got %d", len(lines))
	}
	if len(lines[0]) != 70 || len(lines[1]) != 70 || len(lines[2]) != 10 {
		t.Errorf("line lengths = %d/%d/%d; want 70/70/10", len(lines[0]), len(lines[1]), len(lines[2]))
	}
	if strings.Join(lines, "") != text {
		t.Error("wrapping changed the content")
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
