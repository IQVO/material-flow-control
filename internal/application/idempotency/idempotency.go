// Package idempotency holds the value the Idempotency-Key middleware stores per key.
package idempotency

import "time"

// IdempotencyRecord is one stored answer of a POST made with an Idempotency-Key.
type IdempotencyRecord struct {
	Key, Fingerprint string
	Status           int
	ContentType      string
	Body             []byte
	CreatedAt        time.Time
}
