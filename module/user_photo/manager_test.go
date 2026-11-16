package user_photo

import (
	"bytes"
	"errors"
	"image/color"
	"strings"
	"testing"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/session"
)

func TestSetStoresUnderARandomKeyAndPropagatesTheReference(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 500, 400, color.NRGBA{9, 9, 9, 255})))
	if err != nil {
		t.Fatal(err)
	}
	if ch.Photo.Version != 1 || ch.Photo.Ref != "/users/"+alice+"/photo?v=1" || ch.SessionsErr != nil || ch.OldObjectErr != nil {
		t.Fatalf("change: %+v", ch)
	}
	keys := r.objects.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "user-photo/") || strings.Contains(keys[0], alice) || len(keys[0]) != len("user-photo/")+64+len(".webp") {
		t.Fatalf("object keys %v: want one random key under the prefix, not naming the user", keys)
	}
	rec, _ := r.store.Get(bg, alice)
	if rec.ObjectKey != keys[0] || rec.Version != 1 {
		t.Fatalf("row %+v", rec)
	}
	if len(r.sessions.propagated) != 1 {
		t.Fatalf("propagated %d times", len(r.sessions.propagated))
	}
	p := r.sessions.propagated[0]
	if p.PhotoRef != ch.Photo.Ref || p.PhotoVersion != 1 || p.Version != 7 || p.DisplayName != "User" {
		t.Fatalf("propagated profile %+v: want the service's profile (version 7 kept) with the new photo", p)
	}
	ev := r.audit.OfKind(auth_audit.PhotoChanged)
	if len(ev) != 1 || ev[0].Outcome != auth_audit.OutcomeOK || ev[0].UserUUID != alice || ev[0].ActorUUID != "" || ev[0].SessionID != caller.ID || ev[0].Detail["version"] != "1" {
		t.Fatalf("audit %+v", ev)
	}

	// A second photo replaces the first: new key, version 2, the old object deleted.
	ch2, err := r.m.Set(bg, caller, alice, bytes.NewReader(jpegWithExif(t, 300, 300, "x")))
	if err != nil {
		t.Fatal(err)
	}
	keys2 := r.objects.Keys()
	if ch2.Photo.Version != 2 || len(keys2) != 1 || keys2[0] == keys[0] {
		t.Fatalf("after the second photo: %+v, keys %v", ch2.Photo, keys2)
	}
	if r.sessions.propagated[1].PhotoVersion != 2 {
		t.Fatalf("second propagation %+v", r.sessions.propagated[1])
	}
}

func TestAnotherUsersPhotoCannotBeChangedWithoutTheRight(t *testing.T) {
	r := newRig(t, Config{}, SelfOrPermission{ChangeAny: "user.photo.change_any"})
	alice, bob := newUser(), newUser()
	_, bobSession := r.sessions.login(t, bob)
	_, err := r.m.Set(bg, bobSession, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if r.store.Calls != 0 || r.objects.Calls != 0 || len(r.sessions.propagated) != 0 {
		t.Fatalf("a refused change touched the stores: store %d, objects %d, propagated %d", r.store.Calls, r.objects.Calls, len(r.sessions.propagated))
	}
	if _, err := r.m.Remove(bg, bobSession, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove: got %v, want ErrNotFound", err)
	}
	ev := r.audit.OfKind(auth_audit.PhotoChanged)
	if len(ev) != 1 || ev[0].Outcome != auth_audit.OutcomeFailed || ev[0].ActorUUID != bob || !strings.Contains(ev[0].Reason, "may not") {
		t.Fatalf("audit %+v", ev)
	}
	// An administrator holding the permission may.
	_, admin := r.sessions.login(t, newUser(), "user.photo.change_any")
	if _, err := r.m.Set(bg, admin, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White))); err != nil {
		t.Fatal(err)
	}
	ev = r.audit.OfKind(auth_audit.PhotoChanged)
	if last := ev[len(ev)-1]; last.Outcome != auth_audit.OutcomeOK || last.ActorUUID != admin.UserUUID {
		t.Fatalf("admin change audit %+v", last)
	}
	// No session at all.
	if _, err := r.m.Set(bg, nil, alice, bytes.NewReader(nil)); !errors.Is(err, ErrNoCaller) {
		t.Fatalf("nil caller: %v", err)
	}
}

func TestRefusedImagesAreAuditedAndStoreNothing(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	for name, body := range map[string][]byte{
		"gif named png": []byte("GIF89a....."),
		"over 2 MiB":    make([]byte, DefaultMaxBytes+1),
		"pixel bomb":    pngHeaderOnly(40000, 40000),
	} {
		if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(body)); !errors.Is(err, ErrRejected) {
			t.Fatalf("%s: got %v, want ErrRejected", name, err)
		}
	}
	if r.objects.Calls != 0 || r.store.Calls != 0 {
		t.Fatalf("refused uploads touched the stores")
	}
	if ev := r.audit.OfKind(auth_audit.PhotoChanged); len(ev) != 3 || ev[0].Outcome != auth_audit.OutcomeFailed || ev[0].Reason == "" {
		t.Fatalf("audit %+v", ev)
	}
}

