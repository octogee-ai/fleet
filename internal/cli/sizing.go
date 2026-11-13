package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

var sizingCommands = []*command{
	{
		path: words("sizing ls"), summary: "List sizing profiles.",
		examples: []string{""},
		run:      sizingList,
	},
	{
		path: words("sizing inspect"), usage: "<name>", argc: 1, summary: "Show one sizing profile.",
		examples: []string{"standard"},
		run:      sizingInspect,
	},
	{
		path: words("sizing set"), usage: "<name> --file=<path>", argc: 1, summary: "Create or replace a sizing profile from a JSON file.",
		flags:    []flagSpec{valued("file", "<path>", "A JSON file holding the profile, as `sizing inspect --format=json` prints it under profile.")},
		required: []string{"file"}, mutating: true,
		examples: []string{"standard --file=standard.json", "standard --file=standard.json --apply"},
		run:      sizingSet,
	},
}

// namedProfile is a sizing profile as the Admin API lists it. The profile is
// kept as sent, and its fields are read only for display.
type namedProfile struct {
	Name    string          `json:"name"`
	Profile json.RawMessage `json:"profile"`
}

func (p namedProfile) fields() map[string]any {
	var fields map[string]any
	if json.Unmarshal(p.Profile, &fields) != nil {
		return nil
	}
	return fields
}

// at reads a nested field, or nil when any step is missing.
func at(fields map[string]any, path ...string) any {
	var value any = fields
	for _, name := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[name]
	}
	return value
}

func shown(value any) string {
	if value == nil {
		return ""
	}
	return fieldText(value)
}

func withUnit(value any, unit string) string {
	if value == nil {
		return ""
	}
	return shown(value) + unit
}

func (i *invocation) sizingProfiles() ([]namedProfile, json.RawMessage, error) {
	var response struct {
		SizingProfiles []namedProfile `json:"sizingProfiles"`
	}
	raw, err := i.read(admin.Path("sizing-profiles"), nil, &response)
	return response.SizingProfiles, raw, err
}

func sizingList(i *invocation) error {
	profiles, raw, err := i.sizingProfiles()
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	rows := make([][]string, 0, len(profiles))
	for _, entry := range profiles {
		fields := entry.fields()
		boot := ""
		if size, kind := at(fields, "computer", "bootDisk", "sizeGb"), at(fields, "computer", "bootDisk", "type"); size != nil {
			boot = withUnit(size, "GB") + "/" + shown(kind)
		}
		rows = append(rows, []string{
			i.style.bold(entry.Name),
			dash(shown(at(fields, "isolation"))),
			dash(shown(at(fields, "sizingTier"))),
			dash(shown(at(fields, "computer", "machineType"))),
			dash(shown(at(fields, "computer", "provisioningModel"))),
			dash(shown(at(fields, "computer", "desktop"))),
			dash(boot),
			dash(withUnit(at(fields, "computer", "homeSizeGiB"), "GiB")),
		})
	}
	i.table([]string{"name", "isolation", "tier", "machine", "model", "desktop", "boot", "home"}, rows, "no sizing profiles")
	return nil
}

func sizingInspect(i *invocation) error {
	var entry namedProfile
	raw, err := i.read(admin.Path("sizing-profiles", i.args[0]), nil, &entry)
	if err != nil {
		return err
	}
	if i.json {
		return i.printJSON(raw)
	}
	fields := entry.fields()
	if fields == nil {
		i.line(i.style.bold(entry.Name) + "  " + string(entry.Profile))
		return nil
	}
	boot := ""
	if size := at(fields, "computer", "bootDisk", "sizeGb"); size != nil {
		boot = withUnit(size, " GB") + " " + shown(at(fields, "computer", "bootDisk", "type"))
	}
	i.line(i.style.bold(entry.Name) + "  " + dash(shown(at(fields, "isolation"))) + " sizing profile")
	i.fields([]field{
		{"tier", shown(at(fields, "sizingTier"))},
		{"runtime", shown(at(fields, "computer", "runtime"))},
		{"machine", shown(at(fields, "computer", "machineType"))},
		{"model", shown(at(fields, "computer", "provisioningModel"))},
		{"desktop", shown(at(fields, "computer", "desktop"))},
		{"boot disk", boot},
		{"home disk", withUnit(at(fields, "computer", "homeSizeGiB"), " GiB")},
		{"fallback storage", withUnit(at(fields, "fallbackStorageGb"), " GB")},
	})
	return nil
}

func readProfile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --file: %w", err)
	}
	var profile map[string]any
	if err := json.Unmarshal(data, &profile); err != nil || profile == nil {
		return nil, advise(errors.New("--file must hold one sizing profile as a JSON object"), "write it as `sizing inspect <name> --format=json` prints it under profile")
	}
	return profile, nil
}

func sizingSet(i *invocation) error {
	name := i.args[0]
	profile, err := readProfile(i.value("file"))
	if err != nil {
		return err
	}
	profiles, _, err := i.sizingProfiles()
	if err != nil {
		return err
	}
	done := "sizing profile " + name + " set"
	for _, entry := range profiles {
		if entry.Name != name {
			continue
		}
		var stored map[string]any
		if json.Unmarshal(entry.Profile, &stored) == nil && reflect.DeepEqual(stored, profile) {
			return i.unchanged("sizing profile " + name + " already has these settings")
		}
		done = "sizing profile " + name + " replaced"
	}
	return i.apply(change{
		request: admin.Request{Method: http.MethodPut, Path: admin.Path("sizing-profiles", name), Body: profile},
		done:    done,
	})
}
