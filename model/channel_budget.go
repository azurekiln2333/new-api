package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelBudgetScopeType identifies the owner of one budget account. Channel
// budgets bound every request a channel serves; key budgets bound one stable
// allocation of that channel. Both are local station-quota limits and are
// always additional to wallet/subscription and API-token charging.
type ChannelBudgetScopeType string

const (
	BudgetScopeChannel ChannelBudgetScopeType = "channel"
	BudgetScopeKey     ChannelBudgetScopeType = "key"
)

// Reservation phases.
//
//	pending  - holds are live, nothing has been charged yet
//	open     - charged once, but a linked asynchronous task may still revise it
//	settled  - charged and terminal
//	released - holds released without any charge, or the charge was fully refunded
const (
	BudgetReservationPhasePending  = "pending"
	BudgetReservationPhaseOpen     = "open"
	BudgetReservationPhaseSettled  = "settled"
	BudgetReservationPhaseReleased = "released"
)

// Budget errors. They are sentinel values so callers can map them onto a
// retryable gateway error or a management-API conflict without string matching.
var (
	// ErrBudgetStaleVersion means a management update raced another change.
	ErrBudgetStaleVersion = errors.New("channel budget configuration version is stale")
	// ErrBudgetConfigInvalid means the requested limit is out of range.
	ErrBudgetConfigInvalid = errors.New("channel budget configuration is invalid")
	// ErrBudgetExhausted means the reserve would exceed an enforced limit.
	ErrBudgetExhausted = errors.New("channel budget is exhausted")
	// ErrBudgetAttemptConflict means the same attempt key was reserved with a
	// different payload, so the existing reservation cannot be reused.
	ErrBudgetAttemptConflict = errors.New("channel budget attempt key conflicts with a different reservation")
	// ErrBudgetAllocationInvalid means the allocation identity is unknown,
	// retired, owned by another user or has moved on to another revision.
	ErrBudgetAllocationInvalid = errors.New("channel key allocation is not eligible for this attempt")
	// ErrBudgetReservationNotFound means the reservation id is unknown.
	ErrBudgetReservationNotFound = errors.New("channel budget reservation was not found")
	// ErrBudgetStaleRevision means a settlement/refund used a revision older
	// than the one already committed.
	ErrBudgetStaleRevision = errors.New("channel budget settlement revision is stale")
)

// budgetLimitUpperBound is the largest accepted configured limit: the same
// JavaScript-safe bound the wallet uses, so the console can round-trip it.
const budgetLimitUpperBound = int64(common.MaxWalletQuota)

// budgetKeyAllocationTable is the shared allocation table owned by the Key
// allocation store. The budget ledger only reads the identity columns below,
// so it never depends on the full allocation struct and never creates a second
// allocation table.
const budgetKeyAllocationTable = "channel_key_entries"

