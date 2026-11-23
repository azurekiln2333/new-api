package model

import (
	"context"
	"errors"
	"sync"
	"time"

	clickhouseclient "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/MoeForge/moego/ojson"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const (
	requestContentPhaseReady         = "ready"
	requestContentPhaseWritePending  = "write_pending"
	requestContentPhaseLinkPending   = "link_pending"
	requestContentPhaseDeletePending = "delete_pending"
	requestContentPhaseRetired       = "retired"

	requestContentLeaseSeconds  = 300
	requestContentRecoveryGrace = 300
	requestContentCleanupBatch  = 100
	requestContentWriteTimeout  = 2 * time.Second
	requestContentReadTimeout   = 5 * time.Second
)

// LogRequestPayload stores UTF-8 JSON. MySQL TEXT cannot hold 256 KiB.
type LogRequestPayload string

func (LogRequestPayload) GormDataType() string { return "text" }

func (LogRequestPayload) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db.Dialector.Name() == "mysql" {
		return "longtext"
	}
	return "text"
}

// LogRequestDetail is one immutable, redacted original request snapshot in LOG_DB.
type LogRequestDetail struct {
	Ref            string            `json:"ref" gorm:"type:varchar(64);primaryKey"`
	RequestID      string            `json:"request_id" gorm:"type:varchar(64);index"`
	UserID         int               `json:"-"`
	SubmitNodeName string            `json:"submit_node_name" gorm:"type:varchar(255)"`
	CreatedAt      int64             `json:"-" gorm:"index"`
	Status         string            `json:"status" gorm:"type:varchar(32)"`
	CapturedBytes  int               `json:"captured_bytes"`
	Truncated      bool              `json:"truncated"`
	Payload        LogRequestPayload `json:"-"`
}

func (d LogRequestDetail) MarshalJSON() ([]byte, error) {
	type metadata LogRequestDetail
	payload := common.RawMessage(d.Payload)
	if !ojson.ValidBytes(payload) || common.GetJsonType(payload) != "object" {
		payload = common.RawMessage("null")
	}
	return common.Marshal(struct {
		metadata
		Payload common.RawMessage `json:"payload"`
	}{metadata: metadata(d), Payload: payload})
}

// LogRequestContentProtection lives in the primary DB. It coordinates links
// and cleanup even when logs use a different database. It never holds prompts.
type LogRequestContentProtection struct {
	Ref          string `gorm:"type:varchar(64);primaryKey"`
	RequestID    string `gorm:"type:varchar(64)"`
	UserID       int
	CreatedAt    int64
	HolderID     string `gorm:"type:varchar(64)"`
	Generation   int64
	Phase        string `gorm:"type:varchar(32);index"`
	LeaseUntil   int64  `gorm:"index"`
	WriterID     string `gorm:"type:varchar(64)"`
	WriterUntil  int64
	CheckedAt    int64  `gorm:"index"`
	TaskID       string `gorm:"type:varchar(191)"`
	TaskPlatform string `gorm:"type:varchar(30)"`
}

var (
	errRequestContentPending = errors.New("request content operation is pending recovery")
	errRequestContentNoStore = errors.New("request log database is unavailable")
	errRequestContentStorage = errors.New("request content storage failed")
)

// MigrateLogRequestDetails initializes the payload and coordination stores.
func MigrateLogRequestDetails() error {
	if DB == nil || LOG_DB == nil {
		return errRequestContentNoStore
	}
	if err := DB.AutoMigrate(&LogRequestContentProtection{}); err != nil {
		return err
	}
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return LOG_DB.Exec(`CREATE TABLE IF NOT EXISTS log_request_details (
			ref String, request_id String, user_id Int64, submit_node_name String,
			created_at Int64, status String, captured_bytes Int64,
			truncated UInt8, payload String
		) ENGINE = MergeTree()
		PARTITION BY toYYYYMM(toDateTime(created_at))
		ORDER BY (created_at, ref)`).Error
	}
	return LOG_DB.AutoMigrate(&LogRequestDetail{})
}

