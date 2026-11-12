package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	formatText = "text"
	formatJSON = "json"
)

// ui writes one command's output: decorated text for people, or JSON for
// scripts. Colour and glyphs never reach JSON.
type ui struct {
	out    io.Writer
	err    io.Writer
	style  palette
	errors palette
	json   bool
	now    func() time.Time
}

func newUI(runtime Runtime, parsed parsedArgs) ui {
	noColor := parsed.has("no-color")
	asJSON := parsed.flags["format"].value == formatJSON
	style := choosePalette(runtime.Stdout, runtime.Getenv, noColor || asJSON)
	if asJSON {
		style.glyphs = false
	}
	now := runtime.Now
	if now == nil {
		now = time.Now
	}
	return ui{
		out:    runtime.Stdout,
		err:    runtime.Stderr,
		style:  style,
		errors: choosePalette(runtime.Stderr, runtime.Getenv, noColor),
		json:   asJSON,
		now:    now,
	}
}

func (u ui) line(text string) { fmt.Fprintln(u.out, text) }

func (u ui) mark(glyph func(string) string, symbol, plain, text string) {
	if u.style.glyphs {
		u.line(glyph(symbol) + " " + text)
		return
	}
	u.line(plain + text)
}

func (u ui) ok(text string)   { u.mark(u.style.green, "✓", "", text) }
func (u ui) warn(text string) { u.mark(u.style.yellow, "⚠", "warning: ", text) }
func (u ui) next(text string) { u.mark(u.style.cyan, "→", "next: ", text) }

// check shows a yes-or-no cell: a tick at a terminal, true or false when piped.
func (u ui) check(on bool) string {
	switch {
	case !u.style.glyphs:
		return strconv.FormatBool(on)
	case on:
		return u.style.green("✓")
	}
	return u.style.dim("·")
}

// detail is a secondary line under a result.
func (u ui) detail(text string) {
	if u.style.glyphs {
		u.line("  " + text)
		return
	}
	u.line(text)
}

// notice writes a warning that is about the run, not the data, to stderr so
// that JSON on stdout stays parseable.
func (u ui) notice(text string) {
	if u.errors.glyphs {
		fmt.Fprintln(u.err, u.errors.yellow("⚠")+" "+text)
		return
	}
	fmt.Fprintln(u.err, "warning: "+text)
}

func (u ui) printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	fmt.Fprintln(u.out, string(data))
	return nil
}

// recordsOf lists the records as the Admin API sent them.
func recordsOf[T any](items []T, record func(T) json.RawMessage) []json.RawMessage {
	list := make([]json.RawMessage, len(items))
	for index, item := range items {
		list[index] = record(item)
	}
	return list
}

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func visibleWidth(text string) int {
	return utf8.RuneCountInString(ansiPattern.ReplaceAllString(text, ""))
}

// table aligns columns by their visible width, so coloured cells line up.
func (u ui) table(header []string, rows [][]string, empty string) {
	if len(rows) == 0 {
		u.line(u.style.dim(empty))
		return
	}
	widths := make([]int, len(header))
	for index, name := range header {
		widths[index] = len(name)
	}
	for _, row := range rows {
		for index, cell := range row {
			widths[index] = max(widths[index], visibleWidth(cell))
		}
	}
	titles := make([]string, len(header))
	for index, name := range header {
		titles[index] = u.style.dim(strings.ToUpper(name))
	}
	u.line(alignRow(titles, widths))
	for _, row := range rows {
		u.line(alignRow(row, widths))
	}
}

func alignRow(cells []string, widths []int) string {
	var row strings.Builder
	for index, cell := range cells {
		if index > 0 {
			row.WriteString("  ")
		}
		row.WriteString(cell)
		if index < len(cells)-1 {
			row.WriteString(strings.Repeat(" ", widths[index]-visibleWidth(cell)))
		}
	}
	return strings.TrimRight(row.String(), " ")
}

type field struct {
	label string
	value string
}

// fields prints a labelled block, one field per line, skipping empty values.
func (u ui) fields(fields []field) {
	width := 0
	for _, f := range fields {
		if f.value != "" {
			width = max(width, len(f.label))
		}
	}
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		label := f.label + strings.Repeat(" ", width-len(f.label))
		if u.style.glyphs {
			u.line("  " + u.style.dim(label) + "  " + f.value)
			continue
		}
		u.line(label + "  " + f.value)
	}
}

// instant is a time the Admin API sent as an RFC 3339 string or as
// milliseconds since the epoch.
type instant struct {
	at  time.Time
	raw string
}

func (t *instant) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = instantOf(value)
	return nil
}

func instantOf(value any) instant {
	switch typed := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return instant{at: parsed}
		}
		return instant{raw: typed}
	case float64:
		if typed > 0 {
			return instant{at: time.UnixMilli(int64(typed))}
		}
	}
	return instant{}
}

func (u ui) ago(t instant) string {
	if t.at.IsZero() {
		return dash(t.raw)
	}
	return relative(t.at, u.now())
}

// stamp is an absolute time with how long ago it was, for a detail view.
func (u ui) stamp(t instant) string {
	if t.at.IsZero() {
		return t.raw
	}
	return t.at.UTC().Format("2006-01-02 15:04:05 UTC") + " " + u.style.dim("("+relative(t.at, u.now())+")")
}

func relative(at, now time.Time) string {
	elapsed := now.Sub(at)
	suffix := " ago"
	if elapsed < 0 {
		elapsed, suffix = -elapsed, " from now"
	}
	switch {
	case elapsed < 5*time.Second:
		return "just now"
	case elapsed < time.Minute:
		return fmt.Sprintf("%ds%s", int(elapsed.Seconds()), suffix)
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm%s", int(elapsed.Minutes()), suffix)
	case elapsed < 48*time.Hour:
		return fmt.Sprintf("%dh%s", int(elapsed.Hours()), suffix)
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dd%s", int(elapsed.Hours()/24), suffix)
	}
	return at.UTC().Format("2006-01-02")
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