// ChannelBudgetAccount is one budget ledger account.
//
// The account counters are authoritative across gateway nodes; no approximation
// is kept in Redis. Unlimited accounts (Enabled=false) still record usage so a
// limit can be enabled later without losing in-flight reservations.
type ChannelBudgetAccount struct {
	Id        int64                  `gorm:"primaryKey;autoIncrement" json:"id"`
	ScopeType ChannelBudgetScopeType `gorm:"type:varchar(16);not null;uniqueIndex:idx_channel_budget_scope,priority:1" json:"scope_type"`
	ScopeId   int64                  `gorm:"not null;uniqueIndex:idx_channel_budget_scope,priority:2" json:"scope_id"`
	// ChannelId is the owning channel of either scope. It lets one query read
	// every account a channel row needs without a join.
	ChannelId  int   `gorm:"not null;index:idx_channel_budget_channel" json:"channel_id"`
	Enabled    bool  `gorm:"not null" json:"enabled"`
	LimitQuota int64 `gorm:"not null;default:0" json:"limit_quota"`
	// PeriodUsedQuota is the net actual charge of the current period.
	PeriodUsedQuota int64 `gorm:"not null;default:0" json:"period_used_quota"`
	// LifetimeUsedQuota is the net actual charge since the ledger started.
	LifetimeUsedQuota int64 `gorm:"not null;default:0" json:"lifetime_used_quota"`
	// ReservedQuota is the sum of live holds.
	ReservedQuota int64 `gorm:"not null;default:0" json:"reserved_quota"`
	// ResetEpoch increments on every usage reset. A settlement belongs to the
	// epoch current when it is first charged.
	ResetEpoch int64 `gorm:"not null;default:0" json:"reset_epoch"`
	// ConfigVersion increments on every management change (limit or reset).
	ConfigVersion int64 `gorm:"not null;default:0" json:"config_version"`
	// AccountingStartedAt is when the ledger began counting this scope.
	AccountingStartedAt int64 `gorm:"not null;default:0" json:"accounting_started_at"`
	// Merge bookkeeping for retired allocations. Zero means "not merged".
	MergeTargetId    int64 `gorm:"not null;default:0" json:"merge_target_id"`
	MergeSourceEpoch int64 `gorm:"not null;default:0" json:"merge_source_epoch"`
	MergeTargetEpoch int64 `gorm:"not null;default:0" json:"merge_target_epoch"`
	UpdatedAt        int64 `gorm:"not null;default:0" json:"updated_at"`
}

func (ChannelBudgetAccount) TableName() string { return "channel_budget_accounts" }

// ChannelBudgetReservation is one reserved upstream attempt. It carries no
// credential, request body or token.
type ChannelBudgetReservation struct {
	Id int64 `gorm:"primaryKey;autoIncrement" json:"id"`
	// AttemptKey is an opaque per-attempt identity; a replay of the same
	// attempt must not reserve twice.
	AttemptKey         string `gorm:"type:varchar(160);not null;uniqueIndex:idx_channel_budget_attempt" json:"attempt_key"`
	ChannelId          int    `gorm:"not null;index:idx_channel_budget_reservation_channel" json:"channel_id"`
	AllocationId       int64  `gorm:"not null;default:0;index:idx_channel_budget_reservation_allocation" json:"allocation_id"`
	AllocationRevision int64  `gorm:"not null;default:0" json:"allocation_revision"`
	UserId             int    `gorm:"not null;default:0" json:"user_id"`
	ModelName          string `gorm:"type:varchar(128);not null;default:''" json:"model_name"`
	ReservedQuota      int64  `gorm:"not null;default:0" json:"reserved_quota"`
	// Phase is one of the BudgetReservationPhase* constants.
	Phase string `gorm:"type:varchar(16);not null;default:'pending'" json:"phase"`
	// TaskId links a durable asynchronous task.
	TaskId string `gorm:"type:varchar(64);not null;default:'';index:idx_channel_budget_reservation_task" json:"task_id"`
	// OwnerNode and ExpiresAt support crash recovery of abandoned attempts.
	OwnerNode string `gorm:"type:varchar(64);not null;default:''" json:"owner_node"`
	ExpiresAt int64  `gorm:"not null;default:0" json:"expires_at"`
	// SettledQuota is the cumulative actual charge recorded for this attempt.
	SettledQuota int64 `gorm:"not null;default:0" json:"settled_quota"`
	// SettlementRevision grows monotonically; replays of the same revision and
	// total are safe, older revisions cannot replace newer state.
	SettlementRevision  int64 `gorm:"not null;default:0" json:"settlement_revision"`
	CreatedAt           int64 `gorm:"not null;default:0" json:"created_at"`
	UpdatedAt           int64 `gorm:"not null;default:0" json:"updated_at"`
	ForwardedAt         int64 `gorm:"not null;default:0" json:"-"`
	ReconcilePending    bool  `gorm:"not null" json:"-"`
	DesiredQuota        int64 `gorm:"not null;default:0" json:"-"`
	DesiredRevision     int64 `gorm:"not null;default:0" json:"-"`
	DesiredFinal        bool  `gorm:"not null" json:"-"`
	DesiredRefund       bool  `gorm:"not null" json:"-"`
	NeedsReconciliation bool  `gorm:"not null" json:"-"`
}