// Never print SQL or bound payloads, even under DB.Debug.
func requestContentStore(db *gorm.DB, ctx context.Context) *gorm.DB {
	if db == LOG_DB && common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		// A successful insert must be visible before releasing its fence.
		ctx = clickhouseclient.Context(ctx, clickhouseclient.WithSettings(clickhouseclient.Settings{"async_insert": 0}))
	}
	return db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Discard})
}

func requestContentSameStore() bool {
	if DB == LOG_DB {
		return true
	}
	mainSQL, mainErr := DB.DB()
	logSQL, logErr := LOG_DB.DB()
	return mainErr == nil && logErr == nil && mainSQL == logSQL
}

func requestContentShouldExist(snapshot *common.RequestContentSnapshot) bool {
	return snapshot != nil && (snapshot.Status == common.RequestContentStatusRecorded || snapshot.Status == common.RequestContentStatusTruncated)
}

// ValidLogRequestContentRef accepts only the server-generated opaque ID format.
func ValidLogRequestContentRef(ref string) bool {
	if len(ref) != 32 {
		return false
	}
	for _, value := range ref {
		if (value < '0' || value > '9') && (value < 'a' || value > 'f') {
			return false
		}
	}
	return true
}

func findRequestContentDetail(db *gorm.DB, ref string) *gorm.DB {
	return db.Where("ref = ?", ref).Order("created_at")
}

// ensureRequestContentDetail runs with the coordination row locked. A metadata-
// only caller may link an existing detail but cannot create a blank one.
func ensureRequestContentDetail(db *gorm.DB, snapshot *common.RequestContentSnapshot) error {
	var existing LogRequestDetail
	err := findRequestContentDetail(db, snapshot.Ref).Select("request_id", "user_id").Take(&existing).Error
	switch {
	case err == nil:
		if existing.RequestID != snapshot.RequestID || existing.UserID != snapshot.UserID {
			return errRequestContentStorage
		}
		return nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return err
	}
	if len(snapshot.Payload) == 0 || len(snapshot.Payload) > common.RequestContentMaxMaxBytes ||
		!ojson.ValidBytes(snapshot.Payload) || common.GetJsonType(snapshot.Payload) != "object" {
		return errRequestContentStorage
	}
	return db.Create(&LogRequestDetail{
		Ref: snapshot.Ref, RequestID: snapshot.RequestID, UserID: snapshot.UserID,
		SubmitNodeName: snapshot.SubmitNodeName, CreatedAt: snapshot.CreatedAt,
		Status: snapshot.Status, CapturedBytes: len(snapshot.Payload),
		Truncated: snapshot.Truncated, Payload: LogRequestPayload(snapshot.Payload),
	}).Error
}

func lockRequestContentProtection(tx *gorm.DB, ref string) (*LogRequestContentProtection, error) {
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		// Acquire the writer lock before establishing SQLite's read snapshot.
		if err := tx.Model(&LogRequestContentProtection{}).Where("ref = ?", ref).
			UpdateColumn("generation", gorm.Expr("generation")).Error; err != nil {
			return nil, err
		}
	}
	var protection LogRequestContentProtection
	if err := lockForUpdate(tx).Where("ref = ?", ref).Take(&protection).Error; err != nil {
		return nil, err
	}
	return &protection, nil
}

func lockRequestContentForWrite(tx *gorm.DB, snapshot *common.RequestContentSnapshot) (*LogRequestContentProtection, error) {
	if len(snapshot.Payload) != 0 {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&LogRequestContentProtection{
			Ref: snapshot.Ref, RequestID: snapshot.RequestID, UserID: snapshot.UserID,
			CreatedAt: snapshot.CreatedAt, Phase: requestContentPhaseReady,
		}).Error; err != nil {
			return nil, err
		}
	}
	locked, err := lockRequestContentProtection(tx, snapshot.Ref)
	if err != nil {
		return nil, err
	}
	if locked.RequestID != snapshot.RequestID || locked.UserID != snapshot.UserID {
		return nil, errRequestContentStorage
	}
	if locked.Phase != requestContentPhaseReady || locked.LeaseUntil > common.GetTimestamp() {
		return nil, errRequestContentPending
	}
	return locked, nil
}