func TestExactly2MiBIsAccepted(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	p := solidPNG(t, 64, 64, color.NRGBA{5, 6, 7, 255})
	pad := DefaultMaxBytes - len(p) - 12 // a private ancillary chunk brings the file to exactly 2 MiB
	img := withChunkBeforeIEND(p, pngChunk("zzZz", make([]byte, pad)))
	if len(img) != DefaultMaxBytes {
		t.Fatalf("fixture is %d bytes", len(img))
	}
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(img)); err != nil {
		t.Fatalf("exactly 2 MiB: %v", err)
	}
	img = withChunkBeforeIEND(p, pngChunk("zzZz", make([]byte, pad+1)))
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(img)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("2 MiB + 1: got %v, want ErrTooLarge", err)
	}
}

func TestAFailedDatabaseWriteDeletesTheNewObject(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	r.store.Fail = errors.New("connection refused by db host")
	_, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if err == nil || !strings.Contains(err.Error(), "connection refused by db host") {
		t.Fatalf("got %v", err)
	}
	if len(r.objects.Keys()) != 0 {
		t.Fatalf("the unreferenced object was left: %v", r.objects.Keys())
	}
	if len(r.sessions.propagated) != 0 {
		t.Fatal("propagated a change that was not stored")
	}
	// When the object cannot be deleted either, both causes and the key are named.
	r.objects.FailDelete = errors.New("bucket unreachable")
	_, err = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if err == nil || !strings.Contains(err.Error(), "bucket unreachable") || !strings.Contains(err.Error(), "user-photo/") {
		t.Fatalf("got %v", err)
	}
	ev := r.audit.OfKind(auth_audit.PhotoChanged)
	if last := ev[len(ev)-1]; last.Detail["object_left"] == "" {
		t.Fatalf("audit does not name the object left: %+v", last)
	}
}

func TestAnOldObjectThatCannotBeDeletedIsReportedNotFatal(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White))); err != nil {
		t.Fatal(err)
	}
	old := r.objects.Keys()[0]
	r.objects.FailDelete = errors.New("HTTP 503 SlowDown")
	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.Black)))
	if err != nil {
		t.Fatal(err)
	}
	if ch.OldObjectErr == nil || !strings.Contains(ch.OldObjectErr.Error(), old) || !strings.Contains(ch.OldObjectErr.Error(), "503") {
		t.Fatalf("OldObjectErr %v", ch.OldObjectErr)
	}
	if ch.Photo.Version != 2 || len(r.sessions.propagated) != 2 {
		t.Fatalf("the change must still be stored and propagated: %+v", ch)
	}
	ev := r.audit.OfKind(auth_audit.PhotoChanged)
	if ev[len(ev)-1].Detail["old_object_left"] != old {
		t.Fatalf("audit %+v", ev[len(ev)-1])
	}
}

func TestSessionsFailClosedWhenTheProfileCannotBeLoaded(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	r.loadErr = errors.New("users table: statement timeout")
	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if err != nil {
		t.Fatal(err)
	}
	if ch.SessionsErr == nil || !strings.Contains(ch.SessionsErr.Error(), "statement timeout") || len(r.sessions.invalidated) != 1 || len(r.sessions.revoked) != 0 {
		t.Fatalf("SessionsErr %v, invalidated %v, revoked %v: want the cached profiles dropped", ch.SessionsErr, r.sessions.invalidated, r.sessions.revoked)
	}
	// If dropping the cached profiles fails too, the sessions are ended.
	r.sessions.invalidErr = errors.New("valkey: connection refused")
	ch, err = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.sessions.revoked) != 1 || !strings.Contains(ch.SessionsErr.Error(), "connection refused") || !strings.Contains(ch.SessionsErr.Error(), "ended") {
		t.Fatalf("SessionsErr %v, revoked %v", ch.SessionsErr, r.sessions.revoked)
	}
	ev := r.audit.OfKind(auth_audit.PhotoChanged)
	if ev[len(ev)-1].Detail["sessions_error"] == "" {
		t.Fatalf("audit %+v", ev[len(ev)-1])
	}
	// A propagation error from the session store is handed back as it is.
	r.loadErr, r.sessions.invalidErr = nil, nil
	perr := &session.PropagationError{UserUUID: alice, UpdateErr: errors.New("EVAL failed"), Deleted: 2}
	r.sessions.propagateErr = perr
	ch, _ = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	var got *session.PropagationError
	if !errors.As(ch.SessionsErr, &got) || got.Deleted != 2 {
		t.Fatalf("SessionsErr %v", ch.SessionsErr)
	}
}