func (ChannelBudgetReservation) TableName() string { return "channel_budget_reservations" }

// ChannelBudgetReservationItem is one account's share of a reservation.
type ChannelBudgetReservationItem struct {
	Id            int64                  `gorm:"primaryKey;autoIncrement" json:"id"`
	ReservationId int64                  `gorm:"not null;uniqueIndex:idx_channel_budget_item,priority:1" json:"reservation_id"`
	AccountId     int64                  `gorm:"not null;uniqueIndex:idx_channel_budget_item,priority:2" json:"account_id"`
	ScopeType     ChannelBudgetScopeType `gorm:"type:varchar(16);not null;default:''" json:"scope_type"`
	ScopeId       int64                  `gorm:"not null;default:0" json:"scope_id"`
	// HeldQuota is the amount currently held for this account. It returns to
	// zero on the first settlement or on release.
	HeldQuota int64 `gorm:"not null;default:0" json:"held_quota"`
	// SettledQuota is the cumulative actual share of this account.
	SettledQuota int64 `gorm:"not null;default:0" json:"settled_quota"`
	// Settled reports whether the item has already been charged once; only a
	// settled item freezes its settlement epoch.
	Settled bool `gorm:"not null" json:"settled"`
	// SettlementEpoch is the account reset epoch this item was first charged
	// in. A pending item is assigned the epoch current at settlement time, so a
	// call that finishes after a reset contributes to the new period.
	SettlementEpoch int64 `gorm:"not null;default:0" json:"settlement_epoch"`
}

func (ChannelBudgetReservationItem) TableName() string { return "channel_budget_reservation_items" }

// ChannelBudgetOperation records an idempotent management operation so a
// replayed request returns the original result instead of resetting twice.
type ChannelBudgetOperation struct {
	Id            int64                  `gorm:"primaryKey;autoIncrement" json:"id"`
	OperationId   string                 `gorm:"type:varchar(96);not null;uniqueIndex:idx_channel_budget_operation" json:"operation_id"`
	OperationType string                 `gorm:"type:varchar(24);not null;default:''" json:"operation_type"`
	ScopeType     ChannelBudgetScopeType `gorm:"type:varchar(16);not null;default:''" json:"scope_type"`
	ScopeId       int64                  `gorm:"not null;default:0" json:"scope_id"`
	ResultJSON    string                 `gorm:"type:text;not null" json:"result_json"`
	CreatedAt     int64                  `gorm:"not null;default:0" json:"created_at"`
}

func (ChannelBudgetOperation) TableName() string { return "channel_budget_operations" }

// ChannelBudgetView is the read projection used by the management API and the
// routing summaries. RemainingQuota is nil when the scope is unlimited.
type ChannelBudgetView struct {
	ScopeType           ChannelBudgetScopeType `json:"scope_type"`
	ScopeId             int64                  `json:"scope_id,string"`
	ChannelId           int                    `json:"channel_id"`
	Enabled             bool                   `json:"enabled"`
	LimitQuota          int64                  `json:"limit_quota"`
	PeriodUsedQuota     int64                  `json:"period_used_quota"`
	LifetimeUsedQuota   int64                  `json:"lifetime_used_quota"`
	ReservedQuota       int64                  `json:"reserved_quota"`
	RemainingQuota      *int64                 `json:"remaining_quota,omitempty"`
	ResetEpoch          int64                  `json:"reset_epoch"`
	ConfigVersion       int64                  `json:"config_version"`
	AccountingStartedAt int64                  `json:"accounting_started_at"`
	// Exhausted reports that an enforced limit has no capacity left. It is
	// false for unlimited scopes.
	Exhausted     bool `json:"exhausted"`
	LedgerEnabled bool `json:"ledger_enabled"`
}

// BudgetSelection is the attempt identity the ledger validates before holding
// capacity. AllocationId is the stable channel_key_entries id, never a key
// text or a current array index.
type BudgetSelection struct {
	ChannelID          int
	AllocationID       int64
	AllocationRevision int64
	UserID             int
	Group              string
	ModelName          string
}