// markRequestContentPending commits the intent before any separate-store I/O.
// An expired pending operation is never taken over by an ordinary writer.
func markRequestContentPending(tx *gorm.DB, locked *LogRequestContentProtection, phase string) error {
	locked.HolderID = common.GetUUID()
	locked.Generation++
	locked.Phase = phase
	locked.LeaseUntil = common.GetTimestamp() + requestContentLeaseSeconds
	return tx.Model(locked).Updates(map[string]any{
		"holder_id": locked.HolderID, "generation": locked.Generation,
		"phase": locked.Phase, "lease_until": locked.LeaseUntil,
	}).Error
}

func sameRequestContentOperation(a, b *LogRequestContentProtection) bool {
	return a.Phase == b.Phase && a.Generation == b.Generation && a.HolderID == b.HolderID
}

type requestContentLinkKey struct{}

// requestContentLogLink carries only small provenance. LinkID distinguishes a
// possibly committed external write from earlier logs sharing this snapshot.
type requestContentLogLink struct {
	common.RequestContentMetadata
	LinkID string `json:"link_id,omitempty"`
}

type requestContentWriterKey struct{}

type requestContentWriter struct {
	id     string
	mu     sync.Mutex
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

// TrackRequestContentWrites keeps an ingress ref live between linked logs.
// No renewal work is started until a detail and its first link have committed.
func TrackRequestContentWrites(c *gin.Context) func() {
	writer := &requestContentWriter{id: common.GetUUID()}
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestContentWriterKey{}, writer))
	return writer.close
}

func (w *requestContentWriter) close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		if w.stop != nil {
			close(w.stop)
		}
	}
	done := w.done
	w.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (w *requestContentWriter) start(db *gorm.DB, ref string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.done != nil {
		return
	}
	w.stop, w.done = make(chan struct{}), make(chan struct{})
	go w.keepLease(db, ref)
}

func (w *requestContentWriter) keepLease(db *gorm.DB, ref string) {
	defer close(w.done)
	ticker := time.NewTicker(time.Duration(requestContentLeaseSeconds/3) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			ctx, cancel := context.WithTimeout(context.Background(), requestContentWriteTimeout)
			_ = requestContentStore(db, ctx).Model(&LogRequestContentProtection{}).
				Where("ref = ? AND writer_id = ?", ref, w.id).
				Updates(map[string]any{"writer_id": "", "writer_until": 0}).Error
			cancel()
			return
		case <-ticker.C:
			now := common.GetTimestamp()
			ctx, cancel := context.WithTimeout(context.Background(), requestContentWriteTimeout)
			_ = requestContentStore(db, ctx).Model(&LogRequestContentProtection{}).
				Where("ref = ? AND writer_id = ? AND writer_until > ?", ref, w.id, now).
				Update("writer_until", now+requestContentLeaseSeconds).Error
			cancel()
		}
	}
}

func readyRequestContentProtection(tx *gorm.DB, locked *LogRequestContentProtection, task *Task, writer *requestContentWriter) error {
	updates := map[string]any{"phase": requestContentPhaseReady, "holder_id": "", "lease_until": 0}
	if writer != nil && (locked.WriterID == writer.id || locked.WriterUntil <= common.GetTimestamp()) {
		updates["writer_id"] = writer.id
		updates["writer_until"] = common.GetTimestamp() + requestContentLeaseSeconds
	}
	if task != nil {
		updates["task_id"], updates["task_platform"] = task.TaskID, string(task.Platform)
	}
	return tx.Model(locked).Updates(updates).Error
}

