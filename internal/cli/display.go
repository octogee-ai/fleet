package cli

import (
	"bytes"
	"encoding/json"

	"github.com/octogee-ai/octogee-fleet/internal/terminal"
)

// decodeForDisplay decodes an Admin API value for a text view with every
// string escaped, so that nothing the Admin API sends can drive the terminal.
// JSON output prints the value as sent instead.
func decodeForDisplay(data []byte, view any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	escaped, err := json.Marshal(escapeStrings(value))
	if err != nil {
		return err
	}
	return json.Unmarshal(escaped, view)
}

func escapeStrings(value any) any {
	switch typed := value.(type) {
	case string:
		return terminal.Escape(typed)
	case []any:
		for index := range typed {
			typed[index] = escapeStrings(typed[index])
		}
		return typed
	case map[string]any:
		escaped := make(map[string]any, len(typed))
		for key, item := range typed {
			escaped[terminal.Escape(key)] = escapeStrings(item)
		}
		return escaped
	}
	return value
}

// looseText is a field shown to the operator and never acted on. A string is
// kept as sent, null is empty, and any other value is kept as its JSON, so an
// unexpected value is shown instead of failing the command.
type looseText string

func (t *looseText) UnmarshalJSON(data []byte) error {
	var text string
	switch {
	case json.Unmarshal(data, &text) == nil:
		*t = looseText(text)
	case string(bytes.TrimSpace(data)) == "null":
		*t = ""
	default:
		*t = looseText(compactJSON(data))
	}
	return nil
}

// lenient is a structured field shown to the operator and never acted on. It
// decodes when it can and keeps what was sent when it cannot.
type lenient[T any] struct {
	value T
	ok    bool
	raw   json.RawMessage
}

func (l *lenient[T]) UnmarshalJSON(data []byte) error {
	l.raw = append(json.RawMessage{}, data...)
	l.ok = json.Unmarshal(data, &l.value) == nil
	if !l.ok {
		var zero T
		l.value = zero
	}
	return nil
}

// unreadable is what was sent when it could not be decoded, or nothing.
func (l lenient[T]) unreadable() string {
	if l.ok || len(l.raw) == 0 {
		return ""
	}
	return compactJSON(l.raw)
}

func compactJSON(data []byte) string {
	var compacted bytes.Buffer
	if json.Compact(&compacted, data) != nil {
		return string(data)
	}
	return compacted.String()
}
