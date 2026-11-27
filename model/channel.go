package model

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"

	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type Channel struct {
	Id                 int     `json:"id"`
	Type               int     `json:"type" gorm:"default:0"`
	Key                string  `json:"key" gorm:"not null"`
	OpenAIOrganization *string `json:"openai_organization"`
	TestModel          *string `json:"test_model"`
	Status             int     `json:"status" gorm:"default:1"`
	Name               string  `json:"name" gorm:"index"`
	Weight             *uint   `json:"weight" gorm:"default:0"`
	CreatedTime        int64   `json:"created_time" gorm:"bigint"`
	TestTime           int64   `json:"test_time" gorm:"bigint"`
	ResponseTime       int     `json:"response_time"` // in milliseconds
	BaseURL            *string `json:"base_url" gorm:"column:base_url;default:''"`
	Other              string  `json:"other"`
	Balance            float64 `json:"balance"` // in USD
	BalanceUpdatedTime int64   `json:"balance_updated_time" gorm:"bigint"`
	Models             string  `json:"models"`
	Group              string  `json:"group" gorm:"type:varchar(64);default:'default'"`
	UsedQuota          int64   `json:"used_quota" gorm:"bigint;default:0"`
	ModelMapping       *string `json:"model_mapping" gorm:"type:text"`
	//MaxInputTokens     *int    `json:"max_input_tokens" gorm:"default:0"`
	StatusCodeMapping *string `json:"status_code_mapping" gorm:"type:varchar(1024);default:''"`
	Priority          *int64  `json:"priority" gorm:"bigint;default:0"`
	AutoBan           *int    `json:"auto_ban" gorm:"default:1"`
	OtherInfo         string  `json:"other_info"`
	Tag               *string `json:"tag" gorm:"index"`
	Setting           *string `json:"setting" gorm:"type:text"` // 渠道额外设置
	ParamOverride     *string `json:"param_override" gorm:"type:text"`
	HeaderOverride    *string `json:"header_override" gorm:"type:text"`
	Remark            *string `json:"remark" gorm:"type:varchar(255)" validate:"max=255"`
	// add after v0.8.5
	ChannelInfo ChannelInfo `json:"channel_info" gorm:"type:json"`

	OtherSettings string `json:"settings" gorm:"column:settings"` // 其他设置，存储azure版本等不需要检索的信息，详见dto.ChannelOtherSettings

	// cache info
	Keys []string `json:"-" gorm:"-"`
	// KeyEntryIDs holds the stable allocation ID per key position, and
	// KeyEntryRevisions the allocation's configuration revision. The
	// authoritative rows live in channel_key_entries; this is the loaded
	// projection (see channel_key_entry.go). KeyEntryIDStrings is the same list
	// as decimal strings for JSON, because a JavaScript client cannot hold a
	// 64-bit integer exactly.
	KeyEntryIDs       []int64  `json:"-" gorm:"-"`
	KeyEntryRevisions []int64  `json:"-" gorm:"-"`
	KeyEntryIDStrings []string `json:"key_entry_ids,omitempty" gorm:"-"`
	// BalanceDetail is the last balance query in channel responses; see
	// GetBalanceDetail.
	BalanceDetail *ChannelBalanceDetail `json:"balance_detail,omitempty" gorm:"-"`
}

const ChannelStatusReasonAllKeysDisabled = "All keys are disabled"

type ChannelInfo struct {
	IsMultiKey             bool                  `json:"is_multi_key"`                        // 是否多Key模式
	MultiKeySize           int                   `json:"multi_key_size"`                      // 多Key模式下的Key数量
	MultiKeyStatusList     map[int]int           `json:"multi_key_status_list"`               // key状态列表，key index -> status
	MultiKeyDisabledReason map[int]string        `json:"multi_key_disabled_reason,omitempty"` // key禁用原因列表，key index -> reason
	MultiKeyDisabledTime   map[int]int64         `json:"multi_key_disabled_time,omitempty"`   // key禁用时间列表，key index -> time
	MultiKeyPollingIndex   int                   `json:"multi_key_polling_index"`             // 多Key模式下轮询的key索引
	MultiKeyMode           constant.MultiKeyMode `json:"multi_key_mode"`
	// 每个key可用的模型，key index -> models；缺失或空表示该key可用渠道全部模型
	MultiKeyModels        map[int][]string `json:"multi_key_models,omitempty"`
	MultiKeyModelMappings map[int]string   `json:"multi_key_model_mappings,omitempty"`
	MultiKeyScopeModes    map[int]string   `json:"multi_key_scope_modes,omitempty"`
	// 独占某个key的用户，key index -> user id；缺失或0表示公共key
	MultiKeyUsers map[int]int `json:"multi_key_users,omitempty"`
	// ModelUnavailable records upstream refusals that named a model as
	// unavailable to one credential. It never changes Models or key status.
	ModelUnavailable []ChannelModelUnavailability `json:"model_unavailable,omitempty"`
	// KeyEntryRevision changes whenever an allocation of the channel is added,
	// removed, reordered, replaced or rebind, so a client or an internal task
	// holding an older snapshot can detect that its allocation IDs are stale.
	KeyEntryRevision int64 `json:"key_entry_revision,omitempty"`
}

// ChannelModelUnavailability is one observation that a credential of the
// channel cannot serve a model upstream. UpstreamModel is the model the
// channel's mapping sends upstream, KeyFingerprint identifies the credential
// without storing it, and Reason is a fixed code rather than upstream text.
type ChannelModelUnavailability struct {
	UpstreamModel  string `json:"upstream_model"`
	KeyFingerprint string `json:"key_fingerprint"`
	KeyIndex       int    `json:"key_index"`
	StatusCode     int    `json:"status_code"`
	Evidence       string `json:"evidence"`
	ObservedAt     int64  `json:"observed_at"`
}

type ChannelSortOptions struct {
	SortBy    string
	SortOrder string
	IDSort    bool
}

var channelSortColumns = map[string]string{
	"id":            "id",
	"name":          "name",
	"priority":      "priority",
	"balance":       "balance",
	"response_time": "response_time",
	"test_time":     "test_time",
}

func NewChannelSortOptions(sortBy string, sortOrder string, idSort bool) ChannelSortOptions {
	normalizedSortBy := strings.ToLower(strings.TrimSpace(sortBy))
	normalizedSortOrder := strings.ToLower(strings.TrimSpace(sortOrder))
	if _, ok := channelSortColumns[normalizedSortBy]; !ok {
		normalizedSortBy = ""
		normalizedSortOrder = ""
	} else if normalizedSortOrder != "asc" {
		normalizedSortOrder = "desc"
	}

	return ChannelSortOptions{
		SortBy:    normalizedSortBy,
		SortOrder: normalizedSortOrder,
		IDSort:    idSort,
	}
}

func (options ChannelSortOptions) Apply(query *gorm.DB) *gorm.DB {
	if columnName, ok := channelSortColumns[options.SortBy]; ok {
		column := clause.Column{Name: columnName}
		if columnName == "priority" {
			// Routing treats a missing priority as zero on every database.
			column = clause.Column{Name: "COALESCE(priority, 0)", Raw: true}
		}
		return query.Order(clause.OrderByColumn{
			Column: column,
			Desc:   options.SortOrder != "asc",
		}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true})
	}
	if options.IDSort {
		return query.Order(clause.OrderByColumn{
			Column: clause.Column{Name: "id"},
			Desc:   true,
		})
	}
	return query.Order(clause.OrderByColumn{
		Column: clause.Column{Name: "COALESCE(priority, 0)", Raw: true},
		Desc:   true,
	}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: true})
}

func resolveChannelSortOptions(idSort bool, sortOptions []ChannelSortOptions) ChannelSortOptions {
	if len(sortOptions) == 0 {
		return NewChannelSortOptions("", "", idSort)
	}
	options := sortOptions[0]
	options.IDSort = options.IDSort || idSort
	return options
}

func NormalizeChannelGroupFilter(group string) string {
	group = strings.TrimSpace(group)
	if group == "" || strings.EqualFold(group, "all") || strings.EqualFold(group, "null") {
		return ""
	}
	return group
}

func channelGroupFilterCondition() string {
	if common.UsingMainDatabase(common.DatabaseTypeMySQL) {
		return `CONCAT(',', ` + commonGroupCol + `, ',') LIKE ? ESCAPE '!'`
	}
	return `(',' || ` + commonGroupCol + ` || ',') LIKE ? ESCAPE '!'`
}

func channelGroupFilterPattern(group string) string {
	group = strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(group)
	return "%," + group + ",%"
}

func ApplyChannelGroupFilter(query *gorm.DB, group string) *gorm.DB {
	group = NormalizeChannelGroupFilter(group)
	if group == "" {
		return query
	}
	return query.Where(channelGroupFilterCondition(), channelGroupFilterPattern(group))
}

// Value implements driver.Valuer interface
// 必须返回 string 而非 []byte:PG simple protocol 下 []byte 参数按 bytea
// 编码,写 json 列会触发 SQLSTATE 22P02。
func (c ChannelInfo) Value() (driver.Value, error) {
	b, err := common.Marshal(&c)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan implements sql.Scanner interface
func (c *ChannelInfo) Scan(value any) error {
	return common.Unmarshal(jsonScanBytes(value), c)
}

func (channel *Channel) GetKeys() []string {
	if channel.Key == "" {
		return []string{}
	}
	if len(channel.Keys) > 0 {
		return channel.Keys
	}
	trimmed := strings.TrimSpace(channel.Key)
	// If the key starts with '[', try to parse it as a JSON array (e.g., for Vertex AI scenarios)
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := common.Unmarshal([]byte(trimmed), &arr); err == nil {
			res := make([]string, len(arr))
			for i, v := range arr {
				res[i] = string(v)
			}
			return res
		}
	}
	// Otherwise, fall back to splitting by newline
	keys := strings.Split(strings.Trim(channel.Key, "\n"), "\n")
	return keys
}

func (channel *Channel) GetNextEnabledKey() (string, int, *types.NewAPIError) {
	return channel.GetNextEnabledKeyFor("", 0)
}

