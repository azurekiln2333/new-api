package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Token struct {
	Id                 int            `json:"id"`
	UserId             int            `json:"user_id" gorm:"index"`
	Key                string         `json:"key" gorm:"type:varchar(128);uniqueIndex"`
	Status             int            `json:"status" gorm:"default:1"`
	Name               string         `json:"name" gorm:"index" `
	CreatedTime        int64          `json:"created_time" gorm:"bigint"`
	AccessedTime       int64          `json:"accessed_time" gorm:"bigint"`
	ExpiredTime        int64          `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota        int            `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota     bool           `json:"unlimited_quota"`
	ModelLimitsEnabled bool           `json:"model_limits_enabled"`
	ModelLimits        string         `json:"model_limits" gorm:"type:text"`
	AllowIps           *string        `json:"allow_ips" gorm:"default:''"`
	UsedQuota          int            `json:"used_quota" gorm:"default:0"` // used quota
	Group              string         `json:"group" gorm:"default:''"`
	CrossGroupRetry    bool           `json:"cross_group_retry"` // 跨分组重试，仅auto分组有效
	AutoGroups         string         `json:"-" gorm:"type:text"`
	DeletedAt          gorm.DeletedAt `gorm:"index"`
	// 允许用此令牌查询所属用户的额度；新建令牌默认开启，已有令牌升级后保持关闭
	AllowUserQuotaQuery bool   `json:"allow_user_quota_query"`
	CacheExpiresAt      int64  `json:"-" gorm:"-"`
	CacheMAC            string `json:"-" gorm:"-"`
}

func (token *Token) GetAutoGroups() ([]string, error) {
	if token.AutoGroups == "" {
		return nil, nil
	}
	var groups []string
	if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func (token *Token) SetAutoGroups(groups []string) error {
	if len(groups) == 0 {
		token.AutoGroups = ""
		return nil
	}
	data, err := common.Marshal(groups)
	if err != nil {
		return err
	}
	token.AutoGroups = string(data)
	return nil
}

func (token *Token) Clean() {
	token.Key = ""
}

func MaskTokenKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return strings.Repeat("*", len(key))
	}
	if len(key) <= 8 {
		return key[:2] + "****" + key[len(key)-2:]
	}
	return key[:4] + "**********" + key[len(key)-4:]
}

func (token *Token) GetFullKey() string {
	return token.Key
}

func (token *Token) GetMaskedKey() string {
	return MaskTokenKey(token.Key)
}

