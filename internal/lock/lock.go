// Package lock implements Open Turkey's "typing challenge" mechanism.
//
// === WHAT IS THIS? ===
//
// This package is the only interactive way to unlock an active locked block.
// The idea is friction: when you're tempted to reach something blocked, the
// system makes you transcribe a long run of random characters.
//
// === WHY DOES THIS WORK? ===
//
// It isn't cryptographic security — a determined user with root can always get
// around Open Turkey. The point is PSYCHOLOGICAL: the tedious transcription
// gives the impulse time to pass. The friction comes from TIME and attention:
//
//   - The text is random printable ASCII (crypto/rand): mostly letters of both
//     cases, a lot of digits and a sprinkle of symbols (see randomChar), so it
//     can't be read as words, anticipated or memorized, and keeps the hands
//     moving between Shift, the number row and the symbol keys.
//   - It is typed one line at a time with visible, colored echo and working
//     Backspace, so typos can be seen and fixed.
//   - Scoring is by edit distance with a MinAccuracy threshold, so a few typos
//     don't throw away minutes of work, and a skipped character costs one
//     error instead of misaligning everything after it.
//
// To keep that friction real, the challenge is hard to skip:
//
//   - The input is read in raw terminal mode, one keypress at a time, and
//     PASTING is detected and rejected (see readLineRaw).
//   - A minimum length is enforced so "--lock-chars 0" or a negative value
//     can't produce a trivially-empty challenge.
package lock

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// DefaultLockChars is the default challenge length in characters. 600 random
// characters takes most people well over five minutes to transcribe — long
// enough for an impulse to pass.
const DefaultLockChars = 600

// MinLockChars is the smallest challenge length we allow. It stops a lock from
// being configured with a trivially short (or empty) challenge.
const MinLockChars = 30

// MinAccuracy is the share of the challenge that must be typed correctly,
// measured as 1 - editDistance/challengeLength.
const MinAccuracy = 0.95

// maxLineWidth is the widest challenge line; narrower terminals get narrower
// lines (see lineWidthFor).
const maxLineWidth = 60

// lineSlack is how many characters past the end of a line may be typed. It
// caps each input line so it never wraps (which would break the redraw).
const lineSlack = 5

// pasteBurst is the most printable characters one read may carry before it's
// treated as a paste. Fast typing (key rollover, SSH, a loaded system) can
// deliver two or three keystrokes in one read; a paste delivers many.
const pasteBurst = 4

// ANSI sequences used while typing.
const (
	ansiRed       = "\x1b[31m"
	ansiReset     = "\x1b[0m"
	ansiClearLine = "\r\x1b[2K"
)

// Character classes for the challenge. symbols is every printable ASCII
// character on a US keyboard that isn't a letter, digit or space.
const (
	letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits  = "0123456789"
	symbols = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
)

// Class weights, in percent: a letter 60% of the time, a digit 35%, a symbol
// 5%. Within a class every character is equally likely.
const (
	letterWeight = 60
	digitWeight  = 35
	symbolWeight = 5
)

// GenerateChallenge returns length random characters drawn by randomChar.
// It errors on a non-positive length.
func GenerateChallenge(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("challenge length must be positive, got %d", length)
	}

	out := make([]byte, length)
	for i := range out {
		c, err := randomChar()
		if err != nil {
			return "", fmt.Errorf("failed to pick a random character: %w", err)
		}
		out[i] = c
	}
	return string(out), nil
}

// randomChar picks a class by the weights above, then a character uniformly
// from that class, both with crypto/rand.
func randomChar() (byte, error) {
	roll, err := randInt(letterWeight + digitWeight + symbolWeight)
	if err != nil {
		return 0, err
	}
	class := symbols
	switch {
	case roll < letterWeight:
		class = letters
	case roll < letterWeight+digitWeight:
		class = digits
	}
	i, err := randInt(len(class))
	if err != nil {
		return 0, err
	}
	return class[i], nil
}

