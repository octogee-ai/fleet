package cli

import (
	"bytes"
	"encoding/json"
)

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
