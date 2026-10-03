package account_lockout

import (
	"testing"

	"github.com/donnyhardyanto/dxlib/configuration"
	"github.com/donnyhardyanto/dxlib/utils"
)

func setLockoutConfiguration(t *testing.T, data utils.JSON) {
	t.Helper()
	if configuration.Manager.Configurations == nil {
		configuration.Manager.Configurations = map[string]*configuration.DXConfiguration{}
	}
	configuration.Manager.Configurations["account_lockout"] = &configuration.DXConfiguration{Data: &data}
	t.Cleanup(func() { delete(configuration.Manager.Configurations, "account_lockout") })
}

// A configuration that sets only the mandatory core and fail mode must still
// leave the module usable: the lock check records into the circuit breaker on
// every call and Init starts a ticker when async writes are on.
func TestLoadConfigFillsRuntimeDefaults(t *testing.T) {
	setLockoutConfiguration(t, utils.JSON{
		"core": utils.JSON{
			"enabled":                  true,
			"max_failed_attempts":      5,
			"lockout_duration_minutes": 15,
			"lockout_type":             LockoutTypeAutoUnlock,
		},
		"failure_handling": utils.JSON{
			"redis_fail_mode": RedisFailModeKeepUnlock,
		},
		"audit_logging": utils.JSON{
			"async_db_writes": true,
		},
	})

	al := &DXMAccountLockout{}
	if err := al.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	if al.CircuitBreaker == nil {
		t.Fatal("CircuitBreaker is nil")
	}
	if al.Config.FlushIntervalSeconds <= 0 {
		t.Errorf("FlushIntervalSeconds = %d, want > 0", al.Config.FlushIntervalSeconds)
	}
	if al.Config.BatchSize <= 0 {
		t.Errorf("BatchSize = %d, want > 0", al.Config.BatchSize)
	}
	if al.Config.CircuitBreakerThreshold <= 0 || al.Config.CircuitBreakerTimeout <= 0 {
		t.Errorf("circuit breaker threshold/timeout = %d/%d, want > 0",
			al.Config.CircuitBreakerThreshold, al.Config.CircuitBreakerTimeout)
	}

	// Must not panic on the paths CheckLockStatusRedis takes.
	al.CircuitBreaker.RecordFailure()
	al.CircuitBreaker.RecordSuccess()
}
