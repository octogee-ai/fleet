package terminal

import "testing"

func TestEscape(t *testing.T) {
	for text, want := range map[string]string{
		"plain text, ünïcode ✓":  "plain text, ünïcode ✓",
		"clear\x1b[2Jscreen":     `clear\x1b[2Jscreen`,
		"line\nbreak\ttab\x7f":   `line\x0abreak\x09tab\x7f`,
		"c1\u009bcontrol\u0085":  `c1\u009bcontrol\u0085`,
		"bell\a and null\x00end": `bell\x07 and null\x00end`,
	} {
		if got := Escape(text); got != want {
			t.Errorf("Escape(%q) = %q, want %q", text, got, want)
		}
	}
}
