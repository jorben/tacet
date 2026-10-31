package config

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxContentBytes = 1024 * 1024
const maxDepth = 64
const maxValues = 100000

// Canonical validates bounded JSON without duplicate keys and normalizes
// whitespace, object-key order and exact decimal number spelling. It never
// roundtrips through float64, so a large numeric identifier cannot be rounded.
// Array order and string contents (including whitespace) are preserved.
func Canonical(input []byte) ([]byte, error) {
	if len(input) == 0 || len(input) > MaxContentBytes || !utf8.Valid(input) {
		return nil, ErrContent
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	values := 0
	value, err := parseValue(decoder, 0, &values)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrContent
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrContent
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > MaxContentBytes {
		return nil, ErrContent
	}
	return encoded, nil
}

func parseValue(d *json.Decoder, depth int, values *int) (any, error) {
	*values++
	if depth > maxDepth || *values > maxValues {
		return nil, ErrContent
	}
	token, err := d.Token()
	if err != nil {
		return nil, ErrContent
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, ErrContent
				}
				name, ok := key.(string)
				if !ok {
					return nil, ErrContent
				}
				if _, exists := object[name]; exists {
					return nil, ErrContent
				}
				item, err := parseValue(d, depth+1, values)
				if err != nil {
					return nil, err
				}
				object[name] = item
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrContent
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				item, err := parseValue(d, depth+1, values)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrContent
			}
			return array, nil
		}
		return nil, ErrContent
	case json.Number:
		return canonicalNumber(value.String())
	default:
		return value, nil
	}
}

// Integer significand + exponent avoids large allocations for extreme exponents.
// The resulting json.Number remains a valid JSON number with exactly the same
// mathematical value. 1, 1.00 and 10e-1 therefore hash identically.
func canonicalNumber(value string) (json.Number, error) {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	exponent := 0
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		parsed, err := strconv.Atoi(value[index+1:])
		if err != nil || parsed < -100000 || parsed > 100000 {
			return "", ErrContent
		}
		exponent = parsed
		value = value[:index]
	}
	if index := strings.IndexByte(value, '.'); index >= 0 {
		exponent -= len(value) - index - 1
		value = value[:index] + value[index+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return json.Number("0"), nil
	}
	trimmed := strings.TrimRight(value, "0")
	exponent += len(value) - len(trimmed)
	// Keep ordinary integral values in integer lexical form: typed JSON DTOs
	// must be able to decode memory_bytes/dpi without accepting float coercion.
	// Bound expansion; extreme exponents stay scientific and never allocate an
	// exponent-sized buffer.
	if exponent >= 0 && exponent <= 1024 {
		return json.Number(sign + trimmed + strings.Repeat("0", exponent)), nil
	}
	if exponent < 0 && -exponent <= 1024 {
		point := len(trimmed) + exponent
		if point > 0 {
			return json.Number(sign + trimmed[:point] + "." + trimmed[point:]), nil
		}
		return json.Number(sign + "0." + strings.Repeat("0", -point) + trimmed), nil
	}
	return json.Number(sign + trimmed + "e" + strconv.Itoa(exponent)), nil
}
