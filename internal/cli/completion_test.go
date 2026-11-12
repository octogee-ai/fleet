package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCompletionScriptsParseInTheirShells(t *testing.T) {
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			run := fleetWithoutEnvironment(t, "completion", shell)
			expectHelp(t, run)
			expectContains(t, run.stdout, "gateway")
			expectContains(t, run.stdout, "replace-vm")
			expectContains(t, run.stdout, "idempotency-key")
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}
			script := filepath.Join(t.TempDir(), "completion")
			if err := os.WriteFile(script, []byte(run.stdout), 0o600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command(binary, "-n", script).CombinedOutput(); err != nil {
				t.Fatalf("%s rejects the script: %v\n%s", shell, err, output)
			}
		})
	}
}

func TestCompletionRefusesAnUnknownShell(t *testing.T) {
	run := fleetWithoutEnvironment(t, "completion", "powershell")
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "bash, fish, zsh")
}
