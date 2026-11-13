package cli

import (
	"fmt"
	"slices"
	"strings"
)

const exampleEnvironment = "staging"

var groupTitles = map[string]string{
	"gateway":    "Gateways",
	"user":       "Users",
	"sizing":     "Sizing profiles",
	"ops":        "Operations journal",
	"builds":     "Builds and releases",
	"release":    "Builds and releases",
	"logs":       "Logs",
	"login":      "Sign-in",
	"logout":     "Sign-in",
	"completion": "Shell",
}

var (
	nounAliases = map[string]string{"gw": "gateway"}
	verbAliases = map[string]string{"show": "inspect"}
)

func groupOf(cmd *command) string { return groupTitles[cmd.path[0]] }

func groupOrder() []string {
	var titles []string
	for _, cmd := range allCommands() {
		if title := groupOf(cmd); !slices.Contains(titles, title) {
			titles = append(titles, title)
		}
	}
	return titles
}

func isNoun(word string) bool {
	if alias, ok := nounAliases[word]; ok {
		word = alias
	}
	for _, cmd := range allCommands() {
		if len(cmd.path) > 1 && cmd.path[0] == word {
			return true
		}
	}
	return false
}

func canonical(positional []string) []string {
	path := append([]string{}, positional...)
	if alias, ok := nounAliases[path[0]]; ok {
		path[0] = alias
	}
	if len(path) > 1 {
		if alias, ok := verbAliases[path[1]]; ok {
			path[1] = alias
		}
	}
	return path
}

func help(out ui, target []string) error {
	if len(target) == 0 {
		writeHelp(out)
		return nil
	}
	if len(target) == 1 && isNoun(target[0]) {
		writeGroupHelp(out, canonical(target)[0])
		return nil
	}
	cmd, args := lookup(target)
	if cmd == nil || len(args) != 0 {
		return unknownCommand(target)
	}
	writeCommandHelp(out, cmd)
	return nil
}

func unknownCommand(positional []string) error {
	typed := strings.Join(positional, " ")
	if suggestion := suggestCommand(positional); suggestion != "" {
		return advise(fmt.Errorf("unknown command %q", typed), "did you mean `octogee-fleet "+suggestion+"`?")
	}
	if isNoun(positional[0]) {
		return advise(fmt.Errorf("unknown command %q", typed), "run `octogee-fleet help "+canonical(positional)[0]+"` for its commands")
	}
	return advise(fmt.Errorf("unknown command %q", typed), "run `octogee-fleet help` for the commands")
}

func (u ui) heading(text string) {
	u.line("")
	u.line(u.style.bold(text))
}

func (u ui) twoColumns(rows [][2]string) {
	width := 0
	for _, row := range rows {
		width = max(width, len(row[0]))
	}
	for _, row := range rows {
		u.line("  " + row[0] + strings.Repeat(" ", width-len(row[0])) + "  " + u.style.dim(row[1]))
	}
}

func writeHelp(u ui) {
	u.line(u.style.bold("octogee-fleet") + "  operate a fleet of OpenClaw gateways through the Admin API")
	u.heading("Usage")
	u.line("  octogee-fleet --env=<name> <command> [arguments] [flags]")
	commands := allCommands()
	width := 0
	for _, cmd := range commands {
		width = max(width, len(cmd.name()))
	}
	for _, title := range groupOrder() {
		u.heading(title)
		for _, cmd := range commands {
			if groupOf(cmd) == title {
				u.line("  " + cmd.name() + strings.Repeat(" ", width-len(cmd.name())) + "  " + u.style.dim(cmd.summary))
			}
		}
	}
	u.heading("Flags")
	u.twoColumns(flagRows(append(globalFlagSpecs, bare("version", "Print the version."))))
	u.line("")
	u.line("Mutating commands are a dry run until you add --apply. `gw` is short for `gateway`.")
	u.line("Run `octogee-fleet help <command>` for a command's flags and examples.")
}

func writeGroupHelp(u ui, noun string) {
	var rows [][2]string
	title := ""
	for _, cmd := range allCommands() {
		if cmd.path[0] == noun {
			title = groupOf(cmd)
			rows = append(rows, [2]string{cmd.name(), cmd.summary})
		}
	}
	u.line(u.style.bold(title))
	u.line("")
	u.twoColumns(rows)
	u.line("")
	u.line(fmt.Sprintf("Run `octogee-fleet help %s <command>` for a command's flags and examples.", noun))
}

func writeCommandHelp(u ui, cmd *command) {
	u.line(u.style.bold("octogee-fleet "+cmd.name()) + "  " + cmd.summary)
	u.heading("Usage")
	u.line("  octogee-fleet --env=<name> " + cmd.synopsis())
	if flags := cmd.flagSpecs(); len(flags) > 0 {
		u.heading("Flags")
		u.twoColumns(flagRows(flags))
	}
	if len(cmd.examples) > 0 {
		u.heading("Examples")
		for _, example := range cmd.examples {
			u.line(strings.TrimRight(fmt.Sprintf("  octogee-fleet --env=%s %s %s", exampleEnvironment, cmd.name(), example), " "))
		}
	}
	if aliases := cmd.aliases(); len(aliases) > 0 {
		u.heading("Aliases")
		u.line("  " + strings.Join(aliases, ", "))
	}
	u.line("")
	u.line(u.style.dim("Every command also takes --env=<name>, --no-color and --help."))
}

func flagRows(flags []flagSpec) [][2]string {
	rows := make([][2]string, 0, len(flags))
	for _, flag := range flags {
		rows = append(rows, [2]string{flag.label(), flag.help})
	}
	return rows
}

func (c *command) aliases() []string {
	var aliases []string
	for alias, noun := range nounAliases {
		if c.path[0] == noun {
			aliases = append(aliases, strings.Join(append([]string{alias}, c.path[1:]...), " "))
		}
	}
	for alias, verb := range verbAliases {
		if len(c.path) == 2 && c.path[1] == verb {
			aliases = append(aliases, c.path[0]+" "+alias)
		}
	}
	slices.Sort(aliases)
	return aliases
}

// suggestCommand names the command closest to what was typed, or nothing when
// no command is close enough to be a likely typo.
func suggestCommand(positional []string) string {
	typed := canonical(positional)
	candidates := map[string]int{}
	for _, cmd := range allCommands() {
		words := typed
		if len(words) > len(cmd.path) {
			words = words[:len(cmd.path)]
		}
		candidates[cmd.name()] = distance(strings.Join(words, " "), cmd.name())
	}
	return closest(candidates)
}

func suggestFlag(name string, accepted flagSet) string {
	candidates := map[string]int{}
	for candidate := range accepted {
		candidates[candidate] = distance(name, candidate)
	}
	return closest(candidates)
}

func closest(distances map[string]int) string {
	best, bestDistance := "", 0
	for candidate, d := range distances {
		if d > max(1, len(candidate)/4) {
			continue
		}
		if best == "" || d < bestDistance || (d == bestDistance && candidate < best) {
			best, bestDistance = candidate, d
		}
	}
	return best
}

// distance is the Levenshtein edit distance between two strings.
func distance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current := make([]int, len(b)+1)
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