// GetNextEnabledKeyFor selects the key that serves one request.
//
// modelName is the requested model; an empty name means no model filter.
// userId > 0 is a forwarded request: keys bound to that user win, and when they
// cannot serve the request only public keys are eligible. userId <= 0 is an
// administrative caller (channel test), where every key stays eligible.
func (channel *Channel) GetNextEnabledKeyFor(modelName string, userId int, excludedEntryIDs ...int64) (string, int, *types.NewAPIError) {
	var budgetAllowed map[int64]bool
	if ChannelBudgetsEnabled() && userId > 0 {
		var err error
		budgetAllowed, err = ChannelBudgetAllows(context.Background(), channel.Id, channel.KeyEntryIDs)
		if err != nil {
			return "", 0, types.NewError(errors.New("channel budget is unavailable, retry the request"), types.ErrorCodeUpdateDataError)
		}
	}
	entryEligible := func(index int) bool {
		id := channel.ChannelKeyEntryID(index)
		if slices.Contains(excludedEntryIDs, id) {
			return false
		}
		return budgetAllowed == nil || id > 0 && budgetAllowed[id]
	}
	// If not in multi-key mode, return the original key string directly. A
	// single credential recorded as unable to serve the model is refused here
	// too, so a pin or queued candidate recheck that skipped selection cannot
	// forward a known-unavailable combination.
	if !channel.ChannelInfo.IsMultiKey {
		if !entryEligible(0) {
			return "", 0, types.NewError(errors.New("no enabled key serves this request"), types.ErrorCodeChannelKeyScopeUnsatisfied)
		}
		if len(channel.ChannelInfo.ModelUnavailable) > 0 {
			if upstreamModel, ok := channel.RoutingUpstreamModelNameForKey(0, modelName); ok && channel.keyModelUnavailable(0, upstreamModel) {
				return "", 0, types.NewError(errors.New("no enabled key serves this request"), types.ErrorCodeChannelKeyScopeUnsatisfied, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable))
			}
		}
		return channel.Key, 0, nil
	}

	// Obtain all keys (split by \n)
	keys := channel.GetKeys()
	if len(keys) == 0 {
		// No keys available, return error, should disable the channel
		return "", 0, types.NewError(errors.New("no keys available"), types.ErrorCodeChannelNoAvailableKey)
	}

	lock := GetChannelPollingLock(channel.Id)
	lock.Lock()
	defer lock.Unlock()

	statusList := channel.ChannelInfo.MultiKeyStatusList
	// helper to get key status, default to enabled when missing
	getStatus := func(idx int) int {
		if statusList == nil {
			return common.ChannelStatusEnabled
		}
		if status, ok := statusList[idx]; ok {
			return status
		}
		return common.ChannelStatusEnabled
	}

	// Collect the enabled keys that may serve this request. Without any key
	// scope or user binding this is exactly the previous set of enabled keys.
	// A key recorded as unable to serve the model upstream is skipped, so a
	// retry uses another key of the same channel instead of repeating the
	// refusal.
	enabledIdx := make([]int, 0, len(keys))
	candidates := make([]int, 0, len(keys))
	boundIdx := make([]int, 0, len(keys))
	for i := range keys {
		if getStatus(i) != common.ChannelStatusEnabled {
			continue
		}
		enabledIdx = append(enabledIdx, i)
		if !entryEligible(i) || !channel.KeyServesModel(i, modelName) {
			continue
		}
		if len(channel.ChannelInfo.ModelUnavailable) > 0 {
			upstreamModel, _ := channel.RoutingUpstreamModelNameForKey(i, modelName)
			if channel.keyModelUnavailable(i, upstreamModel) {
				continue
			}
		}
		owner := channel.ChannelInfo.MultiKeyUsers[i]
		if userId > 0 && owner != 0 && owner != userId {
			// A key bound to another user is never used for this request.
			continue
		}
		if userId > 0 && owner == userId {
			boundIdx = append(boundIdx, i)
		}
		candidates = append(candidates, i)
	}
	// If no specific status list or none enabled, return an explicit error so caller can
	// properly handle a channel with no available keys (e.g. mark channel disabled).
	// Returning the first key here caused requests to keep using an already-disabled key.
	if len(enabledIdx) == 0 {
		return "", 0, types.NewError(errors.New("no enabled keys"), types.ErrorCodeChannelNoAvailableKey)
	}
	// Enabled keys exist but none may serve this request. The channel itself is
	// healthy, so this error is retryable elsewhere and must not disable it.
	if len(candidates) == 0 {
		return "", 0, types.NewError(errors.New("no enabled key serves this request"), types.ErrorCodeChannelKeyScopeUnsatisfied, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable))
	}
	if len(boundIdx) > 0 {
		// A key bound to the requesting user is exclusive to that user, so it is
		// preferred over the public keys of the same channel.
		candidates = boundIdx
	}

	switch channel.ChannelInfo.MultiKeyMode {
	case constant.MultiKeyModeRandom:
		// Randomly pick one eligible key
		selectedIdx := candidates[rand.Intn(len(candidates))]
		return keys[selectedIdx], selectedIdx, nil
	case constant.MultiKeyModePolling:
		// Use channel-specific lock to ensure thread-safe polling

		channelInfo, err := CacheGetChannelInfo(channel.Id)
		if err != nil {
			return "", 0, types.NewError(err, types.ErrorCodeGetChannelFailed, types.ErrOptionWithSkipRetry())
		}
		defer func() {
			if common.DebugEnabled {
				logger.LogDebug(nil, "channel %d polling index: %d", channel.Id, channel.ChannelInfo.MultiKeyPollingIndex)
			}
			if !common.MemoryCacheEnabled {
				_ = channel.SaveChannelInfo()
			} else {
				// CacheUpdateChannel(channel)
			}
		}()
		// Start from the saved polling index and look for the next enabled key
		eligible := make(map[int]struct{}, len(candidates))
		for _, idx := range candidates {
			eligible[idx] = struct{}{}
		}
		start := channelInfo.MultiKeyPollingIndex
		if start < 0 || start >= len(keys) {
			start = 0
		}
		for i := range keys {
			idx := (start + i) % len(keys)
			if _, ok := eligible[idx]; ok {
				// update polling index for next call (point to the next position)
				channel.ChannelInfo.MultiKeyPollingIndex = (idx + 1) % len(keys)
				return keys[idx], idx, nil
			}
		}
		// Fallback – should not happen, but return the first eligible key
		return keys[candidates[0]], candidates[0], nil
	default:
		// Unknown mode, default to the first eligible key
		return keys[candidates[0]], candidates[0], nil
	}
}

// KeyServesModel reports whether the key at idx may serve modelName. A key
// without a configured scope serves every model of the channel. Explicit
// scopes may name the request alias or the channel's exact upstream target.
func (channel *Channel) KeyServesModel(idx int, modelName string) bool {
	if modelName == "" {
		return true
	}
	scope := channel.ChannelInfo.MultiKeyModels[idx]
	if mode := channel.ChannelInfo.MultiKeyScopeModes[idx]; mode != "" {
		if mode == "allowlist" {
			return channel.keyScopeServesModel(idx, scope, modelName)
		}
		return mode == "all"
	}
	return len(scope) == 0 || channel.keyScopeServesModel(idx, scope, modelName)
}

// keyScopeServesModel matches an allocation's upstream capability without
// changing the request namespace used by channel, token or group authorization.
// Direct aliases remain supported; only the final mapped target is a fallback.
func (channel *Channel) keyScopeServesModel(index int, scope []string, modelName string) bool {
	if len(scope) == 0 {
		return false
	}
	if slices.Contains(scope, modelName) {
		return true
	}
	upstreamModel, ok := channel.RoutingUpstreamModelNameForKey(index, modelName)
	return ok && upstreamModel != modelName && slices.Contains(scope, upstreamModel)
}

// Evidence values of a stored model-unavailability observation. They are fixed
// codes, never upstream text.
const (
	ChannelModelUnavailableEvidenceCode    = "error_code"
	ChannelModelUnavailableEvidenceMessage = "error_message"

	// A channel keeps the newest observations; older ones are dropped so the
	// channel_info column stays bounded.
	channelModelUnavailableLimit          = 64
	channelModelUnavailableMaxModelLength = 128
)

