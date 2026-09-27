// Package lock implements Open Turkey's "typing challenge" mechanism.
//
// === WHAT IS THIS? ===
//
// This package is the ONLY way to unlock an active block in Open Turkey.
// The idea is simple: create "friction". When you're tempted to access
// something blocked (social media, games, etc.), the system makes you type a
// huge random string — character by character, without mistakes.
//
// === WHY DOES THIS WORK? ===
//
// It isn't real security. Anyone with access to the code could get around it.
// The point is PSYCHOLOGICAL: the tedious task of typing hundreds of random
// characters gives you time to reconsider whether you really need to unlock.
// Most of the time, you give up — and that's what you wanted.
//
// === WHY crypto/rand AND NOT math/rand? ===
//
// math/rand generates "pseudo-random" numbers — they look random, but follow a
// predictable pattern if you know the "seed". Someone clever could predict the
// generated string and paste it without typing.
//
// crypto/rand uses the operating system's randomness sources (like
// /dev/urandom on Linux). It's truly unpredictable — nobody can guess the
// string before it's generated. Is it overkill for our case? Maybe.
// But it's the right thing to do, and the cost is practically zero.
package lock

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"
)

// DefaultLockChars is the default number of challenge characters.
// 300 characters is enough to be really annoying to type, but not impossible.
// It takes most people about 3-5 minutes — enough time for the "I need to
// check Instagram RIGHT NOW" impulse to pass.
const DefaultLockChars = 300

// charset is the set of characters used to generate the challenge.
// It includes lowercase and uppercase letters, digits and symbols.
// Mixing character types makes typing slower because you have to keep
// switching between Shift, digits and letters.
// This is intentional — the harder, the more friction.
const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%&*"

// GenerateChallenge generates a random string of the specified length.
//
// How it works, step by step:
//  1. Create a byte slice (think of it as a "list" of characters)
//  2. For each position, use crypto/rand.Int() to generate a random number
//     between 0 and the charset size
//  3. That number is used as an index to pick a character from the charset
//  4. Repeat this 'length' times until the full string is built
//
// Simplified example:
//
//	charset = "abc" (3 characters)
//	rand.Int() returns 2 → charset[2] = 'c'
//	rand.Int() returns 0 → charset[0] = 'a'
//	rand.Int() returns 1 → charset[1] = 'b'
//	Result: "cab"
//
// In our case, the charset has 70 characters and the default length is 300,
// so the result is something like: "aB3!kZ$m9Q..." (300 random characters).
func GenerateChallenge(length int) (string, error) {
	// big.NewInt converts the charset size to the type crypto/rand expects.
	// crypto/rand works with big numbers (big.Int) because it was made for
	// cryptography, where numbers can have hundreds of digits.
	charsetSize := big.NewInt(int64(len(charset)))

	// Create the slice that will hold each generated character.
	// In Go, a slice is like a dynamic array — a fixed-size list here.
	result := make([]byte, length)

	for i := 0; i < length; i++ {
		// rand.Int(rand.Reader, max) generates a random number between 0 and max-1.
		// rand.Reader is the operating system's source of randomness.
		// If something goes wrong (e.g. /dev/urandom is unavailable), it returns an error.
		index, err := rand.Int(rand.Reader, charsetSize)
		if err != nil {
			// This practically never happens, but it's good to handle the error.
			return "", fmt.Errorf("failed to generate random character at position %d: %w", i, err)
		}

		// index.Int64() converts the big.Int to a normal int64,
		// which we use as an index to pick the character from the charset.
		result[i] = charset[index.Int64()]
	}

	// Convert the byte slice to a string and return it.
	return string(result), nil
}

