package push_notification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib_module/base"
)

// A missing application or token comes back from the public methods, through
// the transaction, as a base.NotFoundError naming what was looked up and
// never the full token. Needs DXLIB_MODULE_TEST_POSTGRES_URL (see
// openClaimTestDatabase).
func TestSendToDeviceNotFoundIsTyped(t *testing.T) {
	f := openClaimTestDatabase(t)
	const token = "secret-fcm-registration-token-value"

	err := f.SendToDevice(context.Background(), &log.Log, "no-such-app", 7, token, "t", "b", nil, 0)
	var nf *base.NotFoundError
	if !errors.As(err, &nf) || !errors.Is(err, base.ErrNotFound) {
		t.Fatalf("missing application: got %v, want a base.NotFoundError", err)
	}
	if nf.Code != "FCM_APPLICATION_NOT_FOUND" || nf.Lookup["nameid"] != "no-such-app" {
		t.Errorf("missing application: got code %q lookup %v", nf.Code, nf.Lookup)
	}

	if _, err := f.Database.Connection.Exec(`INSERT INTO push_notification.fcm_application (nameid) VALUES ('app1')`); err != nil {
		t.Fatal(err)
	}
	err = f.SendToDevice(context.Background(), &log.Log, "app1", 7, token, "t", "b", nil, 0)
	if !errors.As(err, &nf) {
		t.Fatalf("missing token: got %v, want a base.NotFoundError", err)
	}
	if nf.Code != "FCM_USER_TOKEN_NOT_FOUND" || nf.Lookup["user_id"] != int64(7) || nf.Lookup["fcm_application_nameid"] != "app1" {
		t.Errorf("missing token: got code %q lookup %v", nf.Code, nf.Lookup)
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("missing token: error text carries the full token: %v", err)
	}
	if nf.Lookup["fcm_token"] != FCMTokenFingerprint(token) {
		t.Errorf("missing token: lookup fcm_token = %v, want the fingerprint", nf.Lookup["fcm_token"])
	}
}
