package common

// BudgetAttempt is the local channel/allocation budget hold of one upstream
// attempt. The ledger itself lives in the model package; this struct only
// carries the identity a transport needs so it can settle, revise or release
// the hold without re-resolving a key index or credential.
//
// A zero-value BudgetAttempt means "no attempt hold" (for example a probe or an
// administrative model discovery, which never settle user usage).
type BudgetAttempt struct {
	// ReservationId is the durable ledger reservation. Zero means the attempt
	// created no hold.
	ReservationID int64
	// AttemptKey is the opaque per-attempt identity used for idempotency.
	AttemptKey string
	// Revision grows monotonically per settlement of this reservation.
	Revision int64
	// AllocationID is the stable channel_key_entries id the attempt used.
	AllocationID int64
	// AllocationRevision is the allocation configuration version at reserve
	// time, so a late write cannot be attributed to a newer configuration.
	AllocationRevision int64
	// EstimatedQuota is the station estimate the hold was taken against.
	EstimatedQuota int64
	// Forwarded means the final admission check has committed before dispatch.
	Forwarded bool
	// ChargedQuota is the total funding committed for this attempt, including
	// additional violation fees even when a later token write fails.
	ChargedQuota int
	// Settled reports whether this attempt already recorded a charge.
	Settled bool
}

// HasBudgetReservation reports whether the attempt created a ledger hold.
func (b BudgetAttempt) HasBudgetReservation() bool { return b.ReservationID > 0 }
