package push_notification

import (
	"errors"
	"testing"
)

func TestIsPermanentFCMError(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		// transient failures whose text merely contains 400/401/403/404
		{"Post \"https://fcm.googleapis.com/v1/projects/x/messages:send\": dial tcp 10.0.4.1:4043: connect: connection refused", false},
		{"http error status: 503; reason: service unavailable, retry after 4000ms", false},
		{"context deadline exceeded (Client.Timeout exceeded while awaiting headers) request 4011", false},
		// permanent failures
		{"registration-token-not-registered", true},
		{"http error status: 400; reason: request contains an invalid argument; code: invalid-argument", true},
		{"The registration token is not a valid FCM registration token", true},
		{"Requested entity was not found.", true},
		{"permission-denied", true},
	}
	for _, c := range cases {
		if got := isPermanentFCMError(errors.New(c.err)); got != c.want {
			t.Errorf("isPermanentFCMError(%q) = %v, want %v", c.err, got, c.want)
		}
	}
	if isPermanentFCMError(nil) {
		t.Error("nil error reported as permanent")
	}
}
