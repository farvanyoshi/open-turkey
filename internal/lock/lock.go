// Package lock implements Open Turkey's "typing challenge" mechanism.
//
// === WHAT IS THIS? ===
//
// This package is the only interactive way to unlock an active locked block.
// The idea is friction: when you're tempted to reach something blocked, the
// system makes you transcribe a long random string, character by character.
//
// === WHY DOES THIS WORK? ===
//
// It isn't cryptographic security — a determined user with root can always get
// around Open Turkey. The point is PSYCHOLOGICAL: the tedious transcription
// gives the impulse time to pass. To keep that friction real, we make the
// challenge hard to skip:
//
//   - crypto/rand generates the string, so it can't be predicted from a seed.
//   - The input is read in raw terminal mode, one keypress at a time, and
//     PASTING is detected and rejected (see runChallengeRaw). Copy-pasting the
//     displayed string would defeat the whole exercise.
//   - A minimum length is enforced so "--lock-chars 0" or a negative value
//     can't produce a trivially-empty challenge.
package lock

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// DefaultLockChars is the default challenge length. ~300 characters takes most
// people several minutes — long enough for an impulse to pass.
const DefaultLockChars = 300

// MinLockChars is the smallest challenge length we allow. It stops a lock from
// being configured with a trivially short (or empty) challenge.
const MinLockChars = 30

// charset mixes character classes so typing stays slow (constant Shift/number
// switching). It's intentional friction.
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*"

// GenerateChallenge returns a random string of the given length drawn from
// charset using crypto/rand. It errors on a non-positive length.
func GenerateChallenge(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("challenge length must be positive, got %d", length)
	}

	charsetSize := big.NewInt(int64(len(charset)))
	result := make([]byte, length)

	for i := 0; i < length; i++ {
		index, err := rand.Int(rand.Reader, charsetSize)
		if err != nil {
			return "", fmt.Errorf("failed to generate random character at position %d: %w", i, err)
		}
		result[i] = charset[index.Int64()]
	}

	return string(result), nil
}

// RunChallenge generates a challenge, shows it, and reads the user's answer.
// It returns true only if the transcription matches exactly.
//
// On a real terminal it reads in raw mode with paste detection. When stdin is
// not a terminal (e.g. piped input in tests), it falls back to line reading —
// paste detection isn't meaningful there.
func RunChallenge(length int) (bool, error) {
	challenge, err := GenerateChallenge(length)
	if err != nil {
		return false, fmt.Errorf("failed to generate challenge: %w", err)
	}

	fmt.Println()
	fmt.Println("=== UNLOCK CHALLENGE ===")
	fmt.Println()
	fmt.Printf("To unlock, TYPE (do not paste) exactly the text below (%d characters):\n", length)
	fmt.Println()
	fmt.Println(formatChallenge(challenge, 70))
	fmt.Println()

	fd := int(os.Stdin.Fd())
	if isTerminal(fd) {
		return runChallengeRaw(fd, challenge)
	}
	return runChallengeLine(challenge)
}