func (token *Token) GetIpLimits() []string {
	// delete empty spaces
	//split with \n
	ipLimits := make([]string, 0)
	if token.AllowIps == nil {
		return ipLimits
	}
	cleanIps := strings.ReplaceAll(*token.AllowIps, " ", "")
	if cleanIps == "" {
		return ipLimits
	}
	ips := strings.SplitSeq(cleanIps, "\n")
	for ip := range ips {
		ip = strings.TrimSpace(ip)
		ip = strings.ReplaceAll(ip, ",", "")
		if ip != "" {
			ipLimits = append(ipLimits, ip)
		}
	}
	return ipLimits
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

// sanitizeLikePattern 校验并清洗用户输入的 LIKE 搜索模式。
// 规则：
//  1. 转义 ! 和 _（使用 ! 作为 ESCAPE 字符，兼容 MySQL/PostgreSQL/SQLite）
//  2. 连续的 % 合并为单个 %
//  3. 最多允许 2 个 %
//  4. 含 % 时（模糊搜索），去掉 % 后关键词长度必须 >= 2
//  5. 不含 % 时按精确匹配
func sanitizeLikePattern(input string) (string, error) {
	// 1. 先转义 ESCAPE 字符 ! 自身，再转义 _
	//    使用 ! 而非 \ 作为 ESCAPE 字符，避免 MySQL 中反斜杠的字符串转义问题
	input = strings.ReplaceAll(input, "!", "!!")
	input = strings.ReplaceAll(input, `_`, `!_`)

	if err := validateLikePattern(input); err != nil {
		return "", err
	}

	// 5. 无 % 时，精确全匹配
	return input, nil
}

func validateLikePattern(input string) error {
	// 1. 连续的 % 直接拒绝
	if strings.Contains(input, "%%") {
		return common.NewMessage("The search pattern cannot contain consecutive % wildcards")
	}

	// 2. 统计 % 数量，不得超过 2
	count := strings.Count(input, "%")
	if count > 2 {
		return common.NewMessage("The search pattern can contain at most 2 % wildcards")
	}

	// 3. 含 % 时，去掉 % 后关键词长度必须 >= 2
	if count > 0 {
		stripped := strings.ReplaceAll(input, "%", "")
		if len(stripped) < 2 {
			return common.NewMessage("Fuzzy search keywords must be at least 2 characters long")
		}
	}

	return nil
}

const searchHardLimit = 100

func SearchUserTokens(userId int, keyword string, token string, offset int, limit int) (tokens []*Token, total int64, err error) {
	// model 层强制截断
	if limit <= 0 || limit > searchHardLimit {
		limit = searchHardLimit
	}
	if offset < 0 {
		offset = 0
	}

	if token != "" {
		token = strings.TrimPrefix(token, "sk-")
	}

	// 超量用户（令牌数超过上限）只允许精确搜索，禁止模糊搜索
	maxTokens := operation_setting.GetMaxUserTokens()
	hasFuzzy := strings.Contains(keyword, "%") || strings.Contains(token, "%")
	if hasFuzzy {
		count, err := CountUserTokens(userId)
		if err != nil {
			common.SysLog(common.LogText("failed to count user tokens: %s", err.Error()))
			return nil, 0, common.NewMessage("Failed to get the token count")
		}
		if int(count) > maxTokens {
			return nil, 0, common.NewMessage("Token count exceeds the limit. Only exact search is allowed, do not use the % wildcard")
		}
	}

	baseQuery := DB.Model(&Token{}).Where("user_id = ?", userId)

	// 非空才加 LIKE 条件，空则跳过（不过滤该字段）
	if keyword != "" {
		keywordPattern, err := sanitizeLikePattern(keyword)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where("name LIKE ? ESCAPE '!'", keywordPattern)
	}
	if token != "" {
		tokenPattern, err := sanitizeLikePattern(token)
		if err != nil {
			return nil, 0, err
		}
		baseQuery = baseQuery.Where(commonKeyCol+" LIKE ? ESCAPE '!'", tokenPattern)
	}

	// 先查匹配总数（用于分页，受 maxTokens 上限保护，避免全表 COUNT）
	err = baseQuery.Limit(maxTokens).Count(&total).Error
	if err != nil {
		common.SysError(common.LogText("failed to count search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}

	// 再分页查数据
	err = baseQuery.Order("id desc").Offset(offset).Limit(limit).Find(&tokens).Error
	if err != nil {
		common.SysError(common.LogText("failed to search tokens: %s", err.Error()))
		return nil, 0, common.NewMessage("Failed to search tokens")
	}
	return tokens, total, nil
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, ErrTokenNotProvided
	}
	token, err = GetTokenByKey(key, false)
	if err == nil {
		if token.Status == common.TokenStatusExhausted ||
			token.Status == common.TokenStatusExpired ||
			token.Status != common.TokenStatusEnabled {
			return token, ErrTokenInvalid
		}
		if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExpired
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog(common.LogText("failed to update token status%s", err.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		if !token.UnlimitedQuota && token.RemainQuota <= 0 {
			if !common.RedisEnabled {
				token.Status = common.TokenStatusExhausted
				err := token.SelectUpdate()
				if err != nil {
					common.SysLog(common.LogText("failed to update token status%s", err.Error()))
				}
			}
			return token, ErrTokenInvalid
		}
		return token, nil
	}
	common.SysLog(common.LogText("ValidateUserToken: failed to get token: %s", err.Error()))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTokenInvalid
	}
	return nil, fmt.Errorf("%w: %v", ErrDatabase, err)
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("token id is empty")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	return &token, err
}

// lockTokenCredentialSnapshot serializes credential replacement with the
// database snapshot held through cache publication. SQLite has no row locks;
// a zero-row write takes its writer lock even for a custom deferred DSN, before
// a read snapshot exists. It does not modify a token or its quota counters.
func lockTokenCredentialSnapshot(tx *gorm.DB) (*gorm.DB, error) {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		if err := tx.Model(&Token{}).Where("1 = 0").UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return nil, err
		}
	}
	return lockForUpdate(tx), nil
}

func GetTokenByKey(key string, fromDB bool) (token *Token, err error) {
	// Credentials must never appear in SQL traces, including DB.Debug().
	db := DB.Session(&gorm.Session{Logger: logger.Discard})
	if !fromDB && common.RedisEnabled {
		token, err := cacheGetTokenByKey(key)
		if err == nil {
			return token, nil
		}
	}
	// Capture the Redis generation before the snapshot. If Redis is down or
	// fenced, serve the database without publishing a cache entry.
	generation, _ := tokenCacheReadGeneration(key)
	token = &Token{}
	if generation == "" {
		err = db.Where(map[string]any{"key": key}).First(token).Error
	} else {
		err = db.Transaction(func(tx *gorm.DB) error {
			// Rotation holds the same row lock until commit. Even if Redis is
			// restarted during rotation, a new cache miss cannot publish the old
			// database value between invalidation and commit.
			locked, err := lockTokenCredentialSnapshot(tx)
			if err != nil {
				return err
			}
			if err := locked.Where(map[string]any{"key": key}).First(token).Error; err != nil {
				return err
			}
			// Publication stays inside the row lock. A timed-out Redis command
			// arriving later is rejected by the mutation's generation change.
			if _, cacheErr := cacheInitToken(*token, generation); cacheErr != nil {
				common.SysLog(common.LogText("failed to init token cache: %s", cacheErr.Error()))
			}
			return nil
		})
	}
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, ErrDatabase
	}
	return token, nil
}

func (token *Token) Insert() error {
	var err error
	err = DB.Session(&gorm.Session{Logger: logger.Discard}).Create(token).Error
	if err != nil {
		// A unique-index error quotes the conflicting value, which for a token is
		// the secret itself; replace it with a stable, value-free message.
		return mapTokenKeyConflict(err)
	}
	return nil
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() (err error) {
	// 写库前失效缓存并设置 fence，防止并发读者把过期快照重新写回缓存。
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before update: %s", cacheErr.Error()))
	}
	return DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "unlimited_quota",
		"model_limits_enabled", "model_limits", "allow_ips", "group", "cross_group_retry", "auto_groups",
		"allow_user_quota_query").Updates(token).Error
}

