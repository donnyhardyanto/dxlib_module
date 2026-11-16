package pgsink

import (
	"context"
	"strings"
	"testing"

	"github.com/donnyhardyanto/dxlib/databases"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
	"github.com/donnyhardyanto/dxlib_module/v2/module/credentials"
)

// The sink takes a dxlib database or a connection made from the credential provider.
var (
	_ Database = (*databases.DXDatabase)(nil)
	_ Database = (*credentials.PostgresConnection)(nil)
)

// A table name is the only text that reaches the statement unbound, so anything but the fixed form is refused.
func TestNewDBSinkRefusesTableNamesOutsideThePattern(t *testing.T) {
	db := &databases.DXDatabase{}
	for _, name := range []string{
		"", "Audit", "audit;drop table x", "audit events", `"audit"`, "a.b.c", "audit--", "1audit", "audit.",
		".audit", "audit/*x*/", "audit'", "audit\x00", "a" + strings.Repeat("b", 63), "audit.$1", "audit\n",
	} {
		if _, err := NewDBSink(db, name); err == nil || !strings.Contains(err.Error(), "table name") {
			t.Errorf("table name %q: %v, want refused", name, err)
		}
	}
	for _, name := range []string{"audit", "auth.events", "_a.b_1", "a" + strings.Repeat("b", 62)} {
		if _, err := NewDBSink(db, name); err != nil {
			t.Errorf("table name %q: %v, want accepted", name, err)
		}
	}
	if _, err := NewDBSink(nil, "audit"); err == nil {
		t.Error("a nil Database was accepted")
	}
}

func TestWriteRefusesAnEventWithoutKind(t *testing.T) {
	s, err := NewDBSink(&databases.DXDatabase{}, "audit")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), auth_audit.Event{UserUUID: "u"}); err == nil || !strings.Contains(err.Error(), "no Kind") {
		t.Fatalf("got %v, want a refusal naming the missing Kind", err)
	}
}

func TestCleanKeepsTextAndReplacesWhatPostgreSQLRefuses(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"plain":                  "plain",
		"ünïcode ✓ 日本":           "ünïcode ✓ 日本",
		"a\x00b":                 "a�b",
		"a\xffb":                 "a�b",
		"\xc3\x28":               "�(",
		"\x00\x00":               "��",
		"'); DROP TABLE x; --\\": "'); DROP TABLE x; --\\",
	} {
		if got := clean(in); got != want {
			t.Errorf("clean(%q) = %q, want %q", in, got, want)
		}
	}
}
