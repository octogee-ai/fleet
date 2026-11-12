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

func withOddID(name, id string) fleetFixture {
	return withUser(name, func(u *fixtureUser) { u.id = id })
}

func TestAnIDWithAControlCharacterIsSentAsTheAdminAPISentIt(t *testing.T) {
	odd := withOddID("ada", "u-ada\x1b")
	run := fleetWith(t, odd, runOptions{}, "user", "delete", "ada")
	sent := run.onlyDryRun(t)
	if sent.Path != "/api/admin/users/u-ada\x1b/delete" || !strings.HasPrefix(sent.uri, "/api/admin/users/u-ada%1B/delete?") {
		t.Fatalf("sent %q", sent.uri)
	}
	expectNoControlCharacters(t, run.stdout)
	shown := fleetWith(t, odd, runOptions{}, "user", "inspect", "ada")
	expectNoControlCharacters(t, shown.stdout)
	expectContains(t, shown.stdout, `u-ada\x1b`)
}

func TestTheEscapedFormOfAnIDDoesNotMatchIt(t *testing.T) {
	run := fleetWith(t, withOddID("ada", "u-ada\x1b"), runOptions{}, "user", "delete", `u-ada\x1b`)
	if run.exit == 0 || len(run.dryRuns()) != 0 {
		t.Fatalf("the escaped form matched: exit %d, dry runs %v", run.exit, run.dryRuns())
	}
	expectContains(t, run.stderr, "unknown user")
}

func TestANewlineAndItsEscapedFormAreDifferentUsers(t *testing.T) {
	twins := fixture
	twins.users = append(append([]fixtureUser{}, fixture.users...),
		fixtureUser{id: "u-twin\n", name: "first"},
		fixtureUser{id: `u-twin\x0a`, name: "second", provisioning: "pending"},
	)
	refused := fleetWith(t, twins, runOptions{}, "user", "issue-passwords", `u-twin\x0a`)
	if refused.exit == 0 || len(refused.dryRuns()) != 0 {
		t.Fatalf("checked the wrong user: exit %d, dry runs %v", refused.exit, refused.dryRuns())
	}
	expectContains(t, refused.stderr, "second has not finished provisioning")
	sent := fleetWith(t, twins, runOptions{}, "user", "issue-passwords", "first").onlyDryRun(t)
	if !strings.HasPrefix(sent.uri, "/api/admin/users/u-twin%0A/one-time-passwords?") {
		t.Fatalf("sent %q", sent.uri)
	}
}

func TestAOneTimePasswordIsPrintedAsSent(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{chatPassword: "chat\x7fsecret"}, "user", "issue-passwords", "ada", "--apply")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, run.stdout, "chat\x7fsecret")
}

func TestColourCodesFromTheAdminAPIAreEscapedAtATerminal(t *testing.T) {
	run := fleetWith(t, withEmail("grace", "grace\x1b[32m@example.test"), runOptions{env: map[string]string{"FORCE_COLOR": "1"}}, "user", "inspect", "grace")
	expectContains(t, run.stdout, `grace\x1b[32m@example.test`)
	expectContains(t, run.stdout, ansiBold+"grace"+ansiReset)
	if strings.Contains(run.stdout, "grace\x1b[32m@") {
		t.Fatalf("the Admin API's colour code reached the terminal:\n%q", run.stdout)
	}
}

func TestANewlineInAnAdminAPIErrorStaysOnItsLine(t *testing.T) {
	run := fleetWith(t, fixture, runOptions{refuseApplyWith: http.StatusConflict, errorMessage: "busy\nnext: forged"}, "gateway", "restart", "shared-a", "--apply")
	expectNoControlCharacters(t, run.stderr)
	expectContains(t, run.stderr, `busy\x0anext: forged`)
}
