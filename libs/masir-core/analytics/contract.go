package analytics

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

const SchemaVersion = 1

// EventID is random, valid as a PostHog UUID, and unrelated to the machine or user.
func EventID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// Operation supplies a common denominator and correlation ID for one attempt.
// Repeated retries within the operation keep its ID; a new attempt gets a new ID.
type Operation struct {
	ID      string
	Name    string
	Source  string
	Started time.Time
}

func NewOperation(name, source string) Operation {
	return Operation{ID: EventID(), Name: name, Source: source, Started: time.Now()}
}
func (o Operation) Props(outcome string) map[string]any {
	p := map[string]any{"operation_id": o.ID, "operation": o.Name, "source": o.Source}
	if outcome != "" {
		p["outcome"] = outcome
		p["success"] = outcome == "success"
		p["duration_ms"] = time.Since(o.Started).Milliseconds()
	}
	return p
}

// ValidEventID checks the canonical UUID spelling accepted by the wire contract.
func ValidEventID(id string) bool { return eventUUID.MatchString(id) }
