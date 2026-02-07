package account_lockout

import (
	"fmt"
	"strconv"
	"time"

	"github.com/donnyhardyanto/dxlib/log"
)

const (
	redisKeyPrefix     = "pgn:partner:lockout"
	redisKeyCounter    = "counter"
	redisKeyLocked     = "locked"
	redisKeyAttempts   = "attempts"
	redisKeyStatistics = "stats:daily"
)

// Redis key patterns:
// pgn:partner:lockout:counter:{user_id}
// pgn:partner:lockout:locked:{user_id}
// pgn:partner:lockout:attempts:{user_id}
// pgn:partner:lockout:stats:daily:{YYYYMMDD}

func (al *DXMAccountLockout) redisKeyCounterForUser(userID int64) string {
	return fmt.Sprintf("%s:%s:%d", redisKeyPrefix, redisKeyCounter, userID)
}

func (al *DXMAccountLockout) redisKeyLockedForUser(userID int64) string {
	return fmt.Sprintf("%s:%s:%d", redisKeyPrefix, redisKeyLocked, userID)
}

func (al *DXMAccountLockout) redisKeyAttemptsForUser(userID int64) string {
	return fmt.Sprintf("%s:%s:%d", redisKeyPrefix, redisKeyAttempts, userID)
}

func (al *DXMAccountLockout) redisKeyStatsDaily(date string) string {
	return fmt.Sprintf("%s:%s:%s", redisKeyPrefix, redisKeyStatistics, date)
}

// CheckLockStatusRedis checks if user is locked (fast path)
func (al *DXMAccountLockout) CheckLockStatusRedis(userID int64) (isLocked bool, remainingSeconds int64, err error) {
	if !al.Config.Enabled {
		return false, 0, nil
	}

	// Circuit breaker check
	if al.Config.CircuitBreakerEnabled && !al.CircuitBreaker.CanExecute() {
		log.Log.Warnf("Account lockout circuit breaker open, applying fail mode: %s", al.Config.RedisFailMode)
		if al.Config.RedisFailMode == RedisFailModeFailClosed {
			return true, 0, fmt.Errorf("circuit breaker open - fail closed")
		}
		return false, 0, nil // FAIL_OPEN
	}

	key := al.redisKeyLockedForUser(userID)

	// Check if locked key exists
	existsCount, err := al.Redis.Connection.Exists(al.Redis.Context, key).Result()
	if err != nil {
		al.CircuitBreaker.RecordFailure()
		log.Log.Errorf(err, "Redis error checking lock status for user %d", userID)

		// Apply fail mode
		if al.Config.RedisFailMode == RedisFailModeFailClosed {
			return true, 0, err
		}
		return false, 0, nil // FAIL_OPEN - allow login
	}

	al.CircuitBreaker.RecordSuccess()

	exists := existsCount > 0
	if !exists {
		return false, 0, nil
	}

	// Get TTL (remaining time)
	ttlDuration, err := al.Redis.Connection.TTL(al.Redis.Context, key).Result()
	if err != nil {
		log.Log.Errorf(err, "Redis error getting TTL for user %d", userID)
		return true, 0, nil
	}

	ttl := int64(ttlDuration.Seconds())
	if ttl <= 0 {
		// Expired, clean up
		al.Redis.Connection.Del(al.Redis.Context, key)
		return false, 0, nil
	}

	return true, ttl, nil
}

// IncrementFailedAttemptCounterRedis atomically increments the failure counter
func (al *DXMAccountLockout) IncrementFailedAttemptCounterRedis(userID int64, attemptIP string, attemptType string) (count int64, err error) {
	key := al.redisKeyCounterForUser(userID)

	// Atomic increment
	count, err = al.Redis.Connection.HIncrBy(al.Redis.Context, key, "count", 1).Result()
	if err != nil {
		log.Log.Errorf(err, "Redis error incrementing counter for user %d", userID)
		return 0, err
	}

	// Set metadata
	now := time.Now().Unix()
	al.Redis.Connection.HSet(al.Redis.Context, key, "last_attempt_at", fmt.Sprintf("%d", now))
	al.Redis.Connection.HSet(al.Redis.Context, key, "last_attempt_ip", attemptIP)
	al.Redis.Connection.HSet(al.Redis.Context, key, "last_attempt_type", attemptType)

	// Set first attempt timestamp if this is first attempt
	if count == 1 {
		al.Redis.Connection.HSet(al.Redis.Context, key, "first_attempt_at", fmt.Sprintf("%d", now))
	}

	// Set expiry (sliding window - 1 hour)
	al.Redis.Connection.Expire(al.Redis.Context, key, time.Duration(3600)*time.Second)

	return count, nil
}

