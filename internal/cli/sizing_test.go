package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func profileFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func standardWith(t *testing.T, change func(profile map[string]any)) string {
	t.Helper()
	profile := standardSizingProfile()
	if change != nil {
		change(profile)
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSizingListIsATable(t *testing.T) {
	run := fleet(t, "sizing", "ls")
	expectRead(t, run)
	if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/sizing-profiles"}) {
		t.Fatalf("requests %+v", run.requests)
	}
	expectContains(t, run.stdout, "MACHINE")
	row := lineWith(t, run.stdout, "standard")
	for _, want := range []string{"isolated", "e2-medium", "SPOT", "x11", "80GB/pd-balanced", "10GiB"} {
		expectContains(t, row, want)
	}
	expectContains(t, lineWith(t, run.stdout, "default"), "shared")
}

func TestSizingListInJSONIsTheAdminAPIResponse(t *testing.T) {
	run := fleet(t, "sizing", "ls", "--format=json")
	expectRead(t, run)
	var printed struct {
		SizingProfiles []struct {
			Name    string         `json:"name"`
			Profile map[string]any `json:"profile"`
		} `json:"sizingProfiles"`
	}
	if err := json.Unmarshal([]byte(run.stdout), &printed); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, run.stdout)
	}
	if len(printed.SizingProfiles) != 2 || printed.SizingProfiles[1].Profile["sizingTier"] != "s" {
		t.Fatalf("printed %+v", printed)
	}
}

func TestSizingShowReadsOneProfile(t *testing.T) {
	for _, verb := range []string{"show", "inspect"} {
		t.Run(verb, func(t *testing.T) {
			run := fleet(t, "sizing", verb, "standard")
			expectRead(t, run)
			if !run.containsRequest(adminRequest{Method: http.MethodGet, Path: "/api/admin/sizing-profiles/standard"}) {
				t.Fatalf("requests %+v", run.requests)
			}
			expectContains(t, lineWith(t, run.stdout, "machine"), "e2-medium")
			expectContains(t, lineWith(t, run.stdout, "boot disk"), "80 GB pd-balanced")
			expectContains(t, lineWith(t, run.stdout, "home disk"), "10 GiB")
		})
	}
}

func TestSizingShowReportsAnUnknownProfile(t *testing.T) {
	run := fleet(t, "sizing", "show", "platinum")
	expectRefused(t, run)
	expectContains(t, run.stderr, "404 sizing-profile-unknown")
}

func TestSizingSetChecksTheFileAsADryRun(t *testing.T) {
	run := fleet(t, "sizing", "set", "team", "--file="+profileFile(t, `{"isolation": "shared"}`))
	expectRead(t, run)
	checked := run.onlyDryRun(t).adminRequest
	want := adminRequest{
		Method: http.MethodPut, Path: "/api/admin/sizing-profiles/team",
		Query: map[string]string{"dryRun": "true"}, Body: map[string]any{"isolation": "shared"},
	}
	if !reflect.DeepEqual(checked, want) {
		t.Fatalf("checked %+v, want %+v", checked, want)
	}
	expectContains(t, run.stdout, "dry run, nothing changed")
}

func TestSizingSetAppliesTheFile(t *testing.T) {
	run := fleet(t, "sizing", "set", "team", "--file="+profileFile(t, `{"isolation": "shared"}`), "--apply")
	expectApplied(t, run, adminRequest{Method: http.MethodPut, Path: "/api/admin/sizing-profiles/team", Body: map[string]any{"isolation": "shared"}})
	expectContains(t, run.stdout, "sizing profile team set")
	expectContains(t, run.stdout, "op-sizing")
}

func TestSizingSetSaysWhenItReplacesAProfile(t *testing.T) {
	larger := standardWith(t, func(profile map[string]any) {
		profile["computer"].(map[string]any)["machineType"] = "e2-standard-2"
	})
	run := fleet(t, "sizing", "set", "standard", "--file="+profileFile(t, larger), "--apply")
	if run.exit != 0 {
		t.Fatalf("exit %d: %s", run.exit, run.stderr)
	}
	expectContains(t, run.stdout, "sizing profile standard replaced")
}

func TestSizingSetChangesNothingWhenTheProfileAlreadyHasTheseSettings(t *testing.T) {
	run := fleet(t, "sizing", "set", "standard", "--file="+profileFile(t, standardWith(t, nil)), "--apply")
	expectRead(t, run)
	expectContains(t, run.stdout, "nothing to do")
}

func TestSizingSetRefusesAFileThatIsNotOneJSONObject(t *testing.T) {
	for _, content := range []string{`[]`, `null`, `"shared"`, `{"isolation": "shared"} {}`, `isolation: shared`} {
		t.Run(content, func(t *testing.T) {
			run := fleet(t, "sizing", "set", "team", "--file="+profileFile(t, content))
			expectNothingSent(t, run)
			expectContains(t, run.stderr, "one sizing profile as a JSON object")
		})
	}
}

func TestSizingSetRefusesAFileItCannotRead(t *testing.T) {
	run := fleet(t, "sizing", "set", "team", "--file="+filepath.Join(t.TempDir(), "missing.json"))
	expectNothingSent(t, run)
	expectContains(t, run.stderr, "missing.json")
}

func TestSizingSetNeedsAFile(t *testing.T) {
	expectNothingSent(t, fleet(t, "sizing", "set", "team"))
}

func TestHelpListsTheSizingCommands(t *testing.T) {
	run := fleetWithoutEnvironment(t, "help")
	expectHelp(t, run)
	users, sizing, operations := strings.Index(run.stdout, "user ls"), strings.Index(run.stdout, "Sizing profiles"), strings.Index(run.stdout, "ops ls")
	if users >= sizing || sizing >= operations {
		t.Fatalf("expected the sizing profiles between users and operations in:\n%s", run.stdout)
	}
	expectContains(t, run.stdout, "sizing set")
}