// UsesKeyScope reports whether the attempt is attributed to one allocation.
func (s BudgetSelection) UsesKeyScope() bool { return s.AllocationID > 0 }

// BudgetReserveRequest reserves one upstream attempt against every scope the
// selection touches. ReserveQuota is the validated station estimate.
type BudgetReserveRequest struct {
	AttemptKey   string
	Selection    BudgetSelection
	ReserveQuota int64
	OwnerNode    string
	ExpiresAt    int64
	TaskID       string
}

// BudgetResetRequest resets one scope's current period.
type BudgetResetRequest struct {
	Scope           ChannelBudgetScopeType
	ScopeID         int64
	ExpectedVersion int64
	OperationID     string
}

// BudgetSettleRequest records the cumulative actual charge of one attempt.
type BudgetSettleRequest struct {
	ReservationID    int64
	Revision         int64
	TotalActualQuota int64
	// Final marks the terminal billing state. A linked task keeps its
	// reservation open until its settlement is durable.
	Final  bool
	TaskID string
}

// BudgetRefundRequest releases a pending attempt or reverses a confirmed
// funding refund of an already settled one.
type BudgetRefundRequest struct {
	ReservationID int64
	Revision      int64
	Reason        string
}

// BudgetMergeRequest retires one allocation's budget account into a surviving
// account. SourceEpoch/TargetEpoch are the reset epochs observed when the
// deduplication was previewed.
type BudgetMergeRequest struct {
	SourceScope ChannelBudgetScopeType
	SourceID    int64
	TargetScope ChannelBudgetScopeType
	TargetID    int64
	SourceEpoch int64
	TargetEpoch int64
}

// budgetKeyAllocation is a narrow projection of the shared allocation row. The
// budget ledger only needs identity and eligibility columns, so it never
// depends on the full allocation struct.
type budgetKeyAllocation struct {
	Id             int64  `gorm:"column:id;primaryKey"`
	ChannelId      int    `gorm:"column:channel_id"`
	Position       int    `gorm:"column:position"`
	BoundUserId    int    `gorm:"column:bound_user_id"`
	Status         int    `gorm:"column:status"`
	ConfigRevision int64  `gorm:"column:config_revision"`
	Models         string `gorm:"column:models"`
	ScopeMode      string `gorm:"column:scope_mode"`
	RetiredAt      int64  `gorm:"column:retired_at"`
}

func (budgetKeyAllocation) TableName() string { return budgetKeyAllocationTable }

func newBudgetView(account *ChannelBudgetAccount, ledgerEnabled bool) *ChannelBudgetView {
	view := &ChannelBudgetView{
		ScopeType:           account.ScopeType,
		LedgerEnabled:       ledgerEnabled,
		ScopeId:             account.ScopeId,
		ChannelId:           account.ChannelId,
		Enabled:             account.Enabled,
		LimitQuota:          account.LimitQuota,
		PeriodUsedQuota:     account.PeriodUsedQuota,
		LifetimeUsedQuota:   account.LifetimeUsedQuota,
		ReservedQuota:       account.ReservedQuota,
		ResetEpoch:          account.ResetEpoch,
		ConfigVersion:       account.ConfigVersion,
		AccountingStartedAt: account.AccountingStartedAt,
	}
	view.RemainingQuota, view.Exhausted = budgetCapacity(account)
	return view
}

// budgetCapacity returns the enforceable remaining amount and the exhausted
// flag. Unlimited scopes report no remaining value at all.
func budgetCapacity(account *ChannelBudgetAccount) (*int64, bool) {
	if account == nil || !account.Enabled {
		return nil, false
	}
	remaining := account.LimitQuota - account.PeriodUsedQuota
	if remaining > 0 {
		remaining -= account.ReservedQuota
	}
	if remaining < 0 {
		// Actual usage may exceed the configured limit; the console shows a
		// floor of zero and new admission is blocked.
		remaining = 0
	}
	return &remaining, remaining <= 0
}

