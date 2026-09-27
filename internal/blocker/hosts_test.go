package blocker

import "testing"

func TestRemoveOpenTurkeySection(t *testing.T) {
	input := "127.0.0.1 localhost\n" +
		beginMarker + "\n" +
		"0.0.0.0 facebook.com\n" +
		"0.0.0.0 www.facebook.com\n" +
		endMarker + "\n" +
		"192.168.0.1 printer\n"

	got := removeOpenTurkeySection(input)
	want := "127.0.0.1 localhost\n192.168.0.1 printer\n"
	if got != want {
		t.Errorf("removeOpenTurkeySection() = %q; want %q", got, want)
	}
}

func TestExtractOpenTurkeySection(t *testing.T) {
	input := "127.0.0.1 localhost\n" +
		beginMarker + "\n" +
		"0.0.0.0 facebook.com\n" +
		endMarker + "\n"

	got := extractOpenTurkeySection(input)
	want := "0.0.0.0 facebook.com"
	if got != want {
		t.Errorf("extractOpenTurkeySection() = %q; want %q", got, want)
	}
}

func TestContainsLine(t *testing.T) {
	section := "0.0.0.0 facebook.com\n0.0.0.0 www.facebook.com"

	if !containsLine(section, "0.0.0.0 facebook.com") {
		t.Error("containsLine should find the exact line")
	}
	// Must not be fooled by a superstring like facebook.com.evil.test.
	if containsLine(section, "0.0.0.0 facebook.co") {
		t.Error("containsLine matched a partial line; must require a full-line match")
	}
	// A tampered address (real IP instead of 0.0.0.0) must not count.
	if containsLine("1.2.3.4 facebook.com", "0.0.0.0 facebook.com") {
		t.Error("containsLine matched a tampered address line")
	}
}