// randInt returns a uniform random integer in [0, n) from crypto/rand.
func randInt(n int) (int, error) {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

// RunChallenge generates a challenge, has the user type it, and reports
// whether they reached MinAccuracy.
//
// On a real terminal the challenge is typed line by line in raw mode with
// paste detection. When stdin is not a terminal (e.g. piped input in tests),
// it falls back to reading one line — paste detection isn't meaningful there.
func RunChallenge(length int) (bool, error) {
	challenge, err := GenerateChallenge(length)
	if err != nil {
		return false, fmt.Errorf("failed to generate challenge: %w", err)
	}

	fmt.Println()
	fmt.Println("=== UNLOCK CHALLENGE ===")
	fmt.Println()

	fd := int(os.Stdin.Fd())
	if isTerminal(fd) {
		return runChallengeRaw(fd, challenge)
	}
	return runChallengeLine(challenge)
}

// runChallengeRaw shows the challenge one line at a time and reads each typed
// line in raw mode. Accuracy is scored over all lines together.
func runChallengeRaw(fd int, challenge string) (bool, error) {
	lines := splitLines(challenge, lineWidthFor(fd))

	fmt.Printf("Type each line (do not paste) and press Enter to continue.\n")
	fmt.Printf("%d lines, %d characters. Mistakes show in red; Backspace fixes them.\n", len(lines), len(challenge))
	fmt.Printf("You need %.0f%% accuracy overall. Ctrl-C cancels.\n", MinAccuracy*100)

	restore, err := makeRaw(fd)
	if err != nil {
		// Couldn't switch to raw mode — fall back to line reading.
		return runChallengeLine(challenge)
	}
	// Ask the terminal to bracket pasted text so we can detect it. finish
	// undoes that and leaves raw mode; it's idempotent so the defer is safe
	// after the explicit call below.
	fmt.Print("\x1b[?2004h")
	finished := false
	finish := func() {
		if !finished {
			fmt.Print("\x1b[?2004l")
			restore()
			finished = true
		}
	}
	defer finish()

	typed := make([]string, len(lines))
	for i, line := range lines {
		fmt.Printf("\r\n[%d/%d]\r\n  %s\r\n", i+1, len(lines), line)
		input, outcome, err := readLineRaw(line)
		if err != nil {
			fmt.Print("\r\n")
			return false, fmt.Errorf("failed to read input: %w", err)
		}
		switch outcome {
		case outcomePaste:
			fmt.Print("\r\nPaste detected. Unlocking requires typing the text by hand.\r\n")
			return false, nil
		case outcomeCancel:
			fmt.Print("\r\nCancelled.\r\n")
			return false, nil
		}
		typed[i] = input
	}

	fmt.Print("\r\n")
	finish() // back to cooked mode so the result prints normally
	return reportAccuracy(scoreLines(typed, lines)), nil
}

// lineOutcome says how reading one line ended.
type lineOutcome int

const (
	outcomeDone lineOutcome = iota
	outcomeCancel
	outcomePaste
)

// readLineRaw reads one line of input for target, redrawing it after every
// keypress with mistakes in red. It returns on Enter, Ctrl-C or a paste.
//
// PASTE DETECTION. In raw mode a human keypress arrives as one byte per read,
// while a paste dumps many bytes at once. So:
//   - We enable bracketed-paste mode; a paste then arrives wrapped in the
//     escape sequence ESC[200~ … ESC[201~, which we detect and reject.
//   - As a backstop (terminals that don't support bracketed paste), a single
//     read carrying more than pasteBurst printable characters is rejected.
//
// Escape sequences from real keys (arrows, etc.) begin with ESC and are ignored
// rather than counted as input.
func readLineRaw(target string) (string, lineOutcome, error) {
	maxLen := len(target) + lineSlack
	var typed []byte
	buf := make([]byte, 512)

	fmt.Print(renderTyped(typed, target))
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return "", outcomeDone, err
		}
		if n == 0 {
			continue
		}
		chunk := buf[:n]

		if bytesContains(chunk, []byte("\x1b[200~")) {
			return "", outcomePaste, nil
		}
		if chunk[0] == 0x1b {
			continue
		}

		printable := 0
		for _, b := range chunk {
			if b >= 0x20 && b < 0x7f {
				printable++
			}
		}
		if printable > pasteBurst {
			return "", outcomePaste, nil
		}

		for _, b := range chunk {
			switch {
			case b == 3: // Ctrl-C
				return "", outcomeCancel, nil
			case b == '\r' || b == '\n':
				return string(typed), outcomeDone, nil
			case b == 127 || b == 8: // Backspace / Delete
				if len(typed) > 0 {
					typed = typed[:len(typed)-1]
				}
			case b >= 0x20 && b < 0x7f: // printable ASCII
				if len(typed) < maxLen {
					typed = append(typed, b)
				}
			default:
				// ignore other control bytes
			}
		}
		fmt.Print(renderTyped(typed, target))
	}
}

