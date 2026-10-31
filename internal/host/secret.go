package host

import (
	"regexp"
	"unicode/utf8"
)

// Secret is one top-level request field whose value must not be copied into any
// Go string, map or marshalled structure. The caller owns Value and clears it.
type Secret struct {
	Field string
	Value []byte
}

const maxSecretBytes = 1024

func (secret Secret) check() error {
	if !secretField.MatchString(secret.Field) || len(secret.Value) == 0 || len(secret.Value) > maxSecretBytes || !utf8.Valid(secret.Value) {
		return ErrProtocolSession
	}
	return nil
}

var secretField = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// spliceSecrets returns body (a validated JSON-RPC request whose last member is
// the params object) with each secret appended to that object. The result lives
// in one buffer allocated at its worst-case size, so appending never reallocates
// and leaves no uncleared copy behind. The caller clears it.
func spliceSecrets(body []byte, secrets []Secret) ([]byte, error) {
	if len(body) < 2 || body[len(body)-1] != '}' || body[len(body)-2] != '}' {
		return nil, ErrProtocolSession
	}
	capacity := len(body)
	for _, secret := range secrets {
		if secret.check() != nil {
			return nil, ErrProtocolSession
		}
		capacity += len(secret.Field) + 6*len(secret.Value) + 6 // ,"f":"..." with every byte as \u00XX at worst
	}
	frame := make([]byte, 0, capacity)
	frame = append(frame, body[:len(body)-2]...)
	for _, secret := range secrets {
		frame = append(frame, ',', '"')
		frame = append(frame, secret.Field...)
		frame = append(frame, '"', ':', '"')
		frame = appendEscaped(frame, secret.Value)
		frame = append(frame, '"')
	}
	frame = append(frame, '}', '}')
	return frame, nil
}

const hexDigits = "0123456789abcdef"

// appendEscaped writes the JSON string escape of valid UTF-8 text.
func appendEscaped(dst, text []byte) []byte {
	for _, c := range text {
		switch {
		case c == '"' || c == '\\':
			dst = append(dst, '\\', c)
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&15])
		default:
			dst = append(dst, c)
		}
	}
	return dst
}