// writeProtectedRequestContent calls link once under the protection lock. A
// same-store payload and link commit together. On a separate store a failed or
// uncertain operation leaves its previous committed pending marker intact.
// Only a failure before link is attempted can fall back to an ordinary insert.
func writeProtectedRequestContent(ctx context.Context, snapshot *common.RequestContentSnapshot, task *Task, link func(primary, logs *gorm.DB) error) (attempted bool, err error) {
	if DB == nil || LOG_DB == nil || !ValidLogRequestContentRef(snapshot.Ref) || snapshot.RequestID == "" || snapshot.UserID <= 0 {
		return false, errRequestContentStorage
	}
	baseContext := ctx
	ctx, cancel := context.WithTimeout(ctx, requestContentWriteTimeout)
	defer cancel()
	writer, _ := ctx.Value(requestContentWriterKey{}).(*requestContentWriter)
	primary := requestContentStore(DB, ctx)
	sameStore := requestContentSameStore()
	var pending *LogRequestContentProtection
	if !sameStore {
		if err := primary.Transaction(func(tx *gorm.DB) error {
			var err error
			pending, err = lockRequestContentForWrite(tx, snapshot)
			if err != nil {
				return err
			}
			return markRequestContentPending(tx, pending, requestContentPhaseWritePending)
		}); err != nil {
			return false, err
		}
		if task == nil {
			// Persist the distinction between an uncertain payload write (no log
			// could reference it yet) and an uncertain external log insert.
			if err := primary.Transaction(func(tx *gorm.DB) error {
				locked, err := lockRequestContentProtection(tx, snapshot.Ref)
				if err != nil || !sameRequestContentOperation(locked, pending) {
					return errRequestContentPending
				}
				if err := ensureRequestContentDetail(requestContentStore(LOG_DB, ctx), snapshot); err != nil {
					return err
				}
				if err := markRequestContentPending(tx, locked, requestContentPhaseLinkPending); err != nil {
					return err
				}
				pending = locked
				return nil
			}); err != nil {
				return false, err
			}
		}
	}
	if task != nil {
		// Bound diagnostic checks, not the accepted task's normal insert and
		// commit. They still use the original caller context.
		primary = requestContentStore(DB, baseContext)
	}
	err = primary.Transaction(func(tx *gorm.DB) error {
		work := tx.WithContext(ctx)
		var locked *LogRequestContentProtection
		var err error
		logs := work
		if sameStore {
			locked, err = lockRequestContentForWrite(work, snapshot)
		} else {
			locked, err = lockRequestContentProtection(work, snapshot.Ref)
			logCtx := ctx
			if pending.Phase == requestContentPhaseLinkPending {
				logCtx = context.WithValue(ctx, requestContentLinkKey{}, pending.HolderID)
			}
			logs = requestContentStore(LOG_DB, logCtx)
		}
		if err != nil {
			return err
		}
		if pending != nil && !sameRequestContentOperation(locked, pending) {
			return errRequestContentPending
		}
		if sameStore || task != nil {
			if err := ensureRequestContentDetail(logs, snapshot); err != nil {
				return err
			}
		}
		// Ready is invisible until the transaction commits, after link. Put all
		// coordination work before the authoritative task insert.
		if err := readyRequestContentProtection(work, locked, task, writer); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		attempted = true
		return link(tx, logs)
	})
	if err == nil && writer != nil {
		writer.start(DB, snapshot.Ref)
	}
	if !attempted && pending != nil && pending.Phase == requestContentPhaseLinkPending {
		// The link callback was not called. This operation can safely be
		// recovered as payload-only, even when its last transaction failed.
		resetCtx, resetCancel := context.WithTimeout(baseContext, requestContentWriteTimeout)
		_ = requestContentStore(DB, resetCtx).Model(&LogRequestContentProtection{}).
			Where("ref = ? AND phase = ? AND holder_id = ? AND generation = ?", pending.Ref, pending.Phase, pending.HolderID, pending.Generation).
			Update("phase", requestContentPhaseWritePending).Error
		resetCancel()
	}
	return attempted, err
}

// WithLogRequestDetail appends a log using a separate detail row when possible.
// An unavailable detail must not suppress the ordinary log.
func WithLogRequestDetail(ctx context.Context, snapshot *common.RequestContentSnapshot, writeLog func(*gorm.DB, error) error) error {
	if LOG_DB == nil {
		return errRequestContentNoStore
	}
	var detailErr error
	if requestContentShouldExist(snapshot) {
		attempted, err := writeProtectedRequestContent(ctx, snapshot, nil, func(_, logs *gorm.DB) error {
			return writeLog(logs, nil)
		})
		if attempted || err == nil {
			return err
		}
		detailErr = errRequestContentStorage
	}
	fallbackCtx, cancel := context.WithTimeout(ctx, requestContentWriteTimeout)
	defer cancel()
	return writeLog(requestContentStore(LOG_DB, fallbackCtx), detailErr)
}

