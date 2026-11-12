// Package terminal keeps text from the Admin API from driving the terminal.
package terminal

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape replaces each control character, such as ESC, a newline or a C1
// control, and each invisible format character, such as a right-to-left
// override or a zero-width space, with a visible escape like \x1b or \u202e.
// Text from the Admin API can then neither drive the terminal nor hide or
// reorder what is shown.
func Escape(text string) string {
	if strings.IndexFunc(text, hidden) < 0 {
		return text
	}
	var escaped strings.Builder
	for _, r := range text {
		switch {
		case !hidden(r):
			escaped.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&escaped, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&escaped, `\u%04x`, r)
		default:
			fmt.Fprintf(&escaped, `\U%08x`, r)
		}
	}
	return escaped.String()
}

func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}
