package cli

import (
	"fmt"
	"slices"
	"strings"
)

var completionShells = []string{"bash", "fish", "zsh"}

var completionCommand = &command{
	path: words("completion"), usage: "bash|fish|zsh", argc: 1, summary: "Print a shell completion script.", standalone: true,
	examples: []string{
		`zsh > "${fpath[1]}/_octogee-fleet"`,
		"bash > ~/.local/share/bash-completion/completions/octogee-fleet",
		"fish > ~/.config/fish/completions/octogee-fleet.fish",
	},
}

func init() { completionCommand.run = completion }

func completion(i *invocation) error {
	shell := i.args[0]
	switch shell {
	case "bash":
		i.line(bashCompletion())
	case "fish":
		i.line(fishCompletion())
	case "zsh":
		i.line(zshCompletion())
	default:
		return fmt.Errorf("completion supports %s, not %q", strings.Join(completionShells, ", "), shell)
	}
	return nil
}

// choice is one word a shell may offer next, with what it does.
type choice struct {
	word, help string
}

func topChoices() []choice {
	var choices []choice
	seen := map[string]bool{}
	for _, cmd := range allCommands() {
		noun := cmd.path[0]
		if seen[noun] {
			continue
		}
		seen[noun] = true
		help := cmd.summary
		if len(cmd.path) > 1 {
			help = groupTitles[noun]
		}
		choices = append(choices, choice{noun, help})
	}
	return append(choices, choice{"help", "Show help for a command."})
}

// nextChoices are the words that can follow a command path prefix.
func nextChoices(prefix []string) []choice {
	var choices []choice
	for _, cmd := range allCommands() {
		if len(cmd.path) <= len(prefix) || !slices.Equal(cmd.path[:len(prefix)], prefix) {
			continue
		}
		word := cmd.path[len(prefix)]
		help := cmd.summary
		if len(cmd.path) > len(prefix)+1 {
			help = strings.Join(cmd.path[:len(prefix)+1], " ") + " commands"
		}
		if !slices.ContainsFunc(choices, func(c choice) bool { return c.word == word }) {
			choices = append(choices, choice{word, help})
		}
	}
	return choices
}

func choiceWords(choices []choice) string {
	list := make([]string, len(choices))
	for index, c := range choices {
		list[index] = c.word
	}
	return strings.Join(list, " ")
}

// prefixes are the command paths that have further words to complete.
func prefixes() [][]string {
	var found [][]string
	for _, cmd := range allCommands() {
		for length := 1; length < len(cmd.path); length++ {
			prefix := cmd.path[:length]
			if !slices.ContainsFunc(found, func(p []string) bool { return slices.Equal(p, prefix) }) {
				found = append(found, prefix)
			}
		}
	}
	return found
}

func commandFlags(cmd *command) []flagSpec {
	return append(cmd.flagSpecs(), globalFlagSpecs...)
}