// InsertTaskWithRequestContent persists the accepted task exactly once. If
// diagnostics fail before the task insert, the fallback inserts the task with
// storage_failed already in its private metadata. A task/commit error is not
// success and must not trigger a second insert with an uncertain outcome.
func InsertTaskWithRequestContent(ctx context.Context, task *Task, snapshot *common.RequestContentSnapshot, omitColumns ...string) error {
	_, err := InsertTaskWithSubmissionCharge(ctx, task, snapshot, omitColumns, nil)
	return err
}

// InsertTaskWithSubmissionCharge is InsertTaskWithRequestContent with the
// submission charge committed inside the same transaction as the task row. The
// existing request-content lease/fence/link sequence is preserved: the charge
// runs in the task insert callback, after the content link is ready. A nil
// submit keeps the plain insert path. It returns a nil result when the task
// carried no submission charge (the caller keeps its own settlement path).
func InsertTaskWithSubmissionCharge(ctx context.Context, task *Task, snapshot *common.RequestContentSnapshot,
	omitColumns []string, submit *TaskBudgetSubmit) (*TaskBudgetInsertResult, error) {
	if task == nil {
		return nil, errors.New("task is required")
	}
	var committed *TaskBudgetInsertResult
	insert := func(primary *gorm.DB) error {
		if submit == nil || task.PrivateData.BudgetReservationId <= 0 {
			return task.insertTx(primary, omitColumns...)
		}
		result, err := InsertTaskWithBudgetSubmit(primary, task, omitColumns, *submit)
		committed = result
		return err
	}
	if snapshot == nil {
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return insert(tx) })
		return committed, err
	}
	provenance := &TaskRequestContent{
		RequestID: snapshot.RequestID, SubmitNodeName: snapshot.SubmitNodeName,
		Status: snapshot.Status, CapturedBytes: snapshot.CapturedBytes, Truncated: snapshot.Truncated,
	}
	task.PrivateData.RequestContent = provenance
	if requestContentShouldExist(snapshot) {
		if task.UserId == snapshot.UserID && task.TaskID != "" {
			provenance.Ref = snapshot.Ref
			attempted, err := writeProtectedRequestContent(ctx, snapshot, task, func(primary, _ *gorm.DB) error {
				return insert(primary)
			})
			if attempted || err == nil {
				return committed, err
			}
		}
		// The detail store could not take this payload, so the task is inserted
		// without it and the metadata must say the storage attempt failed.
		provenance.Ref = ""
		provenance.Status = common.RequestContentStatusStorageFail
	}
	// A snapshot that never needed storage (or belongs to another user) keeps its
	// own diagnostics status; nothing failed here.
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return insert(tx) })
	return committed, err
}

func hasLogRequestDetailReference(db *gorm.DB, ref, requestID string, userID int, linkID string) (bool, error) {
	if requestID == "" || userID <= 0 {
		return false, nil
	}
	// A server request ID narrows this scan to one request's small set of
	// attempts/settlements. Compare the metadata ref and exact request ID, even
	// on a case-insensitive SQL collation.
	rows, err := db.Model(&Log{}).Select("request_id", "other").
		Where("request_id = ? AND user_id = ?", requestID, userID).
		Where("type IN ?", []int{LogTypeConsume, LogTypeError, LogTypeRefund}).Rows()
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil {
			return false, err
		}
		if id != requestID {
			continue
		}
		var other struct {
			AdminInfo struct {
				RequestContent requestContentLogLink `json:"request_content"`
			} `json:"admin_info"`
		}
		if common.UnmarshalJsonStr(value, &other) == nil {
			link := other.AdminInfo.RequestContent
			if link.Ref == ref && (linkID == "" || link.LinkID == linkID) {
				return true, nil
			}
		}
	}
	return false, rows.Err()
}