func (token *Token) SelectUpdate() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before status update: %s", cacheErr.Error()))
	}
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() (err error) {
	if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before delete: %s", cacheErr.Error()))
	}
	return DB.Delete(token).Error
}

// RotateTokenKey replaces the credential of one existing token owned by userId
// with a freshly generated secret. See RotateTokenKeyWithSecret for the path
// that accepts a user-chosen value.
func RotateTokenKey(id int, userId int) (*Token, error) {
	return rotateTokenKey(id, userId, "")
}

// RotateTokenKeyWithSecret rotates a token to a validated user-chosen secret.
// An empty customKey keeps the secure random generator. The caller sends the
// value exactly as the user typed it: surrounding whitespace is invalid rather
// than silently trimmed, so a mistyped secret is rejected instead of stored in
// a form the user did not choose.
func RotateTokenKeyWithSecret(id int, userId int, customKey string) (*Token, error) {
	if customKey != "" {
		normalized, err := NormalizeCustomTokenKey(customKey)
		if err != nil {
			return nil, err
		}
		customKey = normalized
	}
	return rotateTokenKey(id, userId, customKey)
}

// rotateTokenKey replaces the credential of one existing token owned by userId.
// The record keeps its id, name, status, quota, limits and usage history; only
// the key changes, so clients that cached the old credential lose access while
// the token's configuration and statistics stay attached to the same row.
// The old credential's cache is fenced before the write, and the row is locked
// for the duration of the rotation so two concurrent rotations cannot interleave.
// An empty customKey keeps the secure random generator.
func rotateTokenKey(id int, userId int, customKey string) (*Token, error) {
	if id <= 0 || userId <= 0 {
		return nil, errors.New("invalid token id")
	}
	var rotated *Token
	err := DB.Session(&gorm.Session{Logger: logger.Discard}).Transaction(func(tx *gorm.DB) error {
		locked, err := lockTokenCredentialSnapshot(tx)
		if err != nil {
			return err
		}
		var token Token
		if err := locked.Where("id = ? AND user_id = ?", id, userId).First(&token).Error; err != nil {
			return err
		}
		key := customKey
		if key == "" {
			generated, err := common.GenerateKey()
			if err != nil {
				return err
			}
			key = generated
		} else {
			if key == token.Key {
				return ErrTokenKeyUnchanged
			}
			// Rejects a key another row already owns without revealing whose it
			// is; the unique index remains the final guard against a race.
			var taken int64
			if err := tx.Unscoped().Model(&Token{}).Where(map[string]any{"key": key}).Where("id <> ?", token.Id).Count(&taken).Error; err != nil {
				return ErrDatabase
			}
			if taken > 0 {
				return ErrTokenKeyUnavailable
			}
		}
		// Abort before the write if cache invalidation fails. Cache-miss readers
		// hold this row lock through publication; deleting the cache generation
		// rejects delayed snapshots even after the temporary fence expires.
		if cacheErr := invalidateTokenCacheForMutation(token.Key); cacheErr != nil {
			return ErrDatabase
		}
		// The key column is unique across every user: a duplicate is rejected by
		// the database and rolls the whole rotation back, leaving the old
		// credential valid rather than silently sharing another token's key.
		// The column name stays unquoted here so GORM's dialect handles quoting.
		if err := tx.Model(&Token{}).Where("id = ?", token.Id).Update("key", key).Error; err != nil {
			return mapTokenKeyConflict(err)
		}
		token.Key = key
		rotated = &token
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrTokenKeyUnavailable) || errors.Is(err, ErrTokenKeyUnchanged) {
			return nil, err
		}
		return nil, mapTokenKeyConflict(err)
	}
	return rotated, nil
}

