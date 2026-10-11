package push_notification

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// fcmErrorFrom sends one message through the real Firebase SDK to a fake FCM
// server that answers with status and body, and returns the SDK's error.
func fcmErrorFrom(t *testing.T, status int, body string) error {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: "drain-test"},
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(ctx, &messaging.Message{Token: "fake-token"})
	if err == nil {
		t.Fatal("fake FCM server: Send returned no error")
	}
	return err
}

const fcmUnregisteredBody = `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND",` +
	`"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`

const fcmInternalBody = `{"error":{"code":500,"message":"Internal error encountered.","status":"INTERNAL",` +
	`"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INTERNAL"}]}}`

func TestDescribeFCMError(t *testing.T) {
	cases := []struct {
		name          string
		err           error
		want          fcmErrorDetail
		wantText      string
		wantPermanent bool
	}{
		{"unregistered token", fcmErrorFrom(t, 404, fcmUnregisteredBody),
			fcmErrorDetail{"UNREGISTERED", "NOT_FOUND", 404}, "FCM error UNREGISTERED, status NOT_FOUND, HTTP 404", true},
		{"FCM internal error", fcmErrorFrom(t, 500, fcmInternalBody),
			fcmErrorDetail{"INTERNAL", "INTERNAL", 500}, "FCM error INTERNAL, status INTERNAL, HTTP 500", false},
		{"no HTTP response", errors.New("dial tcp 142.250.4.95:443: connect: connection refused"),
			fcmErrorDetail{}, "no FCM error code, no HTTP response", false},
	}
	for _, c := range cases {
		got := describeFCMError(c.err)
		if got != c.want {
			t.Errorf("%s: describeFCMError = %+v, want %+v", c.name, got, c.want)
		}
		if got.String() != c.wantText {
			t.Errorf("%s: String() = %q, want %q", c.name, got.String(), c.wantText)
		}
		if isPermanentFCMError(c.err) != c.wantPermanent {
			t.Errorf("%s: isPermanentFCMError = %v, want %v", c.name, !c.wantPermanent, c.wantPermanent)
		}
	}
}

type statusWrite struct {
	status     string
	retryCount int64
}

func newTestTarget(writes *[]statusWrite, writeErr error, tokenRejected *bool) fcmSendTarget {
	return fcmSendTarget{
		Kind:              "message",
		Id:                12,
		Recipient:         "token id 3 (sha256:0123456789ab, IOS)",
		ApplicationNameId: "app1",
		MaxAttempts:       10,
		UpdateStatus: func(ctx context.Context, id int64, status string, retryCount int64) (time.Time, error) {
			*writes = append(*writes, statusWrite{status, retryCount})
			return time.Now().Add(40 * time.Second), writeErr
		},
		OnTokenRejected: func() { *tokenRejected = true },
	}
}

func TestRecordSendResult(t *testing.T) {
	unregistered := fcmErrorFrom(t, 404, fcmUnregisteredBody)
	internal := fcmErrorFrom(t, 500, fcmInternalBody)
	writeFailure := errors.New("pq: connection reset")

	cases := []struct {
		name          string
		sendErr       error
		writeErr      error
		retryCount    int64
		wantWrite     statusWrite
		wantRejected  bool
		check         func(p *fcmDrainPass) bool
		wantCountName string
	}{
		{"sent", nil, nil, 2, statusWrite{StatusSent, 2}, false,
			func(p *fcmDrainPass) bool { return p.Sent == 1 }, "Sent"},
		{"sent, status write failed", nil, writeFailure, 0, statusWrite{StatusSent, 0}, false,
			func(p *fcmDrainPass) bool { return p.UnrecordedSent == 1 && p.Sent == 0 }, "UnrecordedSent"},
		{"permanent, token rejected", unregistered, nil, 4, statusWrite{StatusFailedPermanent, 4}, true,
			func(p *fcmDrainPass) bool { return p.FailedPermanent == 1 }, "FailedPermanent"},
		{"permanent, status write failed", unregistered, writeFailure, 4, statusWrite{StatusFailedPermanent, 4}, true,
			func(p *fcmDrainPass) bool { return p.FailedPermanent == 1 && p.StatusWriteErrors == 1 }, "StatusWriteErrors"},
		{"temporary, retried", internal, nil, 4, statusWrite{StatusFailed, 5}, false,
			func(p *fcmDrainPass) bool { return p.Retry == 1 }, "Retry"},
		{"temporary on the last attempt", internal, nil, 9, statusWrite{StatusFailed, 10}, false,
			func(p *fcmDrainPass) bool { return p.Retry == 1 }, "Retry"},
		{"temporary, status write failed", internal, writeFailure, 4, statusWrite{StatusFailed, 5}, false,
			func(p *fcmDrainPass) bool { return p.Retry == 1 && p.StatusWriteErrors == 1 }, "StatusWriteErrors"},
	}
	for _, c := range cases {
		var writes []statusWrite
		var rejected bool
		pass := &fcmDrainPass{Kind: "message", ApplicationId: 1, ApplicationNameId: "app1", Due: 1}
		recordSendResult(context.Background(), pass, newTestTarget(&writes, c.writeErr, &rejected), c.sendErr, c.retryCount)

		if len(writes) != 1 || writes[0] != c.wantWrite {
			t.Errorf("%s: status writes = %+v, want [%+v]", c.name, writes, c.wantWrite)
		}
		if rejected != c.wantRejected {
			t.Errorf("%s: token rejected = %v, want %v", c.name, rejected, c.wantRejected)
		}
		if !c.check(pass) {
			t.Errorf("%s: pass counters wrong, expected %s: %+v", c.name, c.wantCountName, *pass)
		}
	}
}

func TestDrainPassString(t *testing.T) {
	p := &fcmDrainPass{Kind: "message", ApplicationId: 3, ApplicationNameId: "app1", Due: 9,
		Sent: 4, Retry: 2, FailedPermanent: 1, Expired: 1, HeldElsewhere: 1, UnrecordedSent: 1}
	got := p.String()
	for _, want := range []string{
		"FCM message drain pass for application app1 (id 3): 9 due",
		"4 sent", "2 to retry", "1 failed permanently", "1 expired", "1 held by another worker",
		"1 sent but not recorded",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}

func TestFCMTokenRecipientHidesToken(t *testing.T) {
	const token = "dGhpcyBpcyBhIHJlZ2lzdHJhdGlvbiB0b2tlbg"
	got := fcmTokenRecipient(map[string]any{"fcm_user_token_id": int64(31)}, token, "ANDROID")
	want := "token id 31 (" + FCMTokenFingerprint(token) + ", ANDROID)"
	if got != want {
		t.Errorf("fcmTokenRecipient = %q, want %q", got, want)
	}
	if strings.Contains(got, token) {
		t.Errorf("fcmTokenRecipient carries the full token: %q", got)
	}
	if got := fcmTokenRecipient(map[string]any{}, token, "IOS"); !strings.HasPrefix(got, "token id unknown (") {
		t.Errorf("no token id: got %q", got)
	}
}
