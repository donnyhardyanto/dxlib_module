package push_notification

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/donnyhardyanto/dxlib/base"
	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/tables"
)

// The claim tests need a throwaway PostgreSQL database, for example:
//
//	podman run -d --rm --name dxlib-module-pg -e POSTGRES_PASSWORD=test -p 55432:5432 postgres:16
//	DXLIB_MODULE_TEST_POSTGRES_URL=postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable \
//	    go test ./module/push_notification/ -run Claim
//
// They create and drop the push_notification schema in that database.
func openClaimTestDatabase(t *testing.T) *FirebaseCloudMessaging {
	t.Helper()
	url := os.Getenv("DXLIB_MODULE_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("DXLIB_MODULE_TEST_POSTGRES_URL not set")
	}

	const nameId = "push_notification_claim_test"
	d := databases.Manager.NewDatabase(nameId, false, false)
	d.DatabaseType = base.DXDatabaseTypePostgreSQL
	d.ConnectionString = url
	d.NonSensitiveConnectionString = "test"
	d.IsConfigured = true
	if err := d.Connect(); err != nil {
		t.Fatal(err)
	}

	ddl := []string{
		`DROP SCHEMA IF EXISTS push_notification CASCADE`,
		`CREATE SCHEMA push_notification`,
		`CREATE TABLE push_notification.fcm_message (
			id bigserial PRIMARY KEY,
			uid text NOT NULL DEFAULT gen_random_uuid()::text,
			status varchar(255) NOT NULL,
			next_retry_time timestamp with time zone,
			retry_count integer NOT NULL DEFAULT 0)`,
		`CREATE TABLE push_notification.fcm_topic_message (
			id bigserial PRIMARY KEY,
			uid text NOT NULL DEFAULT gen_random_uuid()::text,
			status varchar(255) NOT NULL,
			next_retry_time timestamp with time zone,
			retry_count integer NOT NULL DEFAULT 0)`,
	}
	for _, q := range ddl {
		if _, err := d.Connection.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = d.Connection.Exec(`DROP SCHEMA IF EXISTS push_notification CASCADE`)
	})

	f := &FirebaseCloudMessaging{}
	f.Init(nameId)
	return f
}

func insertMessage(t *testing.T, f *FirebaseCloudMessaging, table string, status string, nextRetryTime *time.Time) int64 {
	t.Helper()
	var id int64
	err := f.Database.Connection.QueryRow(
		`INSERT INTO push_notification.`+table+` (status, next_retry_time) VALUES ($1, $2) RETURNING id`,
		status, nextRetryTime).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Several workers race for the same pending messages; each message must be
// claimed, and so sent, exactly once.
func TestClaimMessageForSendingTwoWorkersSendOnce(t *testing.T) {
	f := openClaimTestDatabase(t)

	const messageCount = 200
	ids := make([]int64, 0, messageCount)
	for i := 0; i < messageCount; i++ {
		ids = append(ids, insertMessage(t, f, "fcm_message", StatusPending, nil))
	}

	const workerCount = 4
	var mu sync.Mutex
	sends := map[int64]int{}
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, id := range ids {
				isClaimed, err := claimMessageForSending(context.Background(), f.FCMMessage, id)
				if err != nil {
					t.Error(err)
					return
				}
				if isClaimed {
					mu.Lock()
					sends[id]++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	for _, id := range ids {
		if sends[id] != 1 {
			t.Errorf("message %d sent %d times, want 1", id, sends[id])
		}
	}
}

func TestClaimMessageForSendingStates(t *testing.T) {
	f := openClaimTestDatabase(t)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	cases := []struct {
		name          string
		table         *tables.DXRawTable
		tableName     string
		status        string
		nextRetryTime *time.Time
		want          bool
	}{
		{"pending", f.FCMMessage, "fcm_message", StatusPending, nil, true},
		{"failed and due", f.FCMMessage, "fcm_message", StatusFailed, &past, true},
		{"failed, not due yet", f.FCMMessage, "fcm_message", StatusFailed, &future, false},
		{"claim expired", f.FCMMessage, "fcm_message", StatusSending, &past, true},
		{"claim held", f.FCMMessage, "fcm_message", StatusSending, &future, false},
		{"sent", f.FCMMessage, "fcm_message", StatusSent, nil, false},
		{"failed permanently", f.FCMMessage, "fcm_message", StatusFailedPermanent, nil, false},
		{"topic pending", f.FCMTopicMessage, "fcm_topic_message", StatusPending, nil, true},
		{"topic claim held", f.FCMTopicMessage, "fcm_topic_message", StatusSending, &future, false},
	}
	for _, c := range cases {
		id := insertMessage(t, f, c.tableName, c.status, c.nextRetryTime)
		got, err := claimMessageForSending(context.Background(), c.table, id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: claimed = %v, want %v", c.name, got, c.want)
		}
	}
}
