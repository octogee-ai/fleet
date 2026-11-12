package cli

import (
	"net/http"
	"strings"
	"testing"
	"unicode"
)

const clearScreen = "\x1b[2J"

func withEmail(name, email string) fleetFixture {
	return withUser(name, func(u *fixtureUser) { u.email = email })
}

func expectNoControlCharacters(t *testing.T, text string) {
	t.Helper()
	for _, r := range text {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			t.Fatalf("output carries the control or format character %U:\n%q", r, text)
		}
	}
}

func TestTextFromTheAdminAPIIsEscapedBeforeItReachesTheTerminal(t *testing.T) {
	hostile := withEmail("grace", "grace"+clearScreen+"@example.test")
	for _, argv := range [][]string{{"gateway", "inspect", "iso-grace"}, {"user", "inspect", "grace"}} {
		run := fleetWith(t, hostile, runOptions{}, argv...)
		expectNoControlCharacters(t, run.stdout)
		expectContains(t, run.stdout, `grace\x1b[2J@example.test`)
	}
}

func TestFormatCharactersFromTheAdminAPIAreEscaped(t *testing.T) {
	run := fleetWith(t, withEmail("grace", "grace\u202e@example.test\u200b"), runOptions{}, "user", "inspect", "grace")
	expectNoControlCharacters(t, run.stdout)
	expectContains(t, run.stdout, `grace\u202e@example.test\u200b`)
}

func TestEscapedCellsKeepTheTableAligned(t *testing.T) {
	run := fleetWith(t, withEmail("grace", "g"+clearScreen+"@example.test"), runOptions{}, "gateway", "inspect", "iso-grace")
	header := lineWith(t, run.stdout, "EMAIL")
	row := lineWith(t, run.stdout, `g\x1b[2J@example.test`)
	if strings.Index(header, "ACTIVE") != strings.Index(row, "2026-11-09T23:05:00Z") {
		t.Fatalf("columns moved:\n%s\n%s", header, row)
	}
}

func TestAnAdminAPIErrorIsEscaped(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{refuseApplyWith: http.StatusConflict, errorMessage: "busy" + clearScreen}, "gateway", "restart", "shared-a", "--apply")
	expectNoControlCharacters(t, run.stderr)
	expectContains(t, run.stderr, `busy\x1b[2J`)
}

func TestJSONKeepsTheAdminAPIsText(t *testing.T) {
	run := fleetWith(t, withEmail("grace", "grace"+clearScreen+"@example.test"), runOptions{}, "user", "inspect", "grace", "--format=json")
	expectContains(t, run.stdout, `grace\u001b[2J@example.test`)
}
