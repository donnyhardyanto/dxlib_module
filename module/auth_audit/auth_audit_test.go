package auth_audit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoresFiltersAndCopies(t *testing.T) {
	m := &Memory{}
	ctx := context.Background()
	if err := m.Write(ctx, Event{Kind: LoginSucceeded, UserUUID: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write(ctx, Event{Kind: Logout, UserUUID: "a", Time: time.Unix(5, 0)}); err != nil {
		t.Fatal(err)
	}
	ev := m.Events()
	if len(ev) != 2 || ev[0].Time.IsZero() || !ev[1].Time.Equal(time.Unix(5, 0)) {
		t.Fatalf("events: %+v", ev)
	}
	ev[0].Kind = PhotoChanged
	if m.Events()[0].Kind != LoginSucceeded {
		t.Fatal("Events returned the stored slice, not a copy")
	}
	if got := m.OfKind(Logout); len(got) != 1 || got[0].UserUUID != "a" {
		t.Fatalf("OfKind: %+v", got)
	}
}

func TestMemoryFailReturnsTheErrorAndStoresNothing(t *testing.T) {
	m := &Memory{Fail: errors.New("disk full")}
	if err := m.Write(context.Background(), Event{Kind: LoginFailed}); err == nil || err.Error() != "disk full" {
		t.Fatalf("Write with Fail set: %v", err)
	}
	if len(m.Events()) != 0 {
		t.Fatal("a failed write stored the event")
	}
}