// GetLogRequestDetail returns content only with a surviving usage-log link.
// Route-level admin/root and PAT log:read checks are still required.
func GetLogRequestDetail(ctx context.Context, ref string) (*LogRequestDetail, error) {
	if LOG_DB == nil || !ValidLogRequestContentRef(ref) {
		return nil, gorm.ErrRecordNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, requestContentReadTimeout)
	defer cancel()
	db := requestContentStore(LOG_DB, ctx)
	var detail LogRequestDetail
	if err := findRequestContentDetail(db, ref).Take(&detail).Error; err != nil {
		return nil, err
	}
	if detail.Ref != ref || (detail.Status != common.RequestContentStatusRecorded && detail.Status != common.RequestContentStatusTruncated) {
		return nil, gorm.ErrRecordNotFound
	}
	linked, err := hasLogRequestDetailReference(db, detail.Ref, detail.RequestID, detail.UserID, "")
	if err != nil {
		return nil, errors.New("request content lookup failed")
	}
	if !linked {
		return nil, gorm.ErrRecordNotFound
	}
	return &detail, nil
}

func hasActiveTaskContentReference(tx *gorm.DB, protection *LogRequestContentProtection) (bool, error) {
	if protection.TaskID == "" {
		return false, nil
	}
	var tasks []Task
	if err := tx.Select("status", "quota", "updated_at", "private_data").
		Where("task_id = ? AND platform = ? AND user_id = ?", protection.TaskID, protection.TaskPlatform, protection.UserID).
		Find(&tasks).Error; err != nil {
		return false, err
	}
	for _, task := range tasks {
		content := task.PrivateData.RequestContent
		if content == nil || content.Ref != protection.Ref || content.RequestID != protection.RequestID {
			continue
		}
		// The terminal-status CAS occurs before the final billing log. Keep
		// the ref over that handoff, and keep failed tasks awaiting a refund.
		if task.PrivateData.BudgetPending || (task.Status != TaskStatusSuccess && task.Status != TaskStatusFailure) ||
			task.UpdatedAt > common.GetTimestamp()-requestContentLeaseSeconds ||
			(task.Status == TaskStatusFailure && task.Quota > 0) {
			return true, nil
		}
	}
	return false, nil
}

func deleteRequestContentDetail(db *gorm.DB, ref string) error {
	if common.UsingLogDatabase(common.DatabaseTypeClickHouse) {
		return db.Exec("ALTER TABLE log_request_details DELETE WHERE ref = ? SETTINGS mutations_sync = 1", ref).Error
	}
	return db.Where("ref = ?", ref).Delete(&LogRequestDetail{}).Error
}

// CleanupLogRequestDetails inspects a bounded, rotating set of candidates. A
// still-linked old record cannot starve newer orphan records on every sweep.
func CleanupLogRequestDetails(ctx context.Context, cutoff int64, limit int) error {
	if LOG_DB == nil || DB == nil {
		return errRequestContentNoStore
	}
	var candidates []LogRequestContentProtection
	if err := requestContentStore(DB, ctx).
		Where("phase IN ? AND writer_until <= ? AND created_at < ?", []string{requestContentPhaseReady, requestContentPhaseRetired}, common.GetTimestamp(), cutoff).
		Order("checked_at, ref").Limit(requestContentSweepLimit(limit)).Find(&candidates).Error; err != nil {
		return errRequestContentStorage
	}
	return sweepRequestContent(ctx, candidates, false)
}

// RecoverLogRequestContentOperations rechecks abandoned operations under the
// same lock and fence as writers. A lease is an eligibility check, not proof
// that an uncertain write or delete has completed.
func RecoverLogRequestContentOperations(ctx context.Context, limit int) error {
	if DB == nil || LOG_DB == nil {
		return errRequestContentNoStore
	}
	var candidates []LogRequestContentProtection
	if err := requestContentStore(DB, ctx).
		Where("phase IN ? AND lease_until < ? AND writer_until <= ?", []string{requestContentPhaseWritePending, requestContentPhaseLinkPending, requestContentPhaseDeletePending}, common.GetTimestamp()-requestContentRecoveryGrace, common.GetTimestamp()).
		Order("checked_at, ref").Limit(requestContentSweepLimit(limit)).Find(&candidates).Error; err != nil {
		return errRequestContentStorage
	}
	return sweepRequestContent(ctx, candidates, true)
}

func requestContentSweepLimit(limit int) int {
	if limit <= 0 {
		return requestContentCleanupBatch
	}
	return min(limit, requestContentCleanupBatch)
}