// budgetAddChecked adds two non-negative counters and refuses to wrap.
func budgetAddChecked(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, ErrBudgetConfigInvalid
	}
	sum := a + b
	if sum < a {
		return 0, ErrBudgetConfigInvalid
	}
	return sum, nil
}

// budgetSubChecked subtracts b from a, refusing to produce a negative counter.
func budgetSubChecked(a, b int64) (int64, error) {
	if a < 0 || b < 0 {
		return 0, ErrBudgetConfigInvalid
	}
	if b > a {
		return 0, ErrBudgetConfigInvalid
	}
	return a - b, nil
}

func normalizeBudgetScope(scope ChannelBudgetScopeType) (ChannelBudgetScopeType, error) {
	switch ChannelBudgetScopeType(scope) {
	case BudgetScopeChannel:
		return BudgetScopeChannel, nil
	case BudgetScopeKey:
		return BudgetScopeKey, nil
	default:
		return "", fmt.Errorf("%w: unknown scope", ErrBudgetConfigInvalid)
	}
}

func validateBudgetLimit(enabled bool, limitQuota int64) error {
	if limitQuota < 0 || limitQuota > budgetLimitUpperBound {
		return fmt.Errorf("%w: limit %d", ErrBudgetConfigInvalid, limitQuota)
	}
	if !enabled && limitQuota != 0 {
		// Turning enforcement off keeps the counters but the unused limit is
		// meaningless; storing it would confuse a later re-enable.
		return fmt.Errorf("%w: a disabled budget must not carry a limit", ErrBudgetConfigInvalid)
	}
	return nil
}

// MigrateChannelBudgets creates the shared ledger tables. It is idempotent and
// is called from the primary-database migration on every start.
func MigrateChannelBudgets(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is unavailable")
	}
	return db.AutoMigrate(
		&ChannelBudgetAccount{},
		&ChannelBudgetReservation{},
		&ChannelBudgetReservationItem{},
		&ChannelBudgetOperation{},
	)
}

// budgetLookupAllocation reads the allocation identity from the shared table
// and validates that the attempt may use it.
func budgetLookupAllocation(tx *gorm.DB, selection BudgetSelection) (*budgetKeyAllocation, error) {
	if selection.ChannelID <= 0 || selection.AllocationID <= 0 || selection.AllocationRevision <= 0 || selection.UserID <= 0 {
		return nil, fmt.Errorf("%w: channel and allocation are required", ErrBudgetAllocationInvalid)
	}
	if strings.TrimSpace(selection.ModelName) == "" {
		return nil, fmt.Errorf("%w: model is required", ErrBudgetAllocationInvalid)
	}
	if !tx.Migrator().HasTable(budgetKeyAllocationTable) {
		// The allocation store has not created its table on this deployment:
		// the attempt cannot be attributed, so admission must not proceed on an
		// unverifiable key budget.
		return nil, fmt.Errorf("%w: allocation store is not available", ErrBudgetAllocationInvalid)
	}
	allocation := &budgetKeyAllocation{}
	if err := lockForUpdate(tx).Where("id = ?", selection.AllocationID).First(allocation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: allocation %d not found", ErrBudgetAllocationInvalid, selection.AllocationID)
		}
		return nil, err
	}
	if allocation.ChannelId != selection.ChannelID {
		return nil, fmt.Errorf("%w: allocation belongs to another channel", ErrBudgetAllocationInvalid)
	}
	if allocation.RetiredAt != 0 {
		return nil, fmt.Errorf("%w: allocation is retired", ErrBudgetAllocationInvalid)
	}
	if allocation.Status != common.ChannelStatusEnabled || allocation.ConfigRevision != selection.AllocationRevision {
		return nil, fmt.Errorf("%w: allocation revision moved", ErrBudgetAllocationInvalid)
	}
	if allocation.BoundUserId != 0 && allocation.BoundUserId != selection.UserID {
		return nil, fmt.Errorf("%w: allocation is bound to another user", ErrBudgetAllocationInvalid)
	}
	var channel Channel
	if err := tx.First(&channel, selection.ChannelID).Error; err != nil {
		return nil, err
	}
	// Channel-layer scope uses the routing namespace: the request name plus its
	// RoutingMatchModelName alias, exactly as abilities and candidate lookup do.
	if !budgetScopeServesModel(channel.GetModels(), selection.ModelName) {
		return nil, ErrBudgetAllocationInvalid
	}
	switch allocation.ScopeMode {
	case "all", "":
	case "allowlist":
		var models []string
		if common.UnmarshalJsonStr(allocation.Models, &models) != nil {
			return nil, ErrBudgetAllocationInvalid
		}
		// Only allocation capability accepts the mapped upstream name. Channel
		// and group authorization above/below stay in the request namespace.
		if !budgetScopeServesModel(models, selection.ModelName) && !channel.keyScopeServesModel(allocation.Position, models, selection.ModelName) {
			return nil, ErrBudgetAllocationInvalid
		}
	default:
		return nil, ErrBudgetAllocationInvalid
	}
	if allocation.BoundUserId == 0 {
		if !slices.ContainsFunc(strings.Split(channel.Group, ","), func(group string) bool { return strings.TrimSpace(group) == selection.Group }) {
			return nil, ErrBudgetAllocationInvalid
		}
		var other dto.ChannelOtherSettings
		if channel.OtherSettings != "" && common.UnmarshalJsonStr(channel.OtherSettings, &other) != nil {
			return nil, ErrBudgetAllocationInvalid
		}
		// A group with no configured whitelist keeps every channel model; an
		// explicit whitelist narrows the same namespace.
		if models := other.GroupModels[selection.Group]; len(models) > 0 && !budgetScopeServesModel(models, selection.ModelName) {
			return nil, ErrBudgetAllocationInvalid
		}
	}
	return allocation, nil
}

