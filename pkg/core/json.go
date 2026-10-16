// Package core defines credential-free domain values and execution contracts.
package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSafeInteger uint64 = 9007199254740991

// DecodeJSON rejects duplicate and unknown fields, invalid UTF-8, trailing input,
// null non-pointer fields, and fields marked required:"true" when absent.
// Optional pointer fields intentionally collapse missing and null. RawMessage is
// the escape hatch for provider-owned data. This does not apply defaults unless
// the destination's UnmarshalJSON implementation explicitly supplies them.
func DecodeJSON(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid JSON UTF-8")
	}
	value, err := parseJSON(data)
	if err != nil {
		return err
	}
	t := reflect.TypeOf(destination)
	if t == nil || t.Kind() != reflect.Pointer {
		return fmt.Errorf("JSON destination must be a pointer")
	}
	if err = checkShape(value, t.Elem(), "$"); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	d.UseNumber()
	return d.Decode(destination)
}

func parseJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	value, err := readValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON input")
	}
	return value, nil
}
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 128 {
		return nil, fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		result := map[string]any{}
		for d.More() {
			keyToken, err := d.Token()
			if err != nil {
				return nil, err
			}
			key := keyToken.(string)
			if _, exists := result[key]; exists {
				return nil, fmt.Errorf("duplicate JSON field %q", key)
			}
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		_, err = d.Token()
		return result, err
	case json.Delim('['):
		result := []any{}
		for d.More() {
			value, err := readValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		_, err = d.Token()
		return result, err
	default:
		return token, nil
	}
}

var rawType = reflect.TypeFor[json.RawMessage]()

func checkShape(value any, t reflect.Type, path string) error {
	if t == rawType || t.Kind() == reflect.Interface {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		if value == nil {
			return nil
		}
		return checkShape(value, t.Elem(), path)
	}
	if value == nil {
		return fmt.Errorf("%s cannot be null", path)
	}
	switch t.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			known[name] = true
			v, found := object[name]
			if !found {
				if field.Tag.Get("required") == "true" {
					return fmt.Errorf("missing field %s.%s", path, name)
				}
				continue
			}
			if err := checkShape(v, field.Type, path+"."+name); err != nil {
				return err
			}
		}
		for key := range object {
			if !known[key] {
				return fmt.Errorf("unknown JSON field %s.%s", path, key)
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		if values, ok := value.([]any); ok {
			for _, v := range values {
				if err := checkShape(v, t.Elem(), path+"[]"); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		if values, ok := value.(map[string]any); ok {
			for name, v := range values {
				if err := checkShape(v, t.Elem(), path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// CanonicalJSON is Pot canonical JSON v1: UTF-8 keys sorted by Go string byte
// order, compact syntax, explicit nulls and no HTML escaping. Numbers must use
// exact integer syntax or finite IEEE-754 binary64 decimal/exponent syntax.
// Integers retain all 64 bits; decimals use Go's shortest round-trip 'g' format.
// Negative zero is normalized to zero. This is Pot v1, not RFC 8785.
// These exact bytes, never incidental struct field or map ordering, bind hashes.
func CanonicalJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parsed, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err = canonical(&output, parsed); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func canonical(output *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		output.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				output.WriteByte(',')
			}
			writeString(output, key)
			output.WriteByte(':')
			if err := canonical(output, v[key]); err != nil {
				return err
			}
		}
		output.WriteByte('}')
	case []any:
		output.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				output.WriteByte(',')
			}
			if err := canonical(output, item); err != nil {
				return err
			}
		}
		output.WriteByte(']')
	case json.Number:
		s := v.String()
		if strings.ContainsAny(s, ".eE") {
			n, err := strconv.ParseFloat(s, 64)
			if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
				return fmt.Errorf("invalid canonical JSON number")
			}
			if n == 0 {
				output.WriteByte('0')
			} else {
				output.WriteString(strconv.FormatFloat(n, 'g', -1, 64))
			}
		} else if strings.HasPrefix(s, "-") {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return err
			}
			output.WriteString(strconv.FormatInt(n, 10))
		} else {
			n, err := strconv.ParseUint(s, 10, 64)
			if err != nil {
				return err
			}
			output.WriteString(strconv.FormatUint(n, 10))
		}
	case string:
		writeString(output, v)
	case nil:
		output.WriteString("null")
	case bool:
		output.WriteString(strconv.FormatBool(v))
	default:
		return fmt.Errorf("unsupported canonical JSON value")
	}
	return nil
}
func writeString(output *bytes.Buffer, value string) {
	e := json.NewEncoder(output)
	e.SetEscapeHTML(false)
	_ = e.Encode(value)
	data := output.Bytes()
	output.Truncate(len(data) - 1)
}
func HashJSON(value any) (string, error) {
	data, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
