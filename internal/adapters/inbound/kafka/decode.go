package kafka

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
)

// payload is a decoded event `data`: check rejects what can never be applied (a poison message).
type payload interface{ check() error }

// handle decodes data into T, checks it, and applies it.
func handle[T payload](apply func(ctx context.Context, eventID, eventType string, d T) error) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[T](e)
		if err != nil {
			return err
		}
		if err := d.check(); err != nil {
			return err
		}
		return apply(ctx, e.ID(), e.Type(), d)
	}
}

// required fails when a required string is empty or a required time is zero: pairs of name and value.
func required(pairs ...any) error {
	var missing []string
	for i := 0; i+1 < len(pairs); i += 2 {
		switch v := pairs[i+1].(type) {
		case string:
			if v == "" {
				missing = append(missing, pairs[i].(string))
			}
		case time.Time:
			if v.IsZero() {
				missing = append(missing, pairs[i].(string))
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", errBadPayload, strings.Join(missing, ", "))
	}
	return nil
}

// oneOf fails a value outside its enum.
func oneOf(name, v string, allowed ...string) error {
	if slices.Contains(allowed, v) {
		return nil
	}
	return fmt.Errorf("%w: %s %q is not one of %v", errBadPayload, name, v, allowed)
}