// runChallengeRaw reads the answer keypress by keypress in raw terminal mode,
// rejecting pastes. It returns true only when the fully typed input equals the
// challenge.
//
// PASTE DETECTION. In raw mode a human keypress arrives as a single byte per
// read, while a paste dumps many bytes at once. So:
//   - We enable bracketed-paste mode; a paste then arrives wrapped in the
//     escape sequence ESC[200~ … ESC[201~, which we detect and reject.
//   - As a backstop (terminals that don't support bracketed paste), any single
//     read that delivers more than one printable character is treated as a
//     paste and rejected.
//
// Escape sequences from real keys (arrows, etc.) begin with ESC and are ignored
// rather than counted as input.
func runChallengeRaw(fd int, challenge string) (bool, error) {
	restore, err := makeRaw(fd)
	if err != nil {
		// Couldn't switch to raw mode — fall back to line reading.
		return runChallengeLine(challenge)
	}
	defer restore()

	// Ask the terminal to bracket pasted text so we can detect it.
	fmt.Print("\x1b[?2004h")
	defer fmt.Print("\x1b[?2004l")

	fmt.Print("Type the text above: ")

	var typed []byte
	buf := make([]byte, 512)

	for len(typed) < len(challenge) {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			fmt.Print("\r\n")
			return false, fmt.Errorf("failed to read input: %w", err)
		}
		if n == 0 {
			continue
		}
		chunk := buf[:n]

		// Bracketed paste sequence → definite paste.
		if bytesContains(chunk, []byte("\x1b[200~")) {
			fmt.Print("\r\nPaste detected. Unlocking requires typing the text by hand.\r\n")
			return false, nil
		}

		// An escape sequence from a real key (arrow, Home, etc.) starts with
		// ESC — ignore the whole chunk rather than mistake it for input.
		if chunk[0] == 0x1b {
			continue
		}

		printable := 0
		aborted := false
		for _, b := range chunk {
			switch {
			case b == 3: // Ctrl-C
				aborted = true
			case b == '\r' || b == '\n':
				// Enter before the full length means "too short".
				fmt.Print("\r\n")
				return finishAndCompare(string(typed), challenge), nil
			case b == 127 || b == 8: // Backspace / Delete
				if len(typed) > 0 {
					typed = typed[:len(typed)-1]
					fmt.Print("\b \b")
				}
			case b >= 0x20 && b < 0x7f: // printable ASCII
				printable++
				typed = append(typed, b)
				fmt.Print("*") // echo a mask, not the character
			default:
				// ignore other control bytes
			}
		}

		if aborted {
			fmt.Print("\r\n")
			return false, nil
		}

		// More than one printable char in a single read is a paste.
		if printable > 1 {
			fmt.Print("\r\nPaste detected. Unlocking requires typing the text by hand.\r\n")
			return false, nil
		}
	}

	fmt.Print("\r\n")
	return finishAndCompare(string(typed), challenge), nil
}

// runChallengeLine reads a single line (used when stdin isn't a terminal).
func runChallengeLine(challenge string) (bool, error) {
	fmt.Print("Type the text above: ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read user input: %w", err)
		}
		return false, fmt.Errorf("empty input — no text was typed")
	}
	return finishAndCompare(scanner.Text(), challenge), nil
}

// finishAndCompare reports whether input matches challenge and prints a helpful
// message on mismatch (pointing at the first differing position or a length
// mismatch).
func finishAndCompare(input, challenge string) bool {
	if input == challenge {
		fmt.Println("Unlock authorized! Challenge completed successfully.")
		return true
	}

	fmt.Println("Incorrect text! Unlock denied.")

	minLen := len(input)
	if len(challenge) < minLen {
		minLen = len(challenge)
	}
	for i := 0; i < minLen; i++ {
		if input[i] != challenge[i] {
			fmt.Printf("Error at position %d: expected '%c', typed '%c'\n", i+1, challenge[i], input[i])
			return false
		}
	}
	if len(input) < len(challenge) {
		fmt.Printf("Text too short! You typed %d characters, but %d are required.\n", len(input), len(challenge))
	} else if len(input) > len(challenge) {
		fmt.Printf("Text too long! You typed %d characters, but %d are required.\n", len(input), len(challenge))
	}
	return false
}

// formatChallenge wraps text into fixed-width lines so a long challenge is
// readable instead of running off the screen.
func formatChallenge(text string, lineWidth int) string {
	if len(text) <= lineWidth {
		return text
	}
	var builder strings.Builder
	for i := 0; i < len(text); i += lineWidth {
		end := i + lineWidth
		if end > len(text) {
			end = len(text)
		}
		builder.WriteString(text[i:end])
		if end < len(text) {
			builder.WriteByte('\n')
		}
	}
	return builder.String()
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