// ChannelKeyFingerprint identifies a credential without storing it: a one-way
// hash prefix that survives key reordering and is never the key itself.
func ChannelKeyFingerprint(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// RoutingUpstreamModelName resolves the model a request for modelName is sent
// upstream as, following the channel's model mapping the same way the relay
// does (relay/helper.ModelMappedHelper): chained hops, the base name of a
// reasoning suffix at any hop, and a stop at a self-map. ok is false for an
// unreadable mapping or a cycle, where no observation applies. The hop walk
// reuses followChannelModelMapping, so the model package keeps one resolver.
func (channel *Channel) RoutingUpstreamModelName(modelName string) (string, bool) {
	if modelName == "" {
		return "", false
	}
	raw := strings.TrimSpace(channel.GetModelMapping())
	if raw == "" || raw == "{}" {
		return modelName, true
	}
	mapping := make(map[string]string)
	if err := common.UnmarshalJsonStr(raw, &mapping); err != nil {
		return "", false
	}
	// The relay also resolves a reasoning-suffixed name through its base name
	// at any hop; pre-filling those entries keeps both resolvers in step.
	candidates := make([]string, 0, len(mapping)+1)
	candidates = append(candidates, modelName)
	for _, target := range mapping {
		candidates = append(candidates, target)
	}
	for _, candidate := range candidates {
		if mapping[candidate] == "" {
			mapping[candidate] = mapping[hostreasoning.BaseModelName(candidate)]
		}
	}
	upstream, cycle := followChannelModelMapping(mapping, modelName)
	if cycle || upstream == "" {
		return "", false
	}
	return upstream, true
}

// GetModelMappingForKey overlays only the selected allocation's source rules.
// No override inherits the channel mapping byte-for-byte. Never mutate either
// stored map while building an attempt snapshot.
func (channel *Channel) GetModelMappingForKey(index int) (string, error) {
	override := strings.TrimSpace(channel.ChannelInfo.MultiKeyModelMappings[index])
	if override == "" || override == "{}" {
		return channel.GetModelMapping(), nil
	}
	mapping := map[string]string{}
	if inherited := strings.TrimSpace(channel.GetModelMapping()); inherited != "" {
		if common.UnmarshalJsonStr(inherited, &mapping) != nil || mapping == nil {
			return "", errors.New("Invalid key model mapping")
		}
	}
	var keyMapping map[string]string
	if common.UnmarshalJsonStr(override, &keyMapping) != nil || keyMapping == nil {
		return "", errors.New("Invalid key model mapping")
	}
	maps.Copy(mapping, keyMapping)
	encoded, err := common.Marshal(mapping)
	return string(encoded), err
}

// RoutingUpstreamModelNameForKey shares the relay-compatible chain resolver,
// but applies the selected Key's exact-source overrides first.
func (channel *Channel) RoutingUpstreamModelNameForKey(index int, modelName string) (string, bool) {
	mapping, err := channel.GetModelMappingForKey(index)
	if err != nil {
		return "", false
	}
	candidate := *channel
	candidate.ModelMapping = &mapping
	return candidate.RoutingUpstreamModelName(modelName)
}

// keyEnabled reports whether the key at idx is enabled. A single-key channel
// carries the channel status instead of a per-key status.
func (channel *Channel) keyEnabled(idx int) bool {
	if !channel.ChannelInfo.IsMultiKey {
		return idx == 0
	}
	status, ok := channel.ChannelInfo.MultiKeyStatusList[idx]
	return !ok || status == common.ChannelStatusEnabled
}

// keyModelUnavailable reports whether the credential at idx is recorded as
// unable to serve upstreamModel. The fingerprint, not the stored index,
// identifies the credential, so reordering keys never inherits an observation.
func (channel *Channel) keyModelUnavailable(idx int, upstreamModel string) bool {
	if upstreamModel == "" {
		return false
	}
	keys := channel.GetKeys()
	if idx < 0 || idx >= len(keys) {
		return false
	}
	fingerprint := ChannelKeyFingerprint(keys[idx])
	if fingerprint == "" {
		return false
	}
	return slices.ContainsFunc(channel.ChannelInfo.ModelUnavailable, func(entry ChannelModelUnavailability) bool {
		return entry.UpstreamModel == upstreamModel && entry.KeyFingerprint == fingerprint
	})
}

// ModelUnavailableForServingKeys reports whether every enabled key that may
// serve modelName is recorded as unable to serve the model upstream. Other
// models, other keys and the configured model list stay untouched.
// An omitted user ID includes every credential (administrative inspection).
// Zero selects public credentials; a positive ID also permits that user's keys.
func (channel *Channel) ModelUnavailableForServingKeys(modelName string, userIDs ...int) bool {
	if len(channel.ChannelInfo.ModelUnavailable) == 0 || modelName == "" {
		return false
	}
	keys := channel.GetKeys()
	serving := 0
	for i := range keys {
		if !channel.keyEnabled(i) || !channel.KeyServesModel(i, modelName) {
			continue
		}
		if len(userIDs) > 0 && channel.ChannelInfo.IsMultiKey {
			owner := channel.ChannelInfo.MultiKeyUsers[i]
			if owner != 0 && owner != userIDs[0] {
				continue
			}
		}
		serving++
		upstreamModel, ok := channel.RoutingUpstreamModelNameForKey(i, modelName)
		if !ok || !channel.keyModelUnavailable(i, upstreamModel) {
			return false
		}
	}
	return serving > 0
}

// channelServesConfiguredModel reports whether the channel still lists the
// requested model, so an observation of a removed model is not recorded.
func channelServesConfiguredModel(channel *Channel, modelName string) bool {
	models := channel.GetModels()
	if slices.Contains(models, modelName) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	return normalized != "" && normalized != modelName && slices.Contains(models, normalized)
}

// RecordChannelModelUnavailability persists that one credential of the channel
// cannot serve the model a request for originModel reaches upstream. It records
// nothing when the stored configuration no longer matches the attempt: a
// replaced or disabled key, a changed mapping, a disabled channel, a model the
// channel no longer serves, or a mapping that now resolves a different model
// than the attempt sent. attemptMapping is the raw model mapping the failed
// attempt used and attemptedUpstreamModel the model that attempt sent. A supplied
// allocation identifies the selected stable row and its configuration revision;
// credential-only legacy callers must match exactly one current allocation.
// Reports whether it wrote.
func RecordChannelModelUnavailability(channelID int, originModel, key, attemptMapping, attemptedUpstreamModel string, statusCode int, evidence string, selected ...ChannelAllocation) (bool, error) {
	fingerprint := ChannelKeyFingerprint(key)
	if channelID <= 0 || originModel == "" || fingerprint == "" || len(selected) > 1 {
		return false, nil
	}
	var allocation ChannelAllocation
	if len(selected) == 1 {
		allocation = selected[0]
		if allocation.ChannelID != channelID || allocation.EntryID <= 0 || allocation.Revision <= 0 {
			return false, nil
		}
	}
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()
	written := false
	_, err := UpdateChannelLocked(channelID, func(channel *Channel) error {
		written = false
		if channel.Status != common.ChannelStatusEnabled {
			return nil
		}
		if !channelServesConfiguredModel(channel, originModel) {
			return nil
		}
		keys := channel.GetKeys()
		index := -1
		if len(selected) == 1 {
			index = slices.Index(channel.KeyEntryIDs, allocation.EntryID)
			if index < 0 || index >= len(keys) || index >= len(channel.KeyEntryRevisions) ||
				channel.KeyEntryRevisions[index] != allocation.Revision || ChannelKeyFingerprint(keys[index]) != fingerprint {
				return nil
			}
		} else {
			for i, candidate := range keys {
				if ChannelKeyFingerprint(candidate) != fingerprint {
					continue
				}
				if index >= 0 {
					return nil // identical credential text cannot identify an allocation
				}
				index = i
			}
		}
		if index < 0 || !channel.keyEnabled(index) || !channel.KeyServesModel(index, originModel) {
			return nil
		}
		mapping, err := channel.GetModelMappingForKey(index)
		if err != nil || mapping != attemptMapping {
			return nil
		}
		upstreamModel, ok := channel.RoutingUpstreamModelNameForKey(index, originModel)
		if !ok || upstreamModel == "" || len(upstreamModel) > channelModelUnavailableMaxModelLength ||
			attemptedUpstreamModel != "" && upstreamModel != attemptedUpstreamModel {
			return nil
		}
		channel.ChannelInfo.ModelUnavailable = appendChannelModelUnavailability(channel.ChannelInfo.ModelUnavailable, ChannelModelUnavailability{
			UpstreamModel:  upstreamModel,
			KeyFingerprint: fingerprint,
			KeyIndex:       index,
			StatusCode:     statusCode,
			Evidence:       evidence,
			ObservedAt:     common.GetTimestamp(),
		})
		written = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if written {
		return true, CacheRefreshChannelModelUnavailability(channelID)
	}
	return false, nil
}

// appendChannelModelUnavailability refreshes the observation of the same
// credential and model and keeps the stored list bounded.
func appendChannelModelUnavailability(entries []ChannelModelUnavailability, entry ChannelModelUnavailability) []ChannelModelUnavailability {
	entries = slices.DeleteFunc(slices.Clone(entries), func(existing ChannelModelUnavailability) bool {
		return existing.UpstreamModel == entry.UpstreamModel && existing.KeyFingerprint == entry.KeyFingerprint
	})
	entries = append(entries, entry)
	if len(entries) > channelModelUnavailableLimit {
		entries = entries[len(entries)-channelModelUnavailableLimit:]
	}
	return entries
}

// RestoreChannelModelUnavailability clears stored observations so the next
// request probes the combination again. An empty upstreamModel clears every
// model and an empty keyFingerprint clears every credential of the selected
// models. The fingerprint identifies the credential, so a reordered key list
// never restores the wrong entry.
func RestoreChannelModelUnavailability(channelID int, upstreamModel, keyFingerprint string) (bool, error) {
	if channelID <= 0 {
		return false, nil
	}
	pollingLock := GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()
	written := false
	_, err := UpdateChannelLocked(channelID, func(channel *Channel) error {
		written = false
		entries := channel.ChannelInfo.ModelUnavailable
		if len(entries) == 0 {
			return nil
		}
		kept := slices.DeleteFunc(slices.Clone(entries), func(entry ChannelModelUnavailability) bool {
			if upstreamModel != "" && entry.UpstreamModel != upstreamModel {
				return false
			}
			return keyFingerprint == "" || entry.KeyFingerprint == keyFingerprint
		})
		if len(kept) == len(entries) {
			return nil
		}
		if len(kept) == 0 {
			kept = nil
		}
		channel.ChannelInfo.ModelUnavailable = kept
		written = true
		return nil
	})
	if err != nil {
		return false, err
	}
	if written {
		return true, CacheRefreshChannelModelUnavailability(channelID)
	}
	return false, nil
}

// modelsServedByKeys returns the subset of models that at least one of the
// given keys may serve. A model with no serving key is dropped, so an all-bound
// channel contributes nothing to the group it no longer serves.
func (channel *Channel) modelsServedByKeys(keyIndexes []int, models []string) []string {
	if len(keyIndexes) == 0 {
		return nil
	}
	served := make([]string, 0, len(models))
	for _, model := range models {
		for _, idx := range keyIndexes {
			if channel.KeyServesModel(idx, model) {
				served = append(served, model)
				break
			}
		}
	}
	return served
}

// PublicModels returns the models the channel's unbound keys serve, always
// intersected with the channel's own model list. Key enable status is ignored
// because abilities do not change while a key is temporarily disabled.
func (channel *Channel) PublicModels() []string {
	models := channel.GetModels()
	if !channel.ChannelInfo.IsMultiKey {
		return models
	}
	keys := channel.GetKeys()
	public := make([]int, 0, len(keys))
	for i := range keys {
		if channel.ChannelInfo.MultiKeyUsers[i] == 0 {
			public = append(public, i)
		}
	}
	return channel.modelsServedByKeys(public, models)
}

// UserBoundModels returns the models served by the keys bound to userId that are
// enabled, intersected with the channel's own model list. It returns nil when
// the user has no bound key on this channel.
func (channel *Channel) UserBoundModels(userId int) []string {
	if !channel.ChannelInfo.IsMultiKey || userId <= 0 {
		return nil
	}
	keys := channel.GetKeys()
	var allowed map[int64]bool
	if ChannelBudgetsEnabled() {
		var err error
		allowed, err = ChannelBudgetAllows(context.Background(), channel.Id, channel.KeyEntryIDs)
		if err != nil {
			return nil
		}
	}
	statusList := channel.ChannelInfo.MultiKeyStatusList
	bound := make([]int, 0, len(keys))
	for i := range keys {
		if allowed != nil && !allowed[channel.ChannelKeyEntryID(i)] {
			continue
		}
		if channel.ChannelInfo.MultiKeyUsers[i] != userId {
			continue
		}
		if status, ok := statusList[i]; ok && status != common.ChannelStatusEnabled {
			continue
		}
		bound = append(bound, i)
	}
	var models []string
	for _, modelName := range channel.GetModels() {
		for _, index := range bound {
			upstreamModel, _ := channel.RoutingUpstreamModelNameForKey(index, modelName)
			if channel.KeyServesModel(index, modelName) && !channel.keyModelUnavailable(index, upstreamModel) {
				models = append(models, modelName)
				break
			}
		}
	}
	return models
}

func (channel *Channel) SaveChannelInfo() error {
	// The polling caller already holds the per-channel polling lock. Merge only
	// its cursor; the request snapshot may predate a model observation or key edit.
	_, err := UpdateChannelLocked(channel.Id, func(stored *Channel) error {
		if stored.Key == channel.Key {
			stored.ChannelInfo.MultiKeyPollingIndex = channel.ChannelInfo.MultiKeyPollingIndex
		}
		return nil
	})
	return err
}

func (channel *Channel) GetModels() []string {
	if channel.Models == "" {
		return []string{}
	}
	return strings.Split(strings.Trim(channel.Models, ","), ",")
}

func (channel *Channel) GetGroups() []string {
	if channel.Group == "" {
		return []string{}
	}
	groups := strings.Split(strings.Trim(channel.Group, ","), ",")
	for i, group := range groups {
		groups[i] = strings.TrimSpace(group)
	}
	return groups
}

func (channel *Channel) GetOtherInfo() map[string]any {
	otherInfo := make(map[string]any)
	if channel.OtherInfo != "" {
		err := common.Unmarshal([]byte(channel.OtherInfo), &otherInfo)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		}
	}
	return otherInfo
}

func (channel *Channel) SetOtherInfo(otherInfo map[string]any) {
	otherInfoBytes, err := json.Marshal(otherInfo)
	if err != nil {
		common.SysLog(common.LogText("failed to marshal other info: channel_id=%d, tag=%s, name=%s, error=%v", channel.Id, channel.GetTag(), channel.Name, err))
		return
	}
	channel.OtherInfo = string(otherInfoBytes)
}

func (channel *Channel) GetTag() string {
	if channel.Tag == nil {
		return ""
	}
	return *channel.Tag
}

func (channel *Channel) SetTag(tag string) {
	channel.Tag = &tag
}

func (channel *Channel) GetAutoBan() bool {
	if channel.AutoBan == nil {
		return false
	}
	return *channel.AutoBan == 1
}

// ErrChannelOtherInfoBusy reports a channel whose other_info other writers
// kept changing through every attempt of UpdateChannelOtherInfo.
var ErrChannelOtherInfoBusy = errors.New("channel other_info kept changing during the update")

const channelOtherInfoAttempts = 5

// UpdateChannelOtherInfo runs update on the stored channel and writes the
// other_info it leaves, with the columns update returns, only while the
// stored other_info is still the one read. When another writer, on any node,
// changed it in between, the channel is read again and update runs on that,
// so neither change is lost: update may run more than once and must not wait
// on the network. It returns nil to write nothing, and written reports a
// write. Only the returned columns and other_info are written, so a stale
// read never puts back the key, counters or settings. On SQLite, which serves
// one node, the channel's key lock also keeps the locked updates of this
// process out.
func UpdateChannelOtherInfo(channelID int, update func(channel *Channel) (map[string]any, error)) (written bool, err error) {
	var allocationMarker Channel
	if err := DB.Select("id", "channel_info").First(&allocationMarker, channelID).Error; err != nil {
		return false, err
	}
	if allocationMarker.ChannelInfo.KeyEntryRevision > 0 {
		return UpdateChannelOtherInfoLocked(context.Background(), channelID, update)
	}
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		lock := sqliteChannelKeyLock(channelID)
		lock.Lock()
		defer lock.Unlock()
	}
	for range channelOtherInfoAttempts {
		var channel Channel
		if err := DB.First(&channel, "id = ?", channelID).Error; err != nil {
			return false, err
		}
		read := channel.OtherInfo
		updates, err := update(&channel)
		if err != nil || updates == nil {
			return false, err
		}
		updates["other_info"] = channel.OtherInfo
		query := DB.Model(&Channel{}).Where("id = ?", channelID)
		if read == "" {
			query = query.Where("(other_info IS NULL OR other_info = ?)", "")
		} else {
			query = query.Where("other_info = ?", read)
		}
		result := query.Updates(updates)
		if result.Error != nil {
			return false, result.Error
		}
		if result.RowsAffected > 0 {
			return true, nil
		}
		// MySQL counts changed rows only: when other_info is still the one
		// read, the row matched and already held every value written.
		if common.UsingMainDatabase(common.DatabaseTypeMySQL) && channel.OtherInfo == read {
			var stored Channel
			if err := DB.Select("other_info").First(&stored, "id = ?", channelID).Error; err != nil {
				return false, err
			}
			if stored.OtherInfo == read {
				return true, nil
			}
		}
	}
	return false, ErrChannelOtherInfoBusy
}

// UpdateChannelOtherInfoLocked validates and applies a completed management
// result against one current, locked row. The callback must not perform network
// I/O. Unlike the other_info CAS, the row lock also protects key and policy
// comparisons from concurrent configuration edits.
func UpdateChannelOtherInfoLocked(ctx context.Context, channelID int, update func(channel *Channel) (map[string]any, error)) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		lock := sqliteChannelKeyLock(channelID)
		lock.Lock()
		defer lock.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	written := false
	db := DB.WithContext(ctx).Session(&gorm.Session{Logger: gormlogger.Discard})
	err := db.Transaction(func(tx *gorm.DB) error {
		if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
			// A custom SQLite DSN may use deferred transactions. Take its writer
			// lock before reading, as credential rotation does.
			if err := tx.Model(&Channel{}).Where("1 = 0").UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
				return err
			}
		}
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, "id = ?", channelID).Error; err != nil {
			return err
		}
		beforeInfo, err := common.Marshal(channel.ChannelInfo)
		if err != nil {
			return err
		}
		updates, err := update(&channel)
		if err != nil || updates == nil {
			return err
		}
		if channel.ChannelInfo.KeyEntryRevision > 0 && channelInfoChanged(beforeInfo, channel.ChannelInfo) {
			entries, err := loadActiveChannelKeyEntries(tx, channelID)
			if err != nil {
				return err
			}
			syncKeyEntryAttributesFromProjection(entries, &channel)
			if err := persistKeyEntries(tx, &channel, entries, nil); err != nil {
				return err
			}
			updates["channel_info"] = channel.ChannelInfo
		}
		updates["other_info"] = channel.OtherInfo
		if err := tx.Model(&Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
			return err
		}
		written = true
		return nil
	})
	return written && err == nil, err
}