// renderTyped returns the escape sequence that redraws the input line: the
// "> " prompt followed by typed, with characters that don't match target at
// the same position (or run past its end) in red.
func renderTyped(typed []byte, target string) string {
	var builder strings.Builder
	builder.WriteString(ansiClearLine)
	builder.WriteString("> ")
	red := false
	for i, b := range typed {
		wrong := i >= len(target) || b != target[i]
		if wrong != red {
			if wrong {
				builder.WriteString(ansiRed)
			} else {
				builder.WriteString(ansiReset)
			}
			red = wrong
		}
		builder.WriteByte(b)
	}
	if red {
		builder.WriteString(ansiReset)
	}
	return builder.String()
}

// runChallengeLine shows the whole challenge and reads a single line (used when
// stdin isn't a terminal).
func runChallengeLine(challenge string) (bool, error) {
	fmt.Println(strings.Join(splitLines(challenge, maxLineWidth), "\n"))
	fmt.Println()
	fmt.Printf("Type the text above on one line, without the line breaks (%.0f%% accuracy needed): ", MinAccuracy*100)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read user input: %w", err)
		}
		return false, fmt.Errorf("empty input — no text was typed")
	}
	return reportAccuracy(scoreLines([]string{scanner.Text()}, []string{challenge})), nil
}

// scoreLines returns the accuracy of typed against lines: one minus the summed
// edit distance of each pair over the total target length, floored at zero.
func scoreLines(typed, lines []string) float64 {
	distance, total := 0, 0
	for i, line := range lines {
		input := ""
		if i < len(typed) {
			input = typed[i]
		}
		distance += editDistance(input, line)
		total += len(line)
	}
	if total == 0 {
		return 0
	}
	accuracy := 1 - float64(distance)/float64(total)
	if accuracy < 0 {
		return 0
	}
	return accuracy
}

// reportAccuracy prints the result and reports whether accuracy passes.
func reportAccuracy(accuracy float64) bool {
	// The epsilon absorbs float error in scores like 1-15/300. The shown value
	// rounds down, so a failing score can never display as the threshold.
	passed := accuracy >= MinAccuracy-1e-9
	shown := math.Floor(accuracy*1000+1e-6) / 10
	if passed {
		fmt.Printf("Accuracy: %.1f%% — unlock authorized!\n", shown)
	} else {
		fmt.Printf("Accuracy: %.1f%% — at least %.0f%% is required. Unlock denied.\n", shown, MinAccuracy*100)
	}
	return passed
}

// editDistance returns the Levenshtein distance between a and b: the fewest
// single-character insertions, deletions and substitutions turning a into b.
//
// Input more than twice as long as the target is scored as len(a), an upper
// bound, without building the O(len(a)·len(b)) table: its accuracy is zero
// either way, and it keeps a huge piped line from stalling the CLI.
func editDistance(a, b string) int {
	if len(a) > 2*len(b) {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// splitLines breaks text into consecutive lines of width characters (the last
// may be shorter). The challenge has no spaces to break on, and a fixed width
// keeps every line the same size.
func splitLines(text string, width int) []string {
	var lines []string
	for len(text) > width {
		lines = append(lines, text[:width])
		text = text[width:]
	}
	if text != "" {
		lines = append(lines, text)
	}
	return lines
}

// lineWidthFor picks the challenge line width for the terminal at fd so that
// the "> " prompt plus a full line plus lineSlack never wraps.
func lineWidthFor(fd int) int {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return maxLineWidth
	}
	width := int(ws.Col) - len("> ") - lineSlack - 1
	if width > maxLineWidth {
		return maxLineWidth
	}
	if width < 20 {
		return 20
	}
	return width
}

// bytesContains reports whether haystack contains needle.
func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == string(needle) {
			return true
		}
	}
	return false
}

// isTerminal reports whether fd refers to a terminal.
func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

// makeRaw puts the terminal into raw mode (no echo, no line buffering, no
// signal generation) and returns a function that restores the previous state.
// We handle Ctrl-C ourselves (as a cancel) since ISIG is disabled.
func makeRaw(fd int) (func(), error) {
	old, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, err
	}
	saved := *old

	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return nil, err
	}
	return func() {
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, &saved)
	}, nil
}
