package llm

import (
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Delta is newly streamed text of one content field, e.g. "sms.body".
type Delta struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}

// FieldStreamer extracts the text of selected string fields from JSON that
// arrives in arbitrary chunks (D34), so the UI can show each field live. It
// is for display only: the complete JSON is still decoded and validated when
// the stream ends (D15). Chunks may split escapes and UTF-8 characters
// anywhere; deltas are always whole characters.
type FieldStreamer struct {
	fields []string
	emit   func(Delta)

	stack []frame // open objects and arrays

	inString bool
	isKey    bool // the open string is an object key
	target   string
	key      strings.Builder // key being read
	out      []byte          // decoded text of the target field, not yet emitted

	escape      bool
	hex         []byte // digits of a \uXXXX escape
	pendingHigh rune   // high surrogate waiting for its low half
}

type frame struct {
	object    bool
	expectKey bool
	key       string
}

func NewFieldStreamer(fields []string, emit func(Delta)) *FieldStreamer {
	return &FieldStreamer{fields: fields, emit: emit}
}

func (s *FieldStreamer) Write(chunk string) {
	for i := 0; i < len(chunk); i++ {
		s.step(chunk[i])
	}
	s.flush(false)
}

func (s *FieldStreamer) step(c byte) {
	if !s.inString {
		s.structural(c)
		return
	}
	switch {
	case len(s.hex) > 0 || (s.escape && c == 'u'):
		s.escape = false
		if c != 'u' {
			s.hex = append(s.hex, c)
		} else {
			s.hex = append(s.hex, 'u')
		}
		if len(s.hex) == 5 { // "u" + 4 digits
			s.unicodeEscape(string(s.hex[1:]))
			s.hex = s.hex[:0]
		}
	case s.escape:
		s.escape = false
		s.dropHigh()
		s.text(string(unescape(c)))
	case c == '\\':
		s.escape = true
	case c == '"':
		s.endString()
	default:
		s.dropHigh()
		s.raw(c)
	}
}

// structural tracks objects, arrays and keys outside strings.
func (s *FieldStreamer) structural(c byte) {
	top := len(s.stack) - 1
	switch c {
	case '{', '[':
		s.stack = append(s.stack, frame{object: c == '{', expectKey: c == '{'})
	case '}', ']':
		if top >= 0 {
			s.stack = s.stack[:top]
		}
	case ':':
		if top >= 0 {
			s.stack[top].expectKey = false
		}
	case ',':
		if top >= 0 && s.stack[top].object {
			s.stack[top].expectKey = true
		}
	case '"':
		s.inString = true
		s.isKey = top >= 0 && s.stack[top].object && s.stack[top].expectKey
		s.target = ""
		if !s.isKey && slices.Contains(s.fields, s.path()) {
			s.target = s.path()
		}
	}
}

// path is the dotted key path of the value being read, e.g. "email.subject".
func (s *FieldStreamer) path() string {
	var keys []string
	for _, f := range s.stack {
		if !f.object {
			return "" // values inside arrays are never content fields
		}
		keys = append(keys, f.key)
	}
	return strings.Join(keys, ".")
}

func (s *FieldStreamer) endString() {
	s.dropHigh()
	if s.isKey {
		s.stack[len(s.stack)-1].key = s.key.String()
		s.key.Reset()
	} else {
		s.flush(true)
	}
	s.inString, s.isKey, s.target = false, false, ""
}

func (s *FieldStreamer) raw(c byte) {
	if s.isKey {
		s.key.WriteByte(c)
	} else if s.target != "" {
		s.out = append(s.out, c)
	}
}

func (s *FieldStreamer) text(t string) {
	for i := 0; i < len(t); i++ {
		s.raw(t[i])
	}
}

func (s *FieldStreamer) unicodeEscape(digits string) {
	var r rune
	for _, d := range digits {
		r <<= 4
		switch {
		case d >= '0' && d <= '9':
			r |= d - '0'
		case d >= 'a' && d <= 'f':
			r |= d - 'a' + 10
		case d >= 'A' && d <= 'F':
			r |= d - 'A' + 10
		}
	}
	switch {
	case utf16.IsSurrogate(r) && r < 0xDC00: // high half: wait for the low half
		s.dropHigh()
		s.pendingHigh = r
	case utf16.IsSurrogate(r) && s.pendingHigh != 0:
		s.text(string(utf16.DecodeRune(s.pendingHigh, r)))
		s.pendingHigh = 0
	default:
		s.dropHigh()
		s.text(string(r)) // a lone low surrogate becomes U+FFFD, as in encoding/json
	}
}

// dropHigh replaces an unpaired high surrogate with U+FFFD, as encoding/json does.
func (s *FieldStreamer) dropHigh() {
	if s.pendingHigh != 0 {
		s.pendingHigh = 0
		s.text(string(utf8.RuneError))
	}
}

// flush emits the target's decoded text. Unless the string has ended, an
// incomplete UTF-8 character at the end waits for the next chunk.
func (s *FieldStreamer) flush(final bool) {
	if s.target == "" || len(s.out) == 0 {
		return
	}
	n := len(s.out)
	if !final {
		n = completePrefix(s.out)
	}
	if n > 0 {
		s.emit(Delta{Field: s.target, Text: string(s.out[:n])})
		s.out = append(s.out[:0], s.out[n:]...)
	}
}

// completePrefix is the length of b without a trailing partial UTF-8 character.
func completePrefix(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}

func unescape(c byte) rune {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	case 'b':
		return '\b'
	case 'f':
		return '\f'
	default: // \" \\ \/
		return rune(c)
	}
}