// mapTokenKeyConflict replaces a driver error with a stable, value-free message.
// Unique-index errors quote the conflicting value, which here is the secret
// itself, so they must never reach the client or a log line.
func mapTokenKeyConflict(err error) error {
	if isTokenKeyConflict(err) {
		return ErrTokenKeyUnavailable
	}
	// Other driver errors can include SQL values too (for example an invalid
	// encoding or failed constraint). Do not return or log the raw driver text.
	return ErrDatabase
}

func isTokenKeyConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") ||
		strings.Contains(message, "duplicate entry") ||
		strings.Contains(message, "duplicate key")
}

// Custom API key contract. A user-chosen secret is stored without the optional
// "sk-" prefix and must be 32-128 ASCII letters or digits. These checks reject
// obvious weak values; they do not make a user-chosen string equivalent to a
// generated secret.
const (
	CustomTokenKeyMinLength   = 32
	CustomTokenKeyMaxLength   = 128
	customTokenKeyMinDistinct = 8
)

var (
	ErrTokenKeyUnavailable = common.NewMessage("This API key is already in use")
	ErrTokenKeyUnchanged   = common.NewMessage("The new API key must differ from the current one")
)

// NormalizeCustomTokenKey validates a user-supplied credential and returns the
// stored value: the optional leading "sk-" is removed, everything else must be
// ASCII letters or digits. Embedded separators, whitespace and control
// characters are rejected because middleware/auth.go parses hyphens as routing
// suffixes and printability cannot be assumed for arbitrary input.
func NormalizeCustomTokenKey(raw string) (string, error) {
	value := raw
	if strings.HasPrefix(value, "sk-") {
		value = value[len("sk-"):]
	}
	if len(value) < CustomTokenKeyMinLength || len(value) > CustomTokenKeyMaxLength {
		return "", common.NewMessage("The API key must be {{min}}-{{max}} characters after the optional prefix", map[string]any{"min": CustomTokenKeyMinLength, "max": CustomTokenKeyMaxLength})
	}
	distinct := make(map[byte]struct{}, customTokenKeyMinDistinct)
	for i := range len(value) {
		c := value[i]
		if !isTokenKeyChar(c) {
			return "", common.NewMessage("The API key may only contain ASCII letters and digits")
		}
		distinct[c] = struct{}{}
	}
	if len(distinct) < customTokenKeyMinDistinct {
		return "", common.NewMessage("The API key is too easy to guess; use at least {{count}} different characters", map[string]any{"count": customTokenKeyMinDistinct})
	}
	lower := strings.ToLower(value)
	if isTrivialTokenSequence(lower) {
		return "", common.NewMessage("The API key is a predictable sequence; choose an unrelated value")
	}
	if hasRepeatingTokenMotif(lower, 16) {
		return "", common.NewMessage("The API key repeats too short a pattern; choose an unrelated value")
	}
	if hasTrivialTokenKeyPrefix(lower) {
		return "", common.NewMessage("The API key contains a common easy-to-guess word")
	}
	return value, nil
}

func isTokenKeyChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// trivialTokenSequences are the ordered runs a person reaches for first. A
// value contained entirely in one of them (either direction) is predictable
// regardless of where the digits-to-letters boundary falls.
var trivialTokenSequences = []string{
	"0123456789abcdefghijklmnopqrstuvwxyz",
	"abcdefghijklmnopqrstuvwxyz0123456789",
	"zyxwvutsrqponmlkjihgfedcba9876543210",
	"9876543210zyxwvutsrqponmlkjihgfedcba",
}

// isTrivialTokenSequence reports a value that is one contiguous run of a trivial
// ordered sequence, such as "0123456789abcdef..." or "zyxwvuts...".
func isTrivialTokenSequence(value string) bool {
	for _, sequence := range trivialTokenSequences {
		if strings.Contains(sequence, value) {
			return true
		}
	}
	return false
}

