// Package system is the production clock and id source.
package system

import (
	"time"

	"github.com/google/uuid"
)

// Clock is the wall clock, UTC.
type Clock struct{}

func (Clock) Now() time.Time { return time.Now().UTC() }

// IDs mints UUID v4 strings.
type IDs struct{}

func (IDs) NewID() string { return uuid.NewString() }