func GetAllChannels(startIdx int, num int, selectAll bool, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	var err error
	order := resolveChannelSortOptions(idSort, sortOptions)
	ctx := WithBatchedChannelKeyEntries(context.Background())
	if selectAll {
		err = order.Apply(DB.WithContext(ctx)).Find(&channels).Error
	} else {
		err = order.Apply(DB.WithContext(ctx)).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	}
	if err != nil {
		return nil, err
	}
	if err := ProjectChannelKeyEntries(DB, ctx, channels, !selectAll); err != nil {
		return nil, err
	}
	return channels, nil
}

// ChannelSearchQueryFiltered builds the base filtered query behind
// /api/channel/search supports: keyword (id/name/key/base_url plus model), group,
// status (enabled / disabled / all) and type. The caller adds pagination,
// ordering or aggregate projection, so the total and type counts are SQL
// aggregates over the same filter instead of the in-memory result set.
func ChannelSearchQueryFiltered(ctx context.Context, keyword, group, modelKeyword string, statusFilter int, typeFilter int) *gorm.DB {
	if ctx == nil {
		ctx = context.Background()
	}
	modelsCol := "`models`"
	baseURLCol := "`base_url`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
		baseURLCol = `"base_url"`
	}
	query := DB.WithContext(ctx).Model(&Channel{})
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + modelKeyword + "%"}
	query = ApplyChannelGroupFilter(query.Where(whereClause, args...), group)
	if statusFilter == common.ChannelStatusEnabled {
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	} else if statusFilter == 0 {
		query = query.Where("status != ?", common.ChannelStatusEnabled)
	}
	if typeFilter >= 0 {
		query = query.Where("type = ?", typeFilter)
	}
	return query
}

// ListChannelsPage reads one page of channels through SQL with the same filters
// and ordering the tag-mode listing uses, projecting allocations in bounded
// chunks. The returned total is the SQL count of the filtered set, so callers no
// longer load the whole table to paginate or to size the result.
func ListChannelsPage(ctx context.Context, query *gorm.DB, startIdx, pageSize int, omitKey bool, sortOptions ChannelSortOptions) ([]*Channel, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var total int64
	if err := query.WithContext(ctx).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	channels := make([]*Channel, 0)
	// Marking the context makes AfterFind defer to the batch projection below,
	// so the page read does not run one allocation query per row.
	pageQuery := sortOptions.Apply(query.WithContext(WithBatchedChannelKeyEntries(ctx))).Limit(pageSize).Offset(startIdx)
	if omitKey {
		pageQuery = pageQuery.Omit("key")
	}
	if err := pageQuery.Find(&channels).Error; err != nil {
		return nil, 0, err
	}
	// Project through the caller's connection (same session/transaction), never a
	// global handle: NewDB in ProjectChannelKeyEntries strips the query's model,
	// where and limit clauses, leaving a plain allocation read.
	if err := ProjectChannelKeyEntries(query, ctx, channels, omitKey); err != nil {
		return nil, 0, err
	}
	return channels, total, nil
}

// CountChannelTypesByQuery reads the per-type channel counts of a filtered
// query in one grouped aggregate instead of deriving them from every matching
// row. It is distinct from CountChannelsByType(channelType int), which counts a
// single type.
func CountChannelTypesByQuery(query *gorm.DB) (map[int64]int64, error) {
	type row struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var rows []row
	if err := query.Select("type, count(*) as count").Group("type").Find(&rows).Error; err != nil {
		return nil, err
	}
	counts := make(map[int64]int64, len(rows))
	for i := range rows {
		counts[rows[i].Type] = rows[i].Count
	}
	return counts, nil
}

