package cli

import (
	"strings"
	"testing"
)

const escape = "\x1b["

var forcedColor = runOptions{env: map[string]string{"FORCE_COLOR": "1"}}

func TestPipedOutputIsPlain(t *testing.T) {
	run := fleet(t, "gateway", "ls")
	expectRead(t, run)
	for _, decoration := range []string{escape, "●", "✓"} {
		if strings.Contains(run.stdout, decoration) {
			t.Fatalf("piped output carries %q:\n%s", decoration, run.stdout)
		}
	}
}

func TestForceColorDecoratesOutput(t *testing.T) {
	for _, name := range []string{"FORCE_COLOR", "CLICOLOR_FORCE"} {
		t.Run(name, func(t *testing.T) {
			run := fleetWith(t, fixture, runOptions{env: map[string]string{name: "1"}}, "gateway", "ls")
			expectRead(t, run)
			expectContains(t, run.stdout, escape)
		})
	}
}

func TestNoColorTurnsColourOff(t *testing.T) {
	cases := map[string]fleetRun{
		"NO_COLOR":   fleetWith(t, fixture, runOptions{env: map[string]string{"FORCE_COLOR": "1", "NO_COLOR": "1"}}, "gateway", "ls"),
		"--no-color": fleetWith(t, fixture, forcedColor, "gateway", "ls", "--no-color"),
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			expectRead(t, run)
			if strings.Contains(run.stdout, escape) {
				t.Fatalf("expected no colour:\n%q", run.stdout)
			}
		})
	}
}

func TestColourDoesNotMoveTableColumns(t *testing.T) {
	plain := fleet(t, "gateway", "ls")
	coloured := fleetWith(t, fixture, forcedColor, "gateway", "ls")
	if got := ansiPattern.ReplaceAllString(coloured.stdout, ""); got != plain.stdout {
		t.Fatalf("coloured table without its escapes:\n%s\nplain table:\n%s", got, plain.stdout)
	}
}

func TestTablesShowTimesRelativeToNow(t *testing.T) {
	run := fleet(t, "gateway", "ls")
	expectRead(t, run)
	expectContains(t, run.stdout, "3m ago")
}

func TestErrorsLeadWithACrossOnlyOnATerminal(t *testing.T) {
	plain := fleet(t, "gateway", "park", "nowhere")
	expectRefused(t, plain)
	expectContains(t, plain.stderr, "octogee-fleet: unknown gateway")

	coloured := fleetWith(t, fixture, forcedColor, "gateway", "park", "nowhere")
	expectRefused(t, coloured)
	expectContains(t, coloured.stderr, "✗")
}