func singleQuoted(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

func bashCompletion() string {
	var script strings.Builder
	script.WriteString(`# bash completion for octogee-fleet
_octogee_fleet() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local line="${COMP_LINE:0:COMP_POINT}" word
  local -a typed=() cmdwords=()
  read -ra typed <<< "$line"
  if [[ $line != *" " && ${#typed[@]} -gt 0 ]]; then
    unset "typed[${#typed[@]}-1]"
  fi
  for word in "${typed[@]:1}"; do
    [[ $word == -* ]] || cmdwords+=("$word")
  done
  [[ ${cmdwords[0]} == help ]] && cmdwords=("${cmdwords[@]:1}")
  [[ ${cmdwords[0]} == gw ]] && cmdwords[0]=gateway
  [[ ${cmdwords[1]} == show ]] && cmdwords[1]=inspect
  if [[ $cur == -* ]]; then
    local flags
    case "${cmdwords[*]}" in
`)
	for _, cmd := range allCommands() {
		fmt.Fprintf(&script, "      %q|%q*) flags=%q ;;\n", cmd.name(), cmd.name()+" ", flagLabels(commandFlags(cmd)))
	}
	fmt.Fprintf(&script, "      *) flags=%q ;;\n", flagLabels(append(globalFlagSpecs, bare("version", ""))))
	script.WriteString(`    esac
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
    [[ ${#COMPREPLY[@]} -eq 1 && ${COMPREPLY[0]} == *= ]] && compopt -o nospace 2>/dev/null
    return
  fi
  case "${cmdwords[*]}" in
`)
	fmt.Fprintf(&script, "    \"\") COMPREPLY=($(compgen -W %q -- \"$cur\")) ;;\n", choiceWords(topChoices()))
	fmt.Fprintf(&script, "    completion) COMPREPLY=($(compgen -W %q -- \"$cur\")) ;;\n", strings.Join(completionShells, " "))
	for _, prefix := range prefixes() {
		fmt.Fprintf(&script, "    %q) COMPREPLY=($(compgen -W %q -- \"$cur\")) ;;\n", strings.Join(prefix, " "), choiceWords(nextChoices(prefix)))
	}
	script.WriteString(`  esac
}
complete -F _octogee_fleet octogee-fleet`)
	return script.String()
}

func flagLabels(flags []flagSpec) string {
	labels := make([]string, len(flags))
	for index, flag := range flags {
		labels[index] = "--" + flag.name
		if flag.kind == valueFlag {
			labels[index] += "="
		}
	}
	return strings.Join(labels, " ")
}

func zshDescriptions(choices []choice) string {
	quoted := make([]string, len(choices))
	for index, c := range choices {
		quoted[index] = singleQuoted(strings.ReplaceAll(c.word, ":", `\:`) + ":" + c.help)
	}
	return strings.Join(quoted, " ")
}

func zshFlagChoices(flags []flagSpec, kind flagKind) string {
	var choices []choice
	for _, flag := range flags {
		if flag.kind != kind {
			continue
		}
		word := "--" + flag.name
		if kind == valueFlag {
			word += "="
		}
		choices = append(choices, choice{word, flag.help})
	}
	return zshDescriptions(choices)
}

func zshCompletion() string {
	var script strings.Builder
	script.WriteString(`#compdef octogee-fleet

_octogee_fleet() {
  local -a cmdwords valued bare choices
  local word
  for word in "${(@)words[2,CURRENT-1]}"; do
    [[ $word == -* ]] || cmdwords+=("$word")
  done
  [[ ${cmdwords[1]} == help ]] && shift cmdwords
  [[ ${cmdwords[1]} == gw ]] && cmdwords[1]=gateway
  [[ ${cmdwords[2]} == show ]] && cmdwords[2]=inspect
  if [[ $PREFIX == --format=* ]]; then
    compset -P '--format='
    compadd text json
    return
  fi
  if [[ $PREFIX == -* ]]; then
    case "${cmdwords[*]}" in
`)
	for _, cmd := range allCommands() {
		flags := commandFlags(cmd)
		fmt.Fprintf(&script, "      (%s|%s*) valued=(%s); bare=(%s) ;;\n", singleQuoted(cmd.name()), singleQuoted(cmd.name()+" "), zshFlagChoices(flags, valueFlag), zshFlagChoices(flags, bareFlag))
	}
	global := append(globalFlagSpecs, bare("version", "Print the version."))
	fmt.Fprintf(&script, "      (*) valued=(%s); bare=(%s) ;;\n", zshFlagChoices(global, valueFlag), zshFlagChoices(global, bareFlag))
	script.WriteString(`    esac
    _describe -t flags 'flag' valued -S '' -- bare
    return
  fi
  case "${cmdwords[*]}" in
`)
	fmt.Fprintf(&script, "    ('') choices=(%s) ;;\n", zshDescriptions(topChoices()))
	fmt.Fprintf(&script, "    (completion) choices=(%s) ;;\n", zshDescriptions([]choice{{"bash", "bash"}, {"fish", "fish"}, {"zsh", "zsh"}}))
	for _, prefix := range prefixes() {
		fmt.Fprintf(&script, "    (%s) choices=(%s) ;;\n", singleQuoted(strings.Join(prefix, " ")), zshDescriptions(nextChoices(prefix)))
	}
	script.WriteString(`    (*) return 1 ;;
  esac
  _describe -t commands 'command' choices
}

if [[ ${zsh_eval_context[-1]} == loadautofunc ]]; then
  _octogee_fleet "$@"
else
  compdef _octogee_fleet octogee-fleet
fi`)
	return script.String()
}

func fishCompletion() string {
	var script strings.Builder
	script.WriteString("# fish completion for octogee-fleet\ncomplete -c octogee-fleet -f\n")
	for _, c := range topChoices() {
		fmt.Fprintf(&script, "complete -c octogee-fleet -n __fish_use_subcommand -a %s -d %s\n", c.word, singleQuoted(c.help))
	}
	for _, prefix := range prefixes() {
		choices := nextChoices(prefix)
		condition := fishSeen(prefix) + "; and not __fish_seen_subcommand_from " + choiceWords(choices)
		for _, c := range choices {
			fmt.Fprintf(&script, "complete -c octogee-fleet -n %s -a %s -d %s\n", singleQuoted(condition), c.word, singleQuoted(c.help))
		}
	}
	fmt.Fprintf(&script, "complete -c octogee-fleet -n %s -a %s\n", singleQuoted("__fish_seen_subcommand_from completion"), singleQuoted(strings.Join(completionShells, " ")))
	for _, flag := range append(globalFlagSpecs, bare("version", "Print the version.")) {
		script.WriteString(fishFlag("", flag))
	}
	for _, cmd := range allCommands() {
		for _, flag := range cmd.flagSpecs() {
			script.WriteString(fishFlag(fishSeen(cmd.path), flag))
		}
	}
	return strings.TrimRight(script.String(), "\n")
}

func fishSeen(path []string) string {
	conditions := make([]string, len(path))
	for index, word := range path {
		alternatives := word
		for alias, noun := range nounAliases {
			if index == 0 && noun == word {
				alternatives += " " + alias
			}
		}
		for alias, verb := range verbAliases {
			if index == 1 && verb == word {
				alternatives += " " + alias
			}
		}
		conditions[index] = "__fish_seen_subcommand_from " + alternatives
	}
	return strings.Join(conditions, "; and ")
}

func fishFlag(condition string, flag flagSpec) string {
	var line strings.Builder
	line.WriteString("complete -c octogee-fleet")
	if condition != "" {
		line.WriteString(" -n " + singleQuoted(condition))
	}
	line.WriteString(" -l " + flag.name)
	if flag.kind == valueFlag {
		line.WriteString(" -x")
		if flag.name == "format" {
			line.WriteString(" -a 'text json'")
		}
	}
	line.WriteString(" -d " + singleQuoted(flag.help) + "\n")
	return line.String()
}