// ChannelTagSearchSubquery expresses, as a SQL subquery, the set of tags whose
// channels match the tag-search filters. Callers use it as `tag IN (subquery)` so
// they can filter, count and page in SQL instead of expanding every matching tag
// into channel rows in memory. The matching conditions are identical to
// searchTags, so the same tags are selected.
func ChannelTagSearchSubquery(ctx context.Context, keyword, group, model string) *gorm.DB {
	if ctx == nil {
		ctx = context.Background()
	}
	modelsCol := "`models`"
	baseURLCol := "`base_url`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
		baseURLCol = `"base_url"`
	}
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	query := DB.WithContext(ctx).Model(&Channel{}).Select("tag").Where("tag != ''").Where(whereClause, args...)
	return ApplyChannelGroupFilter(query, group)
}

// ChannelTagSearchQuery builds the channel listing query for tag mode: the same
// status/type filters as a normal search, narrowed to the tags the tag-search
// subquery matches. The caller adds the ordering and the page window.
func ChannelTagSearchQuery(ctx context.Context, keyword, group, model string, statusFilter int, typeFilter int) *gorm.DB {
	tagSubquery := ChannelTagSearchSubquery(ctx, keyword, group, model)
	// A bare `tag IN (subquery)` keeps the SQL the same on SQLite, MySQL and
	// PostgreSQL; wrapping the subquery in a derived table would need a dialect
	// alias rule for no benefit.
	query := DB.WithContext(ctx).Model(&Channel{}).Where("tag IN (?)", tagSubquery)
	// The original tag mode filtered the per-tag channel fetch by group as well as
	// the tag search, so the outer query carries the same group filter.
	query = ApplyChannelGroupFilter(query, group)
	if statusFilter == common.ChannelStatusEnabled {
		query = query.Where("status = ?", common.ChannelStatusEnabled)
	} else if statusFilter == 0 {
		query = query.Where("status != ?", common.ChannelStatusEnabled)
	}
	if typeFilter >= 0 {
		query = query.Where("type = ?", typeFilter)
	}
	return query
}

func GetChannelsByTag(tag string, idSort bool, selectAll bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	order := resolveChannelSortOptions(idSort, sortOptions)
	ctx := WithBatchedChannelKeyEntries(context.Background())
	query := order.Apply(DB.WithContext(ctx).Where("tag = ?", tag))
	if !selectAll {
		query = query.Omit("key")
	}
	if err := query.Find(&channels).Error; err != nil {
		return nil, err
	}
	if err := ProjectChannelKeyEntries(DB, ctx, channels, !selectAll); err != nil {
		return nil, err
	}
	return channels, nil
}

func SearchChannels(keyword string, group string, model string, idSort bool, sortOptions ...ChannelSortOptions) ([]*Channel, error) {
	var channels []*Channel
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := resolveChannelSortOptions(idSort, sortOptions)

	// 构造基础查询
	baseQuery := DB.Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	// 执行查询
	err := order.Apply(baseQuery).Find(&channels).Error
	if err != nil {
		return nil, err
	}
	return channels, nil
}

// GetChannelById loads a channel directly from the database, bypassing the
// in-memory channel cache.
//
// WARNING: do NOT call this on request hot paths (middleware, distribution,
// relay submit/retry, polling). Every call is a synchronous DB query and will
// not see cache-only state. Use CacheGetChannel instead: it serves from the
// in-memory cache and falls back to this function automatically when
// MemoryCacheEnabled is false. Direct use is appropriate only where fresh DB
// state is required, e.g. admin CRUD, channel testing, or cache (re)building.
func GetChannelById(id int, selectAll bool) (*Channel, error) {
	channel := &Channel{Id: id}
	var err error = nil
	if selectAll {
		err = DB.First(channel, "id = ?", id).Error
	} else {
		err = DB.Omit("key").First(channel, "id = ?", id).Error
	}
	if err != nil {
		return nil, err
	}
	return channel, nil
}

