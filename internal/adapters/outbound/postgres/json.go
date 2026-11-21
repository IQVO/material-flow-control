package postgres

import (
	"encoding/json"
	"fmt"
)

// Aggregate state is stored as the JSON of its domain Snapshot (exported Go field names, no tags in the domain).
// Renaming a domain field is therefore a data migration of the JSONB columns: never rename one without it.

func encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode snapshot: %w", err)
	}
	return b, nil
}

func decode[T any](b []byte) (T, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return v, fmt.Errorf("decode snapshot: %w", err)
	}
	return v, nil
}