// LockAccountRedis locks the account in Redis
func (al *DXMAccountLockout) LockAccountRedis(userID int64, failedCount int64, reason string) error {
	key := al.redisKeyLockedForUser(userID)

	now := time.Now()
	unlockAt := now.Add(time.Duration(al.Config.LockoutDurationMinutes) * time.Minute)

	lockData := map[string]interface{}{
		"locked":        "true",
		"locked_at":     now.Unix(),
		"unlock_at":     unlockAt.Unix(),
		"reason":        reason,
		"failed_count":  failedCount,
		"lock_duration": al.Config.LockoutDurationMinutes * 60,
	}

	for field, value := range lockData {
		if err := al.Redis.Connection.HSet(al.Redis.Context, key, field, fmt.Sprintf("%v", value)).Err(); err != nil {
			log.Log.Errorf(err, "Redis error setting lock field %s for user %d", field, userID)
			return err
		}
	}

	// Set TTL for auto-expiry
	ttlSeconds := time.Duration(al.Config.LockoutDurationMinutes) * time.Minute
	if err := al.Redis.Connection.Expire(al.Redis.Context, key, ttlSeconds).Err(); err != nil {
		log.Log.Errorf(err, "Redis error setting lock expiry for user %d", userID)
		return err
	}

	log.Log.Infof("Account locked in Redis: user_id=%d, duration=%d min, unlock_at=%v",
		userID, al.Config.LockoutDurationMinutes, unlockAt)

	return nil
}

// UnlockAccountRedis removes lock from Redis
func (al *DXMAccountLockout) UnlockAccountRedis(userID int64) error {
	keyLocked := al.redisKeyLockedForUser(userID)
	keyCounter := al.redisKeyCounterForUser(userID)
	keyAttempts := al.redisKeyAttemptsForUser(userID)

	// Delete all lock-related keys
	if err := al.Redis.Connection.Del(al.Redis.Context, keyLocked).Err(); err != nil {
		log.Log.Errorf(err, "Redis error deleting lock key for user %d", userID)
		return err
	}

	if err := al.Redis.Connection.Del(al.Redis.Context, keyCounter).Err(); err != nil {
		log.Log.Errorf(err, "Redis error deleting counter key for user %d", userID)
	}

	if err := al.Redis.Connection.Del(al.Redis.Context, keyAttempts).Err(); err != nil {
		log.Log.Errorf(err, "Redis error deleting attempts key for user %d", userID)
	}

	log.Log.Infof("Account unlocked in Redis: user_id=%d", userID)
	return nil
}

// ResetCounterRedis resets the failed attempt counter
func (al *DXMAccountLockout) ResetCounterRedis(userID int64) error {
	keyCounter := al.redisKeyCounterForUser(userID)
	keyAttempts := al.redisKeyAttemptsForUser(userID)

	if err := al.Redis.Connection.Del(al.Redis.Context, keyCounter).Err(); err != nil {
		log.Log.Errorf(err, "Redis error resetting counter for user %d", userID)
		return err
	}

	if err := al.Redis.Connection.Del(al.Redis.Context, keyAttempts).Err(); err != nil {
		log.Log.Errorf(err, "Redis error resetting attempts for user %d", userID)
	}

	return nil
}

// GetFailedAttemptCountRedis gets current failed attempt count
func (al *DXMAccountLockout) GetFailedAttemptCountRedis(userID int64) (int64, error) {
	key := al.redisKeyCounterForUser(userID)

	countStr, err := al.Redis.Connection.HGet(al.Redis.Context, key, "count").Result()
	if err != nil {
		// Key doesn't exist or error
		if err.Error() == "redis: nil" {
			return 0, nil
		}
		return 0, err
	}

	if countStr == "" {
		return 0, nil
	}

	count, err := strconv.ParseInt(countStr, 10, 64)
	if err != nil {
		return 0, err
	}

	return count, nil
}