func BatchInsertChannels(channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	// Keep each chunk backed by channels so generated IDs reach the caller.
	for chunk := range slices.Chunk(channels, 50) {
		if err := tx.Create(&chunk).Error; err != nil {
			tx.Rollback()
			return err
		}
		for i := range chunk {
			channel_ := &chunk[i]
			channel_.ChannelInfo.KeyEntryRevision = 0
			if err := CreateChannelKeyEntries(tx, channel_); err != nil {
				tx.Rollback()
				return err
			}
			if err := tx.Model(&Channel{}).Where("id = ?", channel_.Id).UpdateColumn("channel_info", channel_.ChannelInfo).Error; err != nil {
				tx.Rollback()
				return err
			}
			if err := channel_.AddAbilities(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	BumpSyncTopic(SyncTopicChannels)
	return nil
}

func BatchDeleteChannels(ids []int) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// 使用事务 分批删除channel表和abilities表
	tx := DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}
	var deletedCount int64
	for _, chunk := range lo.Chunk(ids, 200) {
		for _, id := range chunk {
			if err := RetireChannelKeyEntries(tx, id); err != nil {
				tx.Rollback()
				return 0, err
			}
		}
		result := tx.Where("id in (?)", chunk).Delete(&Channel{})
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		deletedCount += result.RowsAffected
		if err := tx.Where("channel_id in (?)", chunk).Delete(&Ability{}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	BumpSyncTopic(SyncTopicChannels)
	return deletedCount, nil
}

func (channel *Channel) GetPriority() int64 {
	if channel.Priority == nil {
		return 0
	}
	return *channel.Priority
}

func (channel *Channel) GetWeight() int {
	if channel.Weight == nil {
		return 0
	}
	return int(*channel.Weight)
}

func (channel *Channel) GetBaseURL() string {
	if channel.BaseURL == nil {
		return ""
	}
	url := *channel.BaseURL
	if url == "" {
		url = constant.GetChannelBaseURL(channel.Type)
	}
	return url
}

func (channel *Channel) GetModelMapping() string {
	if channel.ModelMapping == nil {
		return ""
	}
	return *channel.ModelMapping
}

func (channel *Channel) GetStatusCodeMapping() string {
	if channel.StatusCodeMapping == nil {
		return ""
	}
	return *channel.StatusCodeMapping
}

func (channel *Channel) Insert() error {
	err := DB.Session(&gorm.Session{Logger: gormlogger.Discard}).Transaction(func(tx *gorm.DB) error {
		channel.ChannelInfo.KeyEntryRevision = 0
		if err := tx.Create(channel).Error; err != nil {
			return err
		}
		if err := CreateChannelKeyEntries(tx, channel); err != nil {
			return err
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channel.Id).UpdateColumn("channel_info", channel.ChannelInfo).Error; err != nil {
			return err
		}
		return channel.AddAbilities(tx)
	})
	if err == nil {
		BumpSyncTopic(SyncTopicChannels)
	}
	return err
}

// remapKeyScopes keeps model scopes and user bindings attached to the same key
// when the key list changes under UpdateChannelLocked. Static keys use their
// contents; plugin credentials use their account identity so token refreshes
// keep the bindings.
func (info *ChannelInfo) remapKeyScopes(previousKeys, keys []string) {
	if len(info.MultiKeyModels) == 0 && len(info.MultiKeyUsers) == 0 {
		return
	}
	previousIndex := make(map[string]int, len(previousKeys))
	for i, key := range previousKeys {
		identity := keyScopeIdentity(key, i)
		if _, ok := previousIndex[identity]; !ok {
			previousIndex[identity] = i
		}
	}
	models := make(map[int][]string, len(info.MultiKeyModels))
	users := make(map[int]int, len(info.MultiKeyUsers))
	for i, key := range keys {
		oldIndex, ok := previousIndex[keyScopeIdentity(key, i)]
		if !ok {
			continue
		}
		if scope, ok := info.MultiKeyModels[oldIndex]; ok {
			models[i] = scope
		}
		if owner, ok := info.MultiKeyUsers[oldIndex]; ok {
			users[i] = owner
		}
	}
	if len(models) == 0 {
		models = nil
	}
	if len(users) == 0 {
		users = nil
	}
	info.MultiKeyModels, info.MultiKeyUsers = models, users
}

func keyScopeIdentity(key string, index int) string {
	key = strings.TrimSpace(key)
	if credential, ok := jsplugin.ParseCredential(key); ok {
		if credential.AccountID == "" {
			return fmt.Sprintf("credential-index\x00%s\x00%d", credential.Plugin, index)
		}
		return "credential\x00" + credential.Plugin + "\x00" + credential.AccountID
	}
	return "key\x00" + key
}

// Update writes an edited channel, all but its key: a key changes only through
// UpdateChannelLocked, so an edit never writes back a key a refresh replaced
// after the edit read it.
func (channel *Channel) Update() error {
	// Multi-key management already holds the polling lock across read and edit.
	// UpdateChannelLocked supplies the database/process lock for this merge.
	stored, err := UpdateChannelLocked(channel.Id, func(stored *Channel) error {
		// An index-based scope edit from an old key list cannot be reassigned to
		// a replacement credential. Ask the caller to reload its channel instead.
		if channel.Key != "" && stored.Key != channel.Key {
			return ErrChannelKeyChanged
		}
		// Legacy callers edit model scopes/bindings. Runtime key status, disable
		// reasons, polling position and observations belong to the stored row.
		stored.ChannelInfo.MultiKeyModels = channel.ChannelInfo.MultiKeyModels
		stored.ChannelInfo.MultiKeyUsers = channel.ChannelInfo.MultiKeyUsers
		if stored.ChannelInfo.IsMultiKey {
			stored.ChannelInfo.MultiKeySize = len(stored.GetKeys())
			for index := range stored.ChannelInfo.MultiKeyStatusList {
				if index < 0 || index >= stored.ChannelInfo.MultiKeySize {
					delete(stored.ChannelInfo.MultiKeyStatusList, index)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	channel.ChannelInfo = stored.ChannelInfo
	return channel.saveConfiguration("key", "channel_info", "status", "other_info")
}

// UpdateConfig saves an ordinary channel edit without writing the key or its
// runtime metadata. Explicit key/mode edits are merged by UpdateChannelLocked.
func (channel *Channel) UpdateConfig() error {
	return channel.saveConfiguration("key", "channel_info")
}

func (channel *Channel) saveConfiguration(omit ...string) error {
	if err := DB.Model(channel).Omit(omit...).Updates(channel).Error; err != nil {
		return err
	}
	defer BumpSyncTopic(SyncTopicChannels)
	// Read into an empty value so decoding channel_info cannot retain map
	// entries from the edit's stale snapshot.
	var stored Channel
	if err := DB.First(&stored, "id = ?", channel.Id).Error; err != nil {
		return err
	}
	*channel = stored
	return channel.UpdateAbilities(nil)
}

func (channel *Channel) UpdateResponseTime(responseTime int64) {
	err := DB.Model(channel).Select("response_time", "test_time").Updates(Channel{
		TestTime:     common.GetTimestamp(),
		ResponseTime: int(responseTime),
	}).Error
	if err != nil {
		common.SysLog(common.LogText("failed to update response time: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) UpdateBalance(balance float64) {
	err := DB.Model(channel).Select("balance_updated_time", "balance").Updates(Channel{
		BalanceUpdatedTime: common.GetTimestamp(),
		Balance:            balance,
	}).Error
	if err != nil {
		common.SysLog(common.LogText("failed to update balance: channel_id=%d, error=%v", channel.Id, err))
	}
}

func (channel *Channel) Delete() error {
	// Deletion stays idempotent when another administrator already removed
	// the row. Retire any surviving allocations and clean abilities as before.
	err := channelBudgetTransaction(context.Background(), channel.Id, true, func(tx *gorm.DB) error {
		if err := RetireChannelKeyEntries(tx, channel.Id); err != nil {
			return err
		}
		if err := tx.Where("channel_id = ?", channel.Id).Delete(&Ability{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Channel{}, channel.Id).Error
	})
	if err == nil {
		BumpSyncTopic(SyncTopicChannels)
	}
	return err
}

var channelStatusLock sync.Mutex

// channelPollingLocks stores locks for each channel.id to ensure thread-safe polling
var channelPollingLocks sync.Map

// GetChannelPollingLock returns or creates a mutex for the given channel ID
func GetChannelPollingLock(channelId int) *sync.Mutex {
	if lock, exists := channelPollingLocks.Load(channelId); exists {
		return lock.(*sync.Mutex)
	}
	// Create new lock for this channel
	newLock := &sync.Mutex{}
	actual, _ := channelPollingLocks.LoadOrStore(channelId, newLock)
	return actual.(*sync.Mutex)
}

// CleanupChannelPollingLocks removes locks for channels that no longer exist
// This is optional and can be called periodically to prevent memory leaks
func CleanupChannelPollingLocks() {
	var activeChannelIds []int
	DB.Model(&Channel{}).Pluck("id", &activeChannelIds)

	activeChannelSet := make(map[int]bool)
	for _, id := range activeChannelIds {
		activeChannelSet[id] = true
	}

	channelPollingLocks.Range(func(key, value any) bool {
		channelId := key.(int)
		if !activeChannelSet[channelId] {
			channelPollingLocks.Delete(channelId)
		}
		return true
	})
}

// sqlitePluginRefreshLocks serialize the credential refreshes of a channel on
// SQLite, which serves one node: channel id -> chan struct{} of capacity one.
var sqlitePluginRefreshLocks sync.Map

// pluginRefreshLockSpace is the first key of the PostgreSQL advisory locks of
// credential refreshes; the channel id is the second.
const pluginRefreshLockSpace = 0x6e617069

// WithPluginCredentialRefreshLock runs refresh while no other node or
// goroutine refreshes a credential of the channel, so a refresh token goes out
// once even when several nodes need a fresh credential at the same time. MySQL
// holds a named lock and PostgreSQL a transaction advisory lock, each on a
// connection of its own, and SQLite, which serves one node, a process lock.
// None of them locks the channel row: no writer of the channel waits for the
// network call refresh makes. ctx bounds the wait for the lock only.
func WithPluginCredentialRefreshLock(ctx context.Context, channelID int, refresh func() error) error {
	switch {
	case common.UsingMainDatabase(common.DatabaseTypeMySQL):
		name := fmt.Sprintf("new-api:plugin-refresh:%d", channelID)
		wait := 0
		if deadline, ok := ctx.Deadline(); ok {
			wait = max(int(time.Until(deadline)/time.Second), 0)
		}
		return DB.WithContext(ctx).Connection(func(conn *gorm.DB) error {
			var acquired sql.NullInt64
			if err := conn.Raw("SELECT GET_LOCK(?, ?)", name, wait).Row().Scan(&acquired); err != nil {
				return err
			}
			if acquired.Int64 != 1 {
				return ErrPluginRefreshLockTimeout
			}
			// The lock belongs to this connection until released, whatever
			// happened to ctx meanwhile.
			defer conn.WithContext(context.Background()).Exec("DO RELEASE_LOCK(?)", name)
			return refresh()
		})
	case common.UsingMainDatabase(common.DatabaseTypePostgreSQL):
		// The commit releases the lock; it must not fail because ctx ended
		// while refresh ran.
		return DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.WithContext(ctx).Exec("SELECT pg_advisory_xact_lock(?, ?)", pluginRefreshLockSpace, channelID).Error; err != nil {
				return err
			}
			return refresh()
		})
	default:
		value, _ := sqlitePluginRefreshLocks.LoadOrStore(channelID, make(chan struct{}, 1))
		lock := value.(chan struct{})
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return ErrPluginRefreshLockTimeout
		}
		defer func() { <-lock }()
		return refresh()
	}
}

// ErrPluginRefreshLockTimeout reports a credential refresh that waited for
// another refresh of the channel longer than it may.
var ErrPluginRefreshLockTimeout = errors.New("timed out waiting for another credential refresh of the channel")

func handlerMultiKeyUpdate(channel *Channel, usingKey string, status int, reason string) {
	keys := channel.GetKeys()
	if len(keys) == 0 {
		channel.Status = status
	} else {
		keyIndex := -1
		for i, key := range keys {
			if key == usingKey {
				keyIndex = i
				break
			}
		}
		if keyIndex < 0 {
			if usingKey != "" {
				common.SysLog(common.LogText("failed to update multi-key status: channel_id=%d, using key not found", channel.Id))
				return
			}
			channel.Status = status
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			return
		}
		if channel.ChannelInfo.MultiKeyStatusList == nil {
			channel.ChannelInfo.MultiKeyStatusList = make(map[int]int)
		}
		if status == common.ChannelStatusEnabled {
			delete(channel.ChannelInfo.MultiKeyStatusList, keyIndex)
		} else {
			channel.ChannelInfo.MultiKeyStatusList[keyIndex] = status
			if channel.ChannelInfo.MultiKeyDisabledReason == nil {
				channel.ChannelInfo.MultiKeyDisabledReason = make(map[int]string)
			}
			if channel.ChannelInfo.MultiKeyDisabledTime == nil {
				channel.ChannelInfo.MultiKeyDisabledTime = make(map[int]int64)
			}
			channel.ChannelInfo.MultiKeyDisabledReason[keyIndex] = reason
			channel.ChannelInfo.MultiKeyDisabledTime[keyIndex] = common.GetTimestamp()
		}
		if !hasEnabledMultiKey(keys, channel.ChannelInfo.MultiKeyStatusList) {
			channel.Status = common.ChannelStatusAutoDisabled
			info := channel.GetOtherInfo()
			info["status_reason"] = ChannelStatusReasonAllKeysDisabled
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
		} else if status == common.ChannelStatusEnabled {
			channel.Status = common.ChannelStatusEnabled
		}
	}
}

func hasEnabledMultiKey(keys []string, statusList map[int]int) bool {
	for i := range keys {
		if statusList == nil {
			return true
		}
		status, ok := statusList[i]
		if !ok || status == common.ChannelStatusEnabled {
			return true
		}
	}
	return false
}

func UpdateChannelStatus(channelId int, usingKey string, status int, reason string) (changed bool) {
	return updateChannelStatus(context.Background(), channelId, usingKey, status, reason, nil)
}

// UpdateChannelStatusChecked changes status only if a short, locked check of
// current configuration still accepts the completed management result.
func UpdateChannelStatusChecked(ctx context.Context, channelID int, usingKey string, status int, reason string, check func(*Channel) bool) bool {
	if check == nil {
		return false
	}
	return updateChannelStatus(ctx, channelID, usingKey, status, reason, check)
}

func updateChannelStatus(ctx context.Context, channelId int, usingKey string, status int, reason string, check func(*Channel) bool) (changed bool) {
	if usingKey != "" {
		current, err := GetChannelById(channelId, true)
		if err != nil {
			return false
		}
		if current.ChannelInfo.KeyEntryRevision > 0 {
			matches := 0
			position := -1
			for i, key := range allocationKeyList(current) {
				if key == usingKey {
					matches++
					position = i
				}
			}
			// Legacy text-only status callers cannot disambiguate duplicate allocations.
			if matches != 1 {
				return false
			}
			selected, ok := current.ChannelAllocationByPosition(position)
			if !ok {
				return false
			}
			return UpdateChannelAllocationStatusChecked(ctx, selected, status, reason, check)
		}
	}
	// A status set by hand names no key and is never an automatic disable; it
	// reaches every node within seconds. An automatic change, a disable after a
	// failure or the re-enable after a test of the key it used, is left to the
	// periodic channel sync: the keys of busy channels change status often, and
	// each bump reloads the whole channel cache on every node.
	bump := usingKey == "" && status != common.ChannelStatusAutoDisabled
	// Runs after the ability update deferred below.
	defer func() {
		if changed && bump {
			BumpSyncTopic(SyncTopicChannels)
		}
	}()
	if common.MemoryCacheEnabled {
		channelStatusLock.Lock()
		defer channelStatusLock.Unlock()
	}

	// ChannelInfo stores both multi-key status and the polling cursor. Hold the
	// same per-channel lock from the first read through persistence so neither
	// writer can save a stale JSON snapshot over the other.
	pollingLock := GetChannelPollingLock(channelId)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	if common.MemoryCacheEnabled && check == nil {
		channelCache, _ := CacheGetChannel(channelId)
		if channelCache == nil {
			return false
		}
		// A request failure must not replace an admin's manual disable;
		// otherwise automatic recovery could re-enable the channel later.
		if status == common.ChannelStatusAutoDisabled && channelCache.Status == common.ChannelStatusManuallyDisabled {
			return false
		}
		if channelCache.ChannelInfo.IsMultiKey {
			beforeStatus := channelCache.Status
			// 如果是多Key模式，更新缓存中的状态
			handlerMultiKeyUpdate(channelCache, usingKey, status, reason)
			if beforeStatus != channelCache.Status {
				CacheUpdateChannelStatus(channelId, channelCache.Status)
			}
			//CacheUpdateChannel(channelCache)
			//return true
		} else {
			// 如果缓存渠道存在，且状态已是目标状态，直接返回
			if channelCache.Status == status {
				return false
			}
			CacheUpdateChannelStatus(channelId, status)
		}
	}

	shouldUpdateAbilities := false
	defer func() {
		if shouldUpdateAbilities {
			err := UpdateAbilityStatus(channelId, status == common.ChannelStatusEnabled)
			if err != nil {
				common.SysLog(common.LogText("failed to update ability status: channel_id=%d, error=%v", channelId, err))
			}
		}
	}()
	// The status, the key statuses and the status reason are written from the
	// stored row, keeping what another writer stores in other_info meanwhile.
	var source Channel
	var committed *Channel
	update := func(channel *Channel) (map[string]any, error) {
		shouldUpdateAbilities = false
		source = *channel
		if check != nil && !check(channel) {
			return nil, nil
		}
		// Check the stored row too, since this node's cache may be stale.
		if status == common.ChannelStatusAutoDisabled && channel.Status == common.ChannelStatusManuallyDisabled {
			return nil, nil
		}
		// The master runs a plugin's scheduled hook when a key is disabled
		// automatically, so another node's disable reaches it within seconds.
		if status == common.ChannelStatusAutoDisabled && !common.IsMasterNode && channelPluginHasHook(channel, "scheduled") {
			bump = true
		}
		// A manual channel operation must replace the exhaustion reason even
		// when the status value is already manually disabled.
		overridesKeyExhaustion := channel.ChannelInfo.IsMultiKey && usingKey == "" &&
			status == common.ChannelStatusManuallyDisabled && reason != ChannelStatusReasonAllKeysDisabled &&
			channel.GetOtherInfo()["status_reason"] == ChannelStatusReasonAllKeysDisabled
		if channel.Status == status && !overridesKeyExhaustion {
			return nil, nil
		}

		if channel.ChannelInfo.IsMultiKey {
			beforeStatus := channel.Status
			handlerMultiKeyUpdate(channel, usingKey, status, reason)
			shouldUpdateAbilities = beforeStatus != channel.Status
		} else {
			info := channel.GetOtherInfo()
			info["status_reason"] = reason
			info["status_time"] = common.GetTimestamp()
			channel.SetOtherInfo(info)
			channel.Status = status
			shouldUpdateAbilities = true
		}
		updates := map[string]any{"status": channel.Status}
		if channel.ChannelInfo.IsMultiKey {
			updates["channel_info"] = channel.ChannelInfo
		}
		committed = channel
		return updates, nil
	}
	var written bool
	var err error
	if check == nil {
		written, err = UpdateChannelOtherInfo(channelId, update)
	} else {
		written, err = UpdateChannelOtherInfoLocked(ctx, channelId, update)
	}
	shouldUpdateAbilities = shouldUpdateAbilities && written
	if written && check != nil {
		// Publish only the status fields after the guarded write commits. Never
		// let an obsolete result mutate cache before its database check.
		bump = true
		publishChannelStatus(&source, committed)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		common.SysLog(common.LogText("failed to update channel status: channel_id=%d, status=%d, error=%v", channelId, status, err))
	}
	return written
}

// publishChannelStatus preserves the current cached configuration when a delayed
// status write commits. Allocation revisions also prevent an older status result
// from replacing a newer result for identical credentials.
func publishChannelStatus(source, committed *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	cached, ok := channelsIDM[committed.Id]
	if !ok || cached.Type != source.Type || cached.Key != source.Key || cached.Status != source.Status ||
		cached.ChannelInfo.IsMultiKey != source.ChannelInfo.IsMultiKey || cached.GetBaseURL() != source.GetBaseURL() ||
		cached.ChannelInfo.KeyEntryRevision != source.ChannelInfo.KeyEntryRevision ||
		!slices.Equal(cached.KeyEntryIDs, source.KeyEntryIDs) || !slices.Equal(cached.KeyEntryRevisions, source.KeyEntryRevisions) ||
		!reflect.DeepEqual(cached.ChannelInfo.MultiKeyModels, source.ChannelInfo.MultiKeyModels) ||
		!maps.Equal(cached.ChannelInfo.MultiKeyScopeModes, source.ChannelInfo.MultiKeyScopeModes) ||
		!maps.Equal(cached.ChannelInfo.MultiKeyUsers, source.ChannelInfo.MultiKeyUsers) ||
		cached.OtherSettings != source.OtherSettings || cached.Other != source.Other ||
		cached.Models != source.Models || cached.Group != source.Group ||
		!reflect.DeepEqual(cached.ModelMapping, source.ModelMapping) ||
		!reflect.DeepEqual(cached.ParamOverride, source.ParamOverride) ||
		!reflect.DeepEqual(cached.OpenAIOrganization, source.OpenAIOrganization) ||
		!reflect.DeepEqual(cached.HeaderOverride, source.HeaderOverride) {
		return
	}
	sameSettings := reflect.DeepEqual(cached.Setting, source.Setting)
	if !sameSettings {
		// Admission-only cap changes do not invalidate a completed result.
		// Preserve every other raw setting, including unknown provider fields.
		cachedSetting, sourceSetting := "{}", "{}"
		if cached.Setting != nil && *cached.Setting != "" {
			cachedSetting = *cached.Setting
		}
		if source.Setting != nil && *source.Setting != "" {
			sourceSetting = *source.Setting
		}
		var cachedSettings, sourceSettings map[string]json.RawMessage
		if common.UnmarshalJsonStr(cachedSetting, &cachedSettings) == nil && cachedSettings != nil &&
			common.UnmarshalJsonStr(sourceSetting, &sourceSettings) == nil && sourceSettings != nil {
			delete(cachedSettings, "max_concurrent_requests")
			delete(sourceSettings, "max_concurrent_requests")
			sameSettings = reflect.DeepEqual(cachedSettings, sourceSettings)
		}
	}
	if !sameSettings {
		return
	}
	updated := *cached
	updated.ChannelInfo.MultiKeyStatusList = committed.ChannelInfo.MultiKeyStatusList
	updated.ChannelInfo.MultiKeyDisabledReason = committed.ChannelInfo.MultiKeyDisabledReason
	updated.ChannelInfo.MultiKeyDisabledTime = committed.ChannelInfo.MultiKeyDisabledTime
	updated.ChannelInfo.KeyEntryRevision = committed.ChannelInfo.KeyEntryRevision
	updated.KeyEntryRevisions = committed.KeyEntryRevisions
	other, committedOther := updated.GetOtherInfo(), committed.GetOtherInfo()
	for _, key := range []string{"status_reason", "status_time"} {
		if value, exists := committedOther[key]; exists {
			other[key] = value
		} else {
			delete(other, key)
		}
	}
	updated.SetOtherInfo(other)
	channelsIDM[updated.Id] = &updated
	cacheUpdateChannelStatusLocked(updated.Id, committed.Status)
	if cached.Status != common.ChannelStatusEnabled && committed.Status == common.ChannelStatusEnabled {
		// A full reload used to restore these routes. Rebuild only this
		// channel's public routes from the retained current configuration.
		for _, ability := range updated.channelAbilities() {
			if group2model2channels[ability.Group] == nil {
				group2model2channels[ability.Group] = make(map[string][]int)
			}
			ids := group2model2channels[ability.Group][ability.Model]
			if !slices.Contains(ids, updated.Id) {
				ids = append(ids, updated.Id)
				slices.SortStableFunc(ids, func(a, b int) int {
					return cmp.Compare(channelsIDM[b].GetPriority(), channelsIDM[a].GetPriority())
				})
				group2model2channels[ability.Group][ability.Model] = ids
			}
		}
	}
}

// channelPluginHasHook reports a task plugin channel whose plugin exports
// hook. It reads the setting without GetSetting, which clears an invalid
// stored setting.
func channelPluginHasHook(ch *Channel, hook string) bool {
	var setting dto.ChannelSettings
	if ch.Type != constant.ChannelTypeTaskPlugin || ch.Setting == nil || common.UnmarshalJsonStr(*ch.Setting, &setting) != nil {
		return false
	}
	plugin, ok := jsplugin.DefaultRegistry.Get(setting.TaskPluginKey)
	return ok && plugin.HasProviderHook(hook)
}

func EnableChannelByTag(tag string) error {
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusEnabled).Error
	if err != nil {
		return err
	}
	defer BumpSyncTopic(SyncTopicChannels)
	err = UpdateAbilityStatusByTag(tag, true)
	return err
}

func DisableChannelByTag(tag string) error {
	// Explicit tag-level disable also cancels automatic restoration for
	// channels that were already disabled because all keys were unavailable.
	var channels []Channel
	if err := DB.Where("tag = ?", tag).Find(&channels).Error; err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.ChannelInfo.IsMultiKey && channel.GetOtherInfo()["status_reason"] == ChannelStatusReasonAllKeysDisabled {
			if !UpdateChannelStatus(channel.Id, "", common.ChannelStatusManuallyDisabled, "manual tag operation") {
				return fmt.Errorf("failed to disable channel #%d by tag", channel.Id)
			}
		}
	}
	err := DB.Model(&Channel{}).Where("tag = ?", tag).Update("status", common.ChannelStatusManuallyDisabled).Error
	if err != nil {
		return err
	}
	defer BumpSyncTopic(SyncTopicChannels)
	err = UpdateAbilityStatusByTag(tag, false)
	return err
}

func EditChannelByTag(tag string, newTag *string, modelMapping *string, models *string, group *string, priority *int64, weight *uint, paramOverride *string, headerOverride *string) error {
	updateData := Channel{}
	shouldReCreateAbilities := false
	updatedTag := tag
	// 如果 newTag 不为空且不等于 tag，则更新 tag
	if newTag != nil && *newTag != tag {
		updateData.Tag = newTag
		updatedTag = *newTag
	}
	if modelMapping != nil {
		updateData.ModelMapping = modelMapping
	}
	if models != nil && *models != "" {
		shouldReCreateAbilities = true
		updateData.Models = *models
	}
	if group != nil && *group != "" {
		shouldReCreateAbilities = true
		updateData.Group = *group
	}
	if priority != nil {
		updateData.Priority = priority
	}
	if weight != nil {
		updateData.Weight = weight
	}
	if paramOverride != nil {
		updateData.ParamOverride = paramOverride
	}
	if headerOverride != nil {
		updateData.HeaderOverride = headerOverride
	}

	err := DB.Model(&Channel{}).Where("tag = ?", tag).Updates(updateData).Error
	if err != nil {
		return err
	}
	defer BumpSyncTopic(SyncTopicChannels)
	if shouldReCreateAbilities {
		channels, err := GetChannelsByTag(updatedTag, false, false)
		if err == nil {
			for _, channel := range channels {
				err = channel.UpdateAbilities(nil)
				if err != nil {
					common.SysLog(common.LogText("failed to update abilities: channel_id=%d, tag=%s, error=%v", channel.Id, channel.GetTag(), err))
				}
			}
		}
	} else {
		err := UpdateAbilityByTag(tag, newTag, priority, weight)
		if err != nil {
			return err
		}
	}
	return nil
}

func UpdateChannelUsedQuota(id int, quota int) {
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeChannelUsedQuota, id, quota)
		return
	}
	updateChannelUsedQuota(id, quota)
}

func updateChannelUsedQuota(id int, quota int) {
	err := DB.Model(&Channel{}).Where("id = ?", id).Update("used_quota", gorm.Expr("used_quota + ?", quota)).Error
	if err != nil {
		common.SysLog(common.LogText("failed to update channel used quota: channel_id=%d, delta_quota=%d, error=%v", id, quota, err))
	}
}

func DeleteChannelByStatus(status int64) (int64, error) {
	result := DB.Where("status = ?", status).Delete(&Channel{})
	return result.RowsAffected, result.Error
}

func DeleteDisabledChannel() (int64, error) {
	result := DB.Where("status = ? or status = ?", common.ChannelStatusAutoDisabled, common.ChannelStatusManuallyDisabled).Delete(&Channel{})
	if result.Error == nil {
		BumpSyncTopic(SyncTopicChannels)
	}
	return result.RowsAffected, result.Error
}

func GetPaginatedTags(offset int, limit int) ([]*string, error) {
	return GetPaginatedChannelTags(DB.Model(&Channel{}), offset, limit)
}

func GetPaginatedChannelTags(query *gorm.DB, offset int, limit int) ([]*string, error) {
	var tags []*string
	err := query.
		Select("DISTINCT tag").
		Where("tag is not null AND tag != ''").
		Order(clause.OrderByColumn{Column: clause.Column{Name: "tag"}}).
		Offset(offset).
		Limit(limit).
		Find(&tags).Error
	return tags, err
}

// SearchTags lists the tags matching a keyword. It exists for callers without a
// request context; request paths use SearchTagsWithContext so a cancelled client
// request also cancels this query.
func SearchTags(keyword string, group string, model string, idSort bool) ([]*string, error) {
	return searchTags(context.Background(), keyword, group, model, idSort)
}

// SearchTagsWithContext is SearchTags bound to the caller's request context.
func SearchTagsWithContext(ctx context.Context, keyword string, group string, model string, idSort bool) ([]*string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return searchTags(ctx, keyword, group, model, idSort)
}

func searchTags(ctx context.Context, keyword string, group string, model string, idSort bool) ([]*string, error) {
	var tags []*string
	modelsCol := "`models`"

	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		modelsCol = `"models"`
	}

	baseURLCol := "`base_url`"
	// 如果是 PostgreSQL，使用双引号
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		baseURLCol = `"base_url"`
	}

	order := "priority desc"
	if idSort {
		order = "id desc"
	}

	// 构造基础查询
	baseQuery := DB.WithContext(ctx).Model(&Channel{}).Omit("key")

	// 构造WHERE子句
	whereClause := "(id = ? OR name LIKE ? OR " + commonKeyCol + " = ? OR " + baseURLCol + " LIKE ?) AND " + modelsCol + " LIKE ?"
	args := []any{common.String2Int(keyword), "%" + keyword + "%", keyword, "%" + keyword + "%", "%" + model + "%"}
	baseQuery = ApplyChannelGroupFilter(baseQuery.Where(whereClause, args...), group)

	subQuery := baseQuery.
		Select("tag").
		Where("tag != ''").
		Order(order)

	err := DB.WithContext(ctx).Table("(?) as sub", subQuery).
		Select("DISTINCT tag").
		Find(&tags).Error

	if err != nil {
		return nil, err
	}

	return tags, nil
}

func (channel *Channel) ValidateSettings() error {
	channelParams := &dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), channelParams)
		if err != nil {
			return err
		}
	}
	if _, err := common.ParseProxyURLStrict(channelParams.Proxy); err != nil {
		return fmt.Errorf("invalid channel proxy: %w", err)
	}
	if err := channelParams.ValidateHTTPTransport(); err != nil {
		return err
	}
	if err := channelParams.ValidateRequestLimits(); err != nil {
		return err
	}
	if err := common.ValidateChannelSupportedEndpoints(channel.Type, channelParams); err != nil {
		return err
	}
	channelOtherSettings := &dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, channelOtherSettings)
		if err != nil {
			return err
		}
	}
	if err := channelOtherSettings.ValidateToolLossPolicy(); err != nil {
		return err
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		channelOtherSettings.AdvancedCustom = preset
	}
	if constant.IsAdvancedCustomChannel(channel.Type) {
		if channelOtherSettings.AdvancedCustom == nil {
			return fmt.Errorf("advanced_custom is required")
		}
	}
	if channelOtherSettings.AdvancedCustom != nil {
		if err := channelOtherSettings.AdvancedCustom.Validate(); err != nil {
			return err
		}
	}
	if constant.IsAdvancedCustomChannel(channel.Type) && channelOtherSettings.UpstreamModelUpdateCheckEnabled {
		if _, ok := channelOtherSettings.AdvancedCustom.ModelListRoute(); !ok {
			return fmt.Errorf("advanced custom channels require a %s route when upstream model update checks are enabled", dto.AdvancedCustomModelListPath)
		}
	}
	return nil
}

