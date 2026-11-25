package cli

import (
	"testing"
	"time"
)

func TestParseBlockTime(t *testing.T) {
	now := time.Date(2027, 1, 1, 20, 30, 0, 0, time.UTC)
	start, err := parseBlockTime(now, "from", "22:00")
	if err != nil || start.Format(blockTimeLayout) != "2027-01-01 22:00" {
		t.Fatalf("start = %v, %v", start, err)
	}
	for _, test := range []struct {
		input string
		want  string
	}{
		{"06:00", "2027-01-02 06:00"},
		{"23:00", "2027-01-01 23:00"},
		{"2027-01-10 09:00", "2027-01-10 09:00"},
		{"2027-01-01 21:00", ""},
		{"2027-01-01 22:00", ""},
		{"25:00", ""},
		{"09:00junk", ""},
	} {
		got, err := parseBlockTime(start, "until", test.input)
		if test.want == "" {
			if err == nil {
				t.Errorf("%q accepted as %v", test.input, got)
			}
			continue
		}
		if err != nil || got.Format(blockTimeLayout) != test.want {
			t.Errorf("%q = %v, %v; want %s", test.input, got, err, test.want)
		}
	}
}