// hasRepeatingTokenMotif reports a value made only of one motif repeated, e.g.
// "abababab...". maxPeriod bounds how long that motif may be.
func hasRepeatingTokenMotif(value string, maxPeriod int) bool {
	for period := 1; period <= maxPeriod && period <= len(value); period++ {
		if len(value)%period != 0 {
			continue
		}
		repeats := true
		for i := period; i < len(value); i++ {
			if value[i] != value[i-period] {
				repeats = false
				break
			}
		}
		if repeats {
			return true
		}
	}
	return false
}

// hasTrivialTokenKeyPrefix reports a value that starts with a common weak word
// and continues only with digits, e.g. "password1234".
func hasTrivialTokenKeyPrefix(value string) bool {
	for _, word := range []string{"password", "secret", "changeme", "letmein", "qwerty"} {
		rest, ok := strings.CutPrefix(value, word)
		if !ok {
			continue
		}
		if rest == "" || isDigitsOnly(rest) {
			return true
		}
	}
	return false
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return true
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func (token *Token) IsModelLimitsEnabled() bool {
	return token.ModelLimitsEnabled
}

func (token *Token) GetModelLimits() []string {
	if token.ModelLimits == "" {
		return []string{}
	}
	return strings.Split(token.ModelLimits, ",")
}

func (token *Token) GetModelLimitsMap() map[string]bool {
	limits := token.GetModelLimits()
	limitsMap := make(map[string]bool)
	for _, limit := range limits {
		limitsMap[limit] = true
	}
	return limitsMap
}

func DisableModelLimits(tokenId int) error {
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	token.ModelLimitsEnabled = false
	token.ModelLimits = ""
	return token.Update()
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return common.NewMessage("Token ID or user ID is empty")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(tokenId int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			// 守卫式增量：哈希不存在时跳过，由下次读取从数据库水合，
			// 绝不创建只有配额字段的残缺哈希。
			if _, err := cacheApplyTokenQuotaDelta(tokenId, key, int64(quota)); err != nil {
				common.SysLog(common.LogText("failed to increase token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, tokenId, quota)
		return nil
	}
	return increaseTokenQuota(tokenId, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

func DecreaseTokenQuota(id int, key string, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota cannot be negative")
	}
	if common.RedisEnabled {
		gopool.Go(func() {
			if _, err := cacheApplyTokenQuotaDelta(id, key, int64(-quota)); err != nil {
				common.SysLog(common.LogText("failed to decrease token quota: %s", err.Error()))
			}
		})
	}
	if common.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, -quota)
		return nil
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]any{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		},
	).Error
	return err
}

// CountUserTokens returns total number of tokens for the given user, used for pagination
func CountUserTokens(userId int) (int64, error) {
	var total int64
	err := DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	return total, err
}

// BatchDeleteTokens 删除指定用户的一组令牌，返回成功删除数量
func BatchDeleteTokens(ids []int, userId int) (int, error) {
	if len(ids) == 0 {
		return 0, common.NewMessage("Token IDs cannot be empty")
	}

	tx := DB.Begin()

	var tokens []Token
	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Find(&tokens).Error; err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := invalidateTokensCache(tokens); err != nil {
		common.SysLog(common.LogText("failed to invalidate token cache before batch delete: %s", err.Error()))
	}

	if err := tx.Where("user_id = ? AND id IN (?)", userId, ids).Delete(&Token{}).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}

	return len(tokens), nil
}

func GetTokenKeysByIds(ids []int, userId int) ([]Token, error) {
	var tokens []Token
	err := DB.Select("id", commonKeyCol).
		Where("user_id = ? AND id IN (?)", userId, ids).
		Find(&tokens).Error
	return tokens, err
}

// InvalidateUserTokensCache 清理指定用户所有令牌在 Redis 中的缓存，
// 配合 InvalidateUserCache 使用，可在用户被禁用/删除时立即阻断其令牌的请求。
// 下一次请求将从数据库重新加载令牌及用户状态，从而立即识别出被禁用的用户。
func InvalidateUserTokensCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return errors.New("invalid user id")
	}
	var tokens []Token
	if err := DB.Unscoped().
		Select("id", commonKeyCol).
		Where("user_id = ?", userId).
		Find(&tokens).Error; err != nil {
		return err
	}
	return invalidateTokensCache(tokens)
}

func invalidateTokensCache(tokens []Token) error {
	if !common.RedisEnabled {
		return nil
	}
	var firstErr error
	for _, t := range tokens {
		if t.Key == "" {
			continue
		}
		if err := invalidateTokenCacheForMutation(t.Key); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