func (channel *Channel) GetSetting() dto.ChannelSettings {
	setting := dto.ChannelSettings{}
	if channel.Setting != nil && *channel.Setting != "" {
		err := common.Unmarshal([]byte(*channel.Setting), &setting)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.Setting = nil // 清空设置以避免后续错误
			// Only the cleared column is written: the rest of this copy, its key
			// included, may be older than the stored channel.
			_ = DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("setting", nil).Error
		}
	}
	return setting
}

func (channel *Channel) SetSetting(setting dto.ChannelSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(common.LogText("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.Setting = common.GetPointer[string](string(settingBytes))
}

func (channel *Channel) GetOtherSettings() dto.ChannelOtherSettings {
	setting := dto.ChannelOtherSettings{}
	if channel.OtherSettings != "" {
		err := common.UnmarshalJsonStr(channel.OtherSettings, &setting)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal setting: channel_id=%d, error=%v", channel.Id, err))
			channel.OtherSettings = "{}" // 清空设置以避免后续错误
			// Only the cleared column is written, as in GetSetting.
			_ = DB.Model(&Channel{}).Where("id = ?", channel.Id).Update("settings", channel.OtherSettings).Error
		}
	}
	if preset := common.GetAdvancedCustomPreset(channel.Type); preset != nil {
		setting.AdvancedCustom = preset
	}
	return setting
}

func (channel *Channel) SetOtherSettings(setting dto.ChannelOtherSettings) {
	settingBytes, err := common.Marshal(setting)
	if err != nil {
		common.SysLog(common.LogText("failed to marshal setting: channel_id=%d, error=%v", channel.Id, err))
		return
	}
	channel.OtherSettings = string(settingBytes)
}

func (channel *Channel) GetParamOverride() map[string]any {
	paramOverride := make(map[string]any)
	if channel.ParamOverride != nil && *channel.ParamOverride != "" {
		err := common.Unmarshal([]byte(*channel.ParamOverride), &paramOverride)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal param override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return paramOverride
}

func (channel *Channel) GetHeaderOverride() map[string]any {
	headerOverride := make(map[string]any)
	if channel.HeaderOverride != nil && *channel.HeaderOverride != "" {
		err := common.Unmarshal([]byte(*channel.HeaderOverride), &headerOverride)
		if err != nil {
			common.SysLog(common.LogText("failed to unmarshal header override: channel_id=%d, error=%v", channel.Id, err))
		}
	}
	return headerOverride
}

func GetChannelsByIds(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Where("id in (?)", ids).Find(&channels).Error
	return channels, err
}

// GetChannelConcurrencySettings reads only the fields needed by the live
// dashboard sampler, avoiding channel credentials and large key metadata on
// each refresh.
func GetChannelConcurrencySettings(ids []int) ([]*Channel, error) {
	var channels []*Channel
	err := DB.Select("id", "setting").Where("id IN ?", ids).Find(&channels).Error
	return channels, err
}

func BatchSetChannelTag(ids []int, tag *string) error {
	// 开启事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	// 更新标签
	err := tx.Model(&Channel{}).Where("id in (?)", ids).Update("tag", tag).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	// Read our uncommitted tag changes before rebuilding abilities.
	var channels []*Channel
	err = tx.Where("id in (?)", ids).Find(&channels).Error
	if err != nil {
		tx.Rollback()
		return err
	}

	for _, channel := range channels {
		err = channel.UpdateAbilities(tx)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	// 提交事务
	if err = tx.Commit().Error; err != nil {
		return err
	}
	BumpSyncTopic(SyncTopicChannels)
	return nil
}

// CountAllChannels returns total channels in DB
func CountAllChannels() (int64, error) {
	var total int64
	err := DB.Model(&Channel{}).Count(&total).Error
	return total, err
}

// CountAllTags returns number of non-empty distinct tags
func CountAllTags() (int64, error) {
	return CountChannelTags(DB.Model(&Channel{}))
}

func CountChannelTags(query *gorm.DB) (int64, error) {
	var total int64
	err := query.Where("tag is not null AND tag != ''").Distinct("tag").Count(&total).Error
	return total, err
}

// Get channels of specified type with pagination
func GetChannelsByType(startIdx int, num int, idSort bool, channelType int) ([]*Channel, error) {
	var channels []*Channel
	order := "priority desc"
	if idSort {
		order = "id desc"
	}
	err := DB.Where("type = ?", channelType).Order(order).Limit(num).Offset(startIdx).Omit("key").Find(&channels).Error
	return channels, err
}

// Count channels of specific type
func CountChannelsByType(channelType int) (int64, error) {
	var count int64
	err := DB.Model(&Channel{}).Where("type = ?", channelType).Count(&count).Error
	return count, err
}

// Return map[type]count for all channels
func CountChannelsGroupByType() (map[int64]int64, error) {
	type result struct {
		Type  int64 `gorm:"column:type"`
		Count int64 `gorm:"column:count"`
	}
	var results []result
	err := DB.Model(&Channel{}).Select("type, count(*) as count").Group("type").Find(&results).Error
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int64)
	for _, r := range results {
		counts[r.Type] = r.Count
	}
	return counts, nil
}
