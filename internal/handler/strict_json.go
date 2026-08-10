package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxStrictJSONDepth = 128

// decodeStrictJSONObject accepts exactly one JSON object. It rejects unknown
// fields during the typed decode and rejects duplicate keys before values can
// be overwritten by encoding/json, including keys in nested objects.
func decodeStrictJSONObject(raw []byte, dst any) error {
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func rejectDuplicateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return errors.New("top-level JSON value must be an object")
	}
	if err := walkJSONObject(decoder, 1); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON data: %w", err)
		}
		return fmt.Errorf("trailing JSON data after token %v", token)
	}
	return nil
}

// walkJSONObject consumes an object after its opening delimiter.
func walkJSONObject(decoder *json.Decoder, depth int) error {
	if depth > maxStrictJSONDepth {
		return fmt.Errorf("JSON nesting exceeds maximum depth %d", maxStrictJSONDepth)
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key must be a string, got %v", keyToken)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate JSON object key %q", key)
		}
		seen[key] = struct{}{}

		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := walkJSONValue(decoder, value, depth); err != nil {
			return err
		}
	}
	closeToken, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := closeToken.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("malformed JSON object close token %v", closeToken)
	}
	return nil
}

// walkJSONArray consumes an array after its opening delimiter. Each object in
// an array gets its own key set; scalar string values are never treated as keys.
func walkJSONArray(decoder *json.Decoder, depth int) error {
	if depth > maxStrictJSONDepth {
		return fmt.Errorf("JSON nesting exceeds maximum depth %d", maxStrictJSONDepth)
	}
	for decoder.More() {
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := walkJSONValue(decoder, value, depth); err != nil {
			return err
		}
	}
	closeToken, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := closeToken.(json.Delim); !ok || delim != ']' {
		return fmt.Errorf("malformed JSON array close token %v", closeToken)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, token json.Token, parentDepth int) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return walkJSONObject(decoder, parentDepth+1)
	case '[':
		return walkJSONArray(decoder, parentDepth+1)
	default:
		return fmt.Errorf("unexpected JSON close token %q", delim)
	}
}
