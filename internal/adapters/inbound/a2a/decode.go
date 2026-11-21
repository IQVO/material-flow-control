package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// decodeStrict decodes one JSON document, refusing unknown fields and trailing data; absent arguments
// decode as {} so the skill's own required checks speak.
//
// carry (Task 21 review): "one document" means the decoder's NEXT token is io.EOF (dec.More is false on a stray
// `}` or `]`, so it let them through), and the keys must match the json tags EXACTLY (encoding/json matches
// case-insensitively, so `StationID` filled stationId).
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON document")
	}
	return exactKeys(raw, dst)
}

// exactKeys refuses any key of the raw object that is not, byte for byte, one of dst's json names.
func exactKeys(raw []byte, dst any) error {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return err
	}
	names := jsonNames(reflect.TypeOf(dst).Elem())
	for key := range keys {
		if _, ok := names[key]; !ok {
			return fmt.Errorf("json: unknown field %q", key)
		}
	}
	return nil
}

// jsonNames is the set of json field names of a struct type (its tags, else the Go field name).
func jsonNames(t reflect.Type) map[string]struct{} {
	names := map[string]struct{}{}
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" {
			name = t.Field(i).Name
		}
		names[name] = struct{}{}
	}
	return names
}
