package account_lockout

import (
	"fmt"

	"github.com/donnyhardyanto/dxlib/configuration"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

func (al *DXMAccountLockout) LoadConfig() error {
	cfg := &AccountLockoutConfig{}

	// Get main configuration
	mainConfig, exists := configuration.Manager.Configurations["account_lockout"]
	if !exists {
		return fmt.Errorf("account_lockout configuration not found")
	}
	if mainConfig.Data == nil {
		return fmt.Errorf("account_lockout configuration data is nil")
	}

	// Load core configuration
	coreData, err := utils.GetJSONFromKV(*mainConfig.Data, "core")
	if err != nil {
		return fmt.Errorf("account_lockout.core not found: %w", err)
	}
	cfg.Enabled, _ = utils.GetBoolFromKV(coreData, "enabled")
	cfg.MaxFailedAttempts, _ = utils.GetIntFromKV(coreData, "max_failed_attempts")
	cfg.LockoutDurationMinutes, _ = utils.GetIntFromKV(coreData, "lockout_duration_minutes")
	cfg.LockoutType, _ = utils.GetStringFromKV(coreData, "lockout_type")

	// Load failure handling
	failureData, err := utils.GetJSONFromKV(*mainConfig.Data, "failure_handling")
	if err == nil {
		cfg.RedisFailMode, _ = utils.GetStringFromKV(failureData, "redis_fail_mode")
		cfg.CircuitBreakerEnabled, _ = utils.GetBoolFromKV(failureData, "circuit_breaker_enabled")
		cfg.CircuitBreakerThreshold, _ = utils.GetIntFromKV(failureData, "circuit_breaker_threshold")
		cfg.CircuitBreakerTimeout, _ = utils.GetIntFromKV(failureData, "circuit_breaker_timeout_seconds")
	}

	// Load audit logging
	auditData, err := utils.GetJSONFromKV(*mainConfig.Data, "audit_logging")
	if err == nil {
		cfg.LogFailedAttempts, _ = utils.GetBoolFromKV(auditData, "log_failed_attempts")
		cfg.LogSuccessfulLogins, _ = utils.GetBoolFromKV(auditData, "log_successful_logins")
		cfg.AsyncDBWrites, _ = utils.GetBoolFromKV(auditData, "async_db_writes")
		cfg.BatchSize, _ = utils.GetIntFromKV(auditData, "batch_size")
		cfg.FlushIntervalSeconds, _ = utils.GetIntFromKV(auditData, "flush_interval_seconds")
	}

	// Load tracking
	trackingData, err := utils.GetJSONFromKV(*mainConfig.Data, "tracking")
	if err == nil {
		cfg.TrackLDAPFailures, _ = utils.GetBoolFromKV(trackingData, "track_ldap_failures")
		cfg.TrackPasswordFailures, _ = utils.GetBoolFromKV(trackingData, "track_password_failures")
		cfg.ResetCounterOnSuccess, _ = utils.GetBoolFromKV(trackingData, "reset_counter_on_success")
		cfg.GracePeriodMinutes, _ = utils.GetIntFromKV(trackingData, "grace_period_minutes")
	}

	// Load progressive
	progressiveData, err := utils.GetJSONFromKV(*mainConfig.Data, "progressive")
	if err == nil {
		cfg.ProgressiveEnabled, _ = utils.GetBoolFromKV(progressiveData, "enabled")
		cfg.ProgressiveMultiplier, _ = utils.GetIntFromKV(progressiveData, "multiplier")
		cfg.ProgressiveMaxDuration, _ = utils.GetIntFromKV(progressiveData, "max_duration_minutes")
	}

	// Validate configuration
	if err := al.validateConfig(cfg); err != nil {
		return err
	}

	al.Config = cfg
	log.Log.Infof("Account Lockout configuration loaded: enabled=%v, max_attempts=%d, duration=%d min",
		cfg.Enabled, cfg.MaxFailedAttempts, cfg.LockoutDurationMinutes)

	return nil
}

func (al *DXMAccountLockout) validateConfig(cfg *AccountLockoutConfig) error {
	// Validate max attempts
	if cfg.MaxFailedAttempts < 1 || cfg.MaxFailedAttempts > 100 {
		return fmt.Errorf("max_failed_attempts must be between 1 and 100, got %d", cfg.MaxFailedAttempts)
	}

	// Validate duration
	if cfg.LockoutDurationMinutes < 1 || cfg.LockoutDurationMinutes > 10080 {
		return fmt.Errorf("lockout_duration_minutes must be between 1 and 10080 (7 days), got %d", cfg.LockoutDurationMinutes)
	}

	// Validate lockout type
	validTypes := []string{LockoutTypeAutoUnlock, LockoutTypeAdminUnlock, LockoutTypeProgressive}
	if !contains(validTypes, cfg.LockoutType) {
		return fmt.Errorf("invalid lockout_type: %s", cfg.LockoutType)
	}

	// Validate fail mode
	validModes := []string{RedisFailModeFailOpen, RedisFailModeFailClosed}
	if !contains(validModes, cfg.RedisFailMode) {
		return fmt.Errorf("invalid redis_fail_mode: %s", cfg.RedisFailMode)
	}

	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