func sweepRequestContent(ctx context.Context, candidates []LogRequestContentProtection, recovering bool) error {
	var errs []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := reclaimRequestContent(ctx, &candidate, recovering); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func reclaimRequestContent(ctx context.Context, candidate *LogRequestContentProtection, recovering bool) error {
	ctx, cancel := context.WithTimeout(ctx, requestContentReadTimeout)
	defer cancel()
	primary := requestContentStore(DB, ctx)
	sameStore := requestContentSameStore()
	var pending *LogRequestContentProtection
	if err := primary.Transaction(func(tx *gorm.DB) error {
		locked, err := lockRequestContentProtection(tx, candidate.Ref)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !sameRequestContentOperation(locked, candidate) || locked.WriterUntil > common.GetTimestamp() {
			return nil
		}
		if recovering && locked.LeaseUntil >= common.GetTimestamp()-requestContentRecoveryGrace {
			return nil
		}
		logs := tx
		if !sameStore {
			logs = requestContentStore(LOG_DB, ctx)
		}
		linkID := ""
		if locked.Phase == requestContentPhaseLinkPending {
			linkID = locked.HolderID
		}
		linked, err := hasLogRequestDetailReference(logs, locked.Ref, locked.RequestID, locked.UserID, linkID)
		if err != nil {
			return err
		}
		if linkID != "" && !linked {
			// Neither absence nor an earlier retry's surviving log proves a
			// timed-out external insert aborted. Keep its fence and rotate.
			return tx.Model(locked).Update("checked_at", time.Now().UnixNano()).Error
		}
		active, err := hasActiveTaskContentReference(tx, locked)
		if err != nil {
			return err
		}
		if linked || active {
			if recovering && (locked.Phase == requestContentPhaseWritePending || locked.Phase == requestContentPhaseLinkPending) {
				// A surviving reference proves the link exists. Confirm the
				// payload identity before releasing the pending fence.
				if err := ensureRequestContentDetail(logs, &common.RequestContentSnapshot{Ref: locked.Ref, RequestID: locked.RequestID, UserID: locked.UserID}); err != nil {
					return err
				}
				return readyRequestContentProtection(tx, locked, nil, nil)
			}
			return tx.Model(locked).Update("checked_at", time.Now().UnixNano()).Error
		}
		if sameStore {
			if err := deleteRequestContentDetail(logs, locked.Ref); err != nil {
				return err
			}
			return tx.Delete(locked).Error
		}
		if locked.Phase == requestContentPhaseRetired {
			// Sweep any late insert from before the uncertain delete. No new
			// writer may link this retired ref.
			var detail LogRequestDetail
			err := findRequestContentDetail(logs, locked.Ref).Select("ref").Take(&detail).Error
			if err == nil {
				err = deleteRequestContentDetail(logs, locked.Ref)
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				err = nil
			}
			if err != nil {
				return err
			}
			return tx.Model(locked).Update("checked_at", time.Now().UnixNano()).Error
		}
		if err := markRequestContentPending(tx, locked, requestContentPhaseDeletePending); err != nil {
			return err
		}
		pending = locked
		return nil
	}); err != nil {
		return errors.New("request content cleanup lookup failed")
	}
	if pending == nil {
		return nil
	}
	if err := primary.Transaction(func(tx *gorm.DB) error {
		locked, err := lockRequestContentProtection(tx, pending.Ref)
		if err != nil || !sameRequestContentOperation(locked, pending) {
			return errRequestContentPending
		}
		if err := deleteRequestContentDetail(requestContentStore(LOG_DB, ctx), locked.Ref); err != nil {
			return err
		}
		if !recovering {
			return tx.Delete(locked).Error
		}
		// Do not permit reinsertion after an uncertain cross-store delete:
		// an earlier server-side mutation may still be in flight. A retired
		// ref keeps only its fence; all content has been removed.
		return tx.Model(locked).Updates(map[string]any{
			"phase": requestContentPhaseRetired, "holder_id": "", "lease_until": 0,
			"checked_at": time.Now().UnixNano(),
		}).Error
	}); err != nil {
		return errors.New("request content cleanup is pending recovery")
	}
	return nil
}
