package cli

import (
	"io"
	"os"
	"strings"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// palette decides how one output stream is decorated. Colour and glyphs are
// for people at a terminal; piped output stays plain so scripts can read it.
type palette struct {
	color  bool
	glyphs bool
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func enabled(value string) bool {
	return value != "" && value != "0" && value != "false"
}

func choosePalette(w io.Writer, getenv func(string) string, noColor bool) palette {
	terminal := isTerminal(w) && getenv("TERM") != "dumb"
	forced := enabled(getenv("FORCE_COLOR")) || enabled(getenv("CLICOLOR_FORCE"))
	fancy := terminal || forced
	return palette{glyphs: fancy, color: fancy && !noColor && getenv("NO_COLOR") == ""}
}

func (p palette) paint(code, text string) string {
	if !p.color || text == "" {
		return text
	}
	return code + text + ansiReset
}

func (p palette) bold(text string) string   { return p.paint(ansiBold, text) }
func (p palette) dim(text string) string    { return p.paint(ansiDim, text) }
func (p palette) red(text string) string    { return p.paint(ansiRed, text) }
func (p palette) green(text string) string  { return p.paint(ansiGreen, text) }
func (p palette) yellow(text string) string { return p.paint(ansiYellow, text) }
func (p palette) cyan(text string) string   { return p.paint(ansiCyan, text) }

type tone int

const (
	toneNone tone = iota
	toneGood
	toneWarn
	toneBad
	toneMuted
)

var tones = map[string]tone{}

func init() {
	for _, word := range words("running ready ok healthy succeeded completed complete converged serving live active true") {
		tones[word] = toneGood
	}
	for _, word := range words("paused draining pending in-progress in_progress in-flight provisioning staging starting activating returning retiring converging recorded requested queued suspending suspended stopping stopped terminated repairing warning warn notice degraded") {
		tones[word] = toneWarn
	}
	for _, word := range words("failed refused error rolled-back critical alert emergency unhealthy invalid assigned-not-mounted mounted-orphan pointer-drift missing-gateway blocked") {
		tones[word] = toneBad
	}
	tones["not ready"] = toneBad
	for _, word := range words("retired unknown none false - default debug info") {
		tones[word] = toneMuted
	}
}

func toneOf(state string) tone { return tones[strings.ToLower(state)] }

func (p palette) toned(t tone, text string) string {
	switch t {
	case toneGood:
		return p.green(text)
	case toneWarn:
		return p.yellow(text)
	case toneBad:
		return p.red(text)
	case toneMuted:
		return p.dim(text)
	}
	return text
}

// state colours a status word by what it means for the operator.
func (p palette) state(word string) string {
	return p.toned(toneOf(word), dash(word))
}

// dot is a state word led by a coloured dot, for a field that names one state.
func (p palette) dot(word string) string {
	if !p.glyphs {
		return dash(word)
	}
	t := toneOf(word)
	return p.toned(t, "●") + " " + p.toned(t, dash(word))
}