// budgetScopeServesModel reports whether a configured model list admits the
// requested name. Lists may hold the exact request name or its routing alias,
// so both are accepted; an entry that is neither admits nothing.
func budgetScopeServesModel(models []string, modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" || len(models) == 0 {
		return false
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	return slices.ContainsFunc(models, func(entry string) bool {
		entry = strings.TrimSpace(entry)
		if entry == modelName {
			return true
		}
		return normalized != "" && normalized != modelName && entry == normalized
	})
}

// budgetEnsureAccount creates the accounting row when the scope has never been
// used and returns it locked for the rest of the transaction.
func budgetEnsureAccount(tx *gorm.DB, scope ChannelBudgetScopeType, scopeID int64, channelID int) (*ChannelBudgetAccount, error) {
	now := common.GetTimestamp()
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ChannelBudgetAccount{
		ScopeType:           scope,
		ScopeId:             scopeID,
		ChannelId:           channelID,
		AccountingStartedAt: now,
		UpdatedAt:           now,
	}).Error; err != nil {
		return nil, err
	}
	account := &ChannelBudgetAccount{}
	if err := lockForUpdate(tx).Where("scope_type = ? AND scope_id = ?", scope, scopeID).First(account).Error; err != nil {
		return nil, err
	}
	return account, nil
}

func budgetResolveChannelID(tx *gorm.DB, scope ChannelBudgetScopeType, scopeID int64) (int, error) {
	if scope == BudgetScopeChannel {
		if scopeID <= 0 {
			return 0, fmt.Errorf("%w: channel scope id is required", ErrBudgetConfigInvalid)
		}
		return int(scopeID), nil
	}
	if scopeID <= 0 {
		return 0, fmt.Errorf("%w: allocation id is required", ErrBudgetConfigInvalid)
	}
	if !tx.Migrator().HasTable(budgetKeyAllocationTable) {
		return 0, fmt.Errorf("%w: allocation store is not available", ErrBudgetAllocationInvalid)
	}
	allocation := &budgetKeyAllocation{}
	if err := tx.Select("id", "channel_id", "retired_at").Where("id = ?", scopeID).First(allocation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, fmt.Errorf("%w: allocation %d not found", ErrBudgetAllocationInvalid, scopeID)
		}
		return 0, err
	}
	return allocation.ChannelId, nil
}
