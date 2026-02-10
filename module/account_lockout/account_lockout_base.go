package account_lockout

import (
	"fmt"
	"time"

	"github.com/donnyhardyanto/dxlib/databases"
	dxlibModule "github.com/donnyhardyanto/dxlib/module"
	"github.com/donnyhardyanto/dxlib/redis"
	"github.com/donnyhardyanto/dxlib/tables"
	"github.com/donnyhardyanto/dxlib/utils"
)

const (
	LockoutTypeAutoUnlock   = "AUTO_UNLOCK"
	LockoutTypeAdminUnlock  = "ADMIN_UNLOCK"
	LockoutTypeProgressive  = "PROGRESSIVE"

	EventTypeFailedAttempt        = "FAILED_ATTEMPT"
	EventTypeAccountLocked        = "ACCOUNT_LOCKED"
	EventTypeAccountUnlockedAuto  = "ACCOUNT_UNLOCKED_AUTO"
	EventTypeAccountUnlockedAdmin = "ACCOUNT_UNLOCKED_ADMIN"

	AttemptTypePassword = "password"
	AttemptTypeLDAP     = "ldap"

	RedisFailModeFailOpen   = "FAIL_OPEN"
	RedisFailModeFailClosed = "FAIL_CLOSED"
)

type DXMAccountLockout struct {
	dxlibModule.DXModule

	// Configuration
	Config *AccountLockoutConfig

	// Redis (hot path - active lockouts)
	Redis *redis.DXRedis

	// Database (cold path - audit trail)
	AuditLogDB *databases.DXDatabase
	ConfigDB   *databases.DXDatabase

	// Tables
	AccountLockoutEvents *tables.DXRawTable
	AccountLockoutConfig *tables.DXTable

	// Circuit Breaker
	CircuitBreaker *CircuitBreaker

	// Async writer queue
	asyncWriteQueue chan *LockoutEvent
}

type AccountLockoutConfig struct {
	// Core
	Enabled                bool
	MaxFailedAttempts      int
	LockoutDurationMinutes int
	LockoutType            string

	// Failure Handling
	RedisFailMode           string
	CircuitBreakerEnabled   bool
	CircuitBreakerThreshold int
	CircuitBreakerTimeout   int

	// Audit Logging
	LogFailedAttempts    bool
	LogSuccessfulLogins  bool
	AsyncDBWrites        bool
	BatchSize            int
	FlushIntervalSeconds int

	// Tracking
	TrackLDAPFailures     bool
	TrackPasswordFailures bool
	ResetCounterOnSuccess bool
	GracePeriodMinutes    int

	// Progressive
	ProgressiveEnabled    bool
	ProgressiveMultiplier int
	ProgressiveMaxDuration int
}

type LockoutEvent struct {
	EventType              string
	EventTimestamp         string
	UserID                 int64
	UserUID                string
	UserLoginID            string
	OrganizationID         int64
	OrganizationUID        string
	LockoutReason          string
	FailedAttemptsCount    int
	LockoutDurationSeconds int
	LockedAt               string
	UnlockAt               string
	UnlockedByUserID       int64
	UnlockedByUserUID      string
	UnlockReason           string
	AttemptType            string
	AttemptIPAddress       string
	AttemptUserAgent       string
	AttemptAuthSource      string
	Metadata               utils.JSON
}

var ModuleAccountLockout DXMAccountLockout

func (al *DXMAccountLockout) Init(
	auditLogDBNameId string,
	configDBNameId string,
) {
	al.DatabaseNameId = auditLogDBNameId

	// Get connections from managers (set up in internal.go)
	al.Redis = redis.Manager.Redises["account_lockout"]
	al.AuditLogDB = databases.Manager.Databases[auditLogDBNameId]
	al.ConfigDB = databases.Manager.Databases[configDBNameId]

	// Initialize tables
	al.AccountLockoutEvents = tables.NewDXRawTableSimple(
		auditLogDBNameId,
		"partner_auditlog.account_lockout_events",
		"partner_auditlog.account_lockout_events",
		"partner_auditlog.account_lockout_events",
		"id", "uid", "", "metadata",
		nil,
		[][]string{{"user_id"}, {"user_uid"}, {"event_timestamp"}},
		[]string{"user_id", "user_uid", "user_loginid", "event_type", "event_timestamp"},
		[]string{"id", "uid", "event_type", "event_timestamp", "user_id", "user_uid",
			"user_loginid", "organization_id", "lockout_reason", "failed_attempts_count",
			"locked_at", "unlock_at", "attempt_type", "attempt_ip_address"},
		[]string{"id", "uid", "user_id", "user_uid", "organization_id", "event_type", "attempt_type", "event_timestamp", "locked_at", "unlock_at", "created_at"},
	)

	al.AccountLockoutConfig = tables.NewDXTableSimple(
		configDBNameId,
		"partner_config.account_lockout_config",
		"partner_config.account_lockout_config",
		"partner_config.account_lockout_config",
		"id", "uid", "", "data",
		nil,
		[][]string{{"organization_id"}},
		[]string{"organization_id", "max_failed_attempts", "lockout_duration_minutes", "is_deleted"},
		[]string{"id", "uid", "organization_id", "max_failed_attempts",
			"lockout_duration_minutes", "lockout_type", "is_active"},
		[]string{"id", "uid", "organization_id", "lockout_type", "is_active", "created_at", "last_modified_at", "is_deleted"},
	)

	// Initialize async write queue if enabled
	if al.Config.AsyncDBWrites {
		al.asyncWriteQueue = make(chan *LockoutEvent, al.Config.BatchSize*2)
		go al.asyncDBWriter()
	}
}

// asyncDBWriter runs in background goroutine
func (al *DXMAccountLockout) asyncDBWriter() {
	ticker := time.NewTicker(time.Duration(al.Config.FlushIntervalSeconds) * time.Second)
	defer ticker.Stop()

	batch := make([]*LockoutEvent, 0, al.Config.BatchSize)

	for {
		select {
		case event := <-al.asyncWriteQueue:
			batch = append(batch, event)

			// Flush if batch is full
			if len(batch) >= al.Config.BatchSize {
				al.flushBatch(batch)
				batch = make([]*LockoutEvent, 0, al.Config.BatchSize)
			}

		case <-ticker.C:
			// Periodic flush
			if len(batch) > 0 {
				al.flushBatch(batch)
				batch = make([]*LockoutEvent, 0, al.Config.BatchSize)
			}
		}
	}
}

// flushBatch writes a batch of events to database
func (al *DXMAccountLockout) flushBatch(batch []*LockoutEvent) {
	for _, event := range batch {
		al.writeAuditLogSync(event)
	}
	fmt.Printf("Flushed %d audit log events to database\n", len(batch))
}