func TestRemoveKeepsARisingVersionAndClearsTheReference(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	if _, err := r.m.Remove(bg, caller, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove without a photo: %v", err)
	}
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White))); err != nil {
		t.Fatal(err)
	}
	ch, err := r.m.Remove(bg, caller, alice)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Photo.Version != 2 || len(r.objects.Keys()) != 0 {
		t.Fatalf("change %+v, objects %v", ch, r.objects.Keys())
	}
	p := r.sessions.propagated[len(r.sessions.propagated)-1]
	if p.PhotoRef != "" || p.PhotoVersion != 0 {
		t.Fatalf("propagated %+v: want no photo", p)
	}
	if _, err := r.m.Remove(bg, caller, alice); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: %v", err)
	}
	if ev := r.audit.OfKind(auth_audit.PhotoRemoved); len(ev) != 3 || ev[1].Outcome != auth_audit.OutcomeOK {
		t.Fatalf("audit %+v", ev)
	}
	// A new photo after a removal continues the version.
	ch, _ = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if ch.Photo.Version != 3 {
		t.Fatalf("version after removal %d", ch.Photo.Version)
	}
}

func TestDeleteUserRemovesTheRowAndTheObject(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice, admin := newUser(), newUser()
	_, caller := r.sessions.login(t, alice)
	if err := r.m.DeleteUser(bg, alice, admin); err != nil {
		t.Fatalf("user without a photo: %v", err)
	}
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White))); err != nil {
		t.Fatal(err)
	}
	if err := r.m.DeleteUser(bg, alice, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Get(bg, alice); !errors.Is(err, ErrNotFound) || len(r.objects.Keys()) != 0 {
		t.Fatalf("row %v, objects %v", err, r.objects.Keys())
	}
	ev := r.audit.OfKind(auth_audit.PhotoRemoved)
	if len(ev) != 1 || ev[0].ActorUUID != admin || ev[0].Detail["reason"] != "user deleted" {
		t.Fatalf("audit %+v", ev)
	}
	// An object that cannot be deleted is named in the error and the audit record.
	_, _ = r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	key := r.objects.Keys()[0]
	r.objects.FailDelete = errors.New("HTTP 500 InternalError")
	err := r.m.DeleteUser(bg, alice, admin)
	if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "500") {
		t.Fatalf("got %v", err)
	}
	ev = r.audit.OfKind(auth_audit.PhotoRemoved)
	if last := ev[len(ev)-1]; last.Outcome != auth_audit.OutcomeFailed || last.Detail["object_left"] != key {
		t.Fatalf("audit %+v", last)
	}
	if err := r.m.DeleteUser(bg, "x' OR '1'='1", admin); err == nil {
		t.Fatal("a malformed uuid was accepted")
	}
}

func TestProfilesFillThePhotoAndKeepTheServicesVersion(t *testing.T) {
	r := newRig(t, Config{PhotoPath: "/api/v1/users/{uuid}/avatar"}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	p, err := r.m.profiles.LoadProfile(bg, alice)
	if err != nil || p.PhotoRef != "" || p.PhotoVersion != 0 || p.Version != 7 {
		t.Fatalf("no photo: %+v %v", p, err)
	}
	if _, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White))); err != nil {
		t.Fatal(err)
	}
	p = session.Profile{DisplayName: "from login", Version: 3, PhotoRef: "stale", PhotoVersion: 99}
	if err := r.m.profiles.Fill(bg, alice, &p); err != nil {
		t.Fatal(err)
	}
	if p.PhotoRef != "/api/v1/users/"+alice+"/avatar?v=1" || p.PhotoVersion != 1 || p.Version != 3 || p.DisplayName != "from login" {
		t.Fatalf("filled %+v", p)
	}
	r.store.Fail = errors.New("db down")
	if err := r.m.profiles.Fill(bg, alice, &p); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("got %v", err)
	}
}

func TestAnAuditStoreThatFailsIsReportedBesideTheStoredChange(t *testing.T) {
	r := newRig(t, Config{}, nil)
	alice := newUser()
	_, caller := r.sessions.login(t, alice)
	r.audit.Fail = errors.New("audit db: disk full")
	ch, err := r.m.Set(bg, caller, alice, bytes.NewReader(solidPNG(t, 10, 10, color.White)))
	if ch == nil || ch.Photo.Version != 1 || err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("change %+v, err %v: want the stored change and the audit error", ch, err)
	}
	// A refusal keeps its own cause first, the audit failure added.
	_, err = r.m.Set(bg, caller, alice, bytes.NewReader([]byte("GIF89a")))
	if !errors.Is(err, ErrFormat) || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("refusal with a failing audit: %v", err)
	}
}
