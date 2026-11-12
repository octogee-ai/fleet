// Package terminal keeps text from the Admin API from driving the terminal.
package terminal

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape replaces each control character, such as ESC, a newline or a C1
// control, with a visible escape like \x1b, so that text from the Admin API
// can neither move the cursor nor restyle the screen.
func Escape(text string) string {
	if strings.IndexFunc(text, unicode.IsControl) < 0 {
		return text
	}
	var escaped strings.Builder
	for _, r := range text {
		switch {
		case !unicode.IsControl(r):
			escaped.WriteRune(r)
		case r < 0x80:
			fmt.Fprintf(&escaped, `\x%02x`, r)
		default:
			fmt.Fprintf(&escaped, `\u%04x`, r)
		}
	}
	return escaped.String()
}