// RunChallenge runs the full typing challenge.
//
// Flow:
//  1. Generate the random string with GenerateChallenge
//  2. Show the formatted string to the user (wrapped into 70-character lines)
//  3. Read what the user typed from stdin (keyboard)
//  4. Compare character by character
//  5. If it matches: great, unlock authorized
//  6. If it doesn't: show exactly where they went wrong to help on the next try
//
// Returns true if the challenge was completed successfully, false otherwise.
func RunChallenge(length int) (bool, error) {
	// Step 1: Generate the challenge
	challenge, err := GenerateChallenge(length)
	if err != nil {
		return false, fmt.Errorf("failed to generate challenge: %w", err)
	}

	// Step 2: Show the formatted challenge to the user.
	// We wrap it into 70-character lines to make it easier to read.
	// A single 300-character line would be impossible to follow.
	fmt.Println()
	fmt.Println("=== UNLOCK CHALLENGE ===")
	fmt.Println()
	fmt.Printf("To unlock, type EXACTLY the text below (%d characters):\n", length)
	fmt.Println()
	fmt.Println(formatChallenge(challenge, 70))
	fmt.Println()

	// Step 3: Read the user's input.
	// We use bufio.Scanner because it reads a whole line at once.
	// Go's standard scanner (fmt.Scan) stops at the first space, which
	// doesn't work for us — our string may contain any character.
	fmt.Print("Type the text above: ")
	scanner := bufio.NewScanner(os.Stdin)

	// By default, the Scanner has a limit of ~64KB per line.
	// For very large challenges, we need to increase that buffer.
	// 1MB is more than enough for any reasonable challenge.
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	// Scan() reads a line from stdin. Returns false on error or EOF.
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("failed to read user input: %w", err)
		}
		// If Scan() returns false without an error, it means EOF (the user
		// closed the terminal or redirected /dev/null, for example).
		return false, fmt.Errorf("empty input — no text was typed")
	}

	// scanner.Text() returns the line read, already without the trailing '\n'.
	// We don't need to trim manually — the Scanner takes care of it.
	input := scanner.Text()

	// Step 4: Compare the input with the challenge.
	// First do a quick comparison (are the strings equal?).
	if input == challenge {
		// Step 5: Success!
		fmt.Println()
		fmt.Println("Unlock authorized! Challenge completed successfully.")
		return true, nil
	}

	// Step 6: Failure — find where the first mistake is
	// to help the user on the next try.
	fmt.Println()
	fmt.Println("Incorrect text! Unlock denied.")

	// Find the first position where the texts differ.
	// Walk character by character until we find the difference.
	minLen := len(input)
	if len(challenge) < minLen {
		minLen = len(challenge)
	}

	for i := 0; i < minLen; i++ {
		if input[i] != challenge[i] {
			// Show the position (starting at 1, not 0, because
			// "position 0" doesn't make sense to a regular user).
			fmt.Printf("Error at position %d: expected '%c', typed '%c'\n", i+1, challenge[i], input[i])
			return false, nil
		}
	}

	// If we got here, one text is a prefix of the other (one is shorter).
	// That means the user typed too many or too few characters.
	if len(input) < len(challenge) {
		fmt.Printf("Text too short! You typed %d characters, but %d are required.\n", len(input), len(challenge))
	} else {
		fmt.Printf("Text too long! You typed %d characters, but %d are required.\n", len(input), len(challenge))
	}

	return false, nil
}

// formatChallenge wraps the challenge text into fixed-width lines.
//
// Without this, a 300-character string would look like this in the terminal:
//
//	aB3!kZ$m9Q...(...300 characters on a single line, running off the screen)
//
// With formatting (lineWidth=70), it looks like this:
//
//	aB3!kZ$m9QxY7pL#nW2vR8dF0jH5... (70 characters)
//	tK4gM1sA6bC3eI9oU0wX7yZ2qJ5r... (70 characters)
//	fD8hN4lP0mV6kB1aS3cG9iO5uW7x... (70 characters)
//	eR2tY4jL6nH8pF0qZ (remainder)
//
// This makes it much easier to read and lets the user follow along with a
// finger on the screen while typing.
func formatChallenge(text string, lineWidth int) string {
	// If the text is shorter than the line width, no need to wrap.
	if len(text) <= lineWidth {
		return text
	}

	// strings.Builder is the efficient way to concatenate strings in Go.
	// Every time you do s = s + "something", Go creates a new string in memory.
	// The Builder avoids that by accumulating everything in an internal buffer.
	var builder strings.Builder

	for i := 0; i < len(text); i += lineWidth {
		// Compute the end of the current chunk.
		// If we're on the last chunk, the end is the total text length.
		end := i + lineWidth
		if end > len(text) {
			end = len(text)
		}

		// Write the current chunk.
		builder.WriteString(text[i:end])

		// Add a newline, except after the last chunk.
		if end < len(text) {
			builder.WriteByte('\n')
		}
	}

	return builder.String()
}
