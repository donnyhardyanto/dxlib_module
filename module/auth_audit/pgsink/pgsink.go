// Package pgsink is an auth_audit.Sink that writes each event as one row of a PostgreSQL table the service names,
// through dxlib's databases package. It lives apart from auth_audit so that the packages writing audit events
// (module/session and the others) do not import dxlib's database code.
//
// The table is append-only: the role the sink connects as holds INSERT on the columns the sink writes and nothing
// else, so id and recorded_at always come from the database; the table is owned by another role (README, "Audit
// store in PostgreSQL"):
//
//	CREATE TABLE <schema>.<table> (
//	    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
//	    occurred_at timestamptz NOT NULL,
//	    recorded_at timestamptz NOT NULL DEFAULT now(),
//	    kind        text NOT NULL,
//	    user_uuid   text NOT NULL,
//	    actor_uuid  text NOT NULL,
//	    session_id  text NOT NULL,
//	    outcome     text NOT NULL,
//	    reason      text NOT NULL,
//	    detail      jsonb NOT NULL
//	);
//	REVOKE ALL ON <schema>.<table> FROM PUBLIC;
//	GRANT USAGE ON SCHEMA <schema> TO <writer>;
//	GRANT INSERT (occurred_at, kind, user_uuid, actor_uuid, session_id, outcome, reason, detail)
//	    ON <schema>.<table> TO <writer>;
package pgsink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"

	"github.com/donnyhardyanto/dxlib_module/v2/module/auth_audit"
)

// Database runs a transaction. *databases.DXDatabase implements it, and so does credentials.PostgresConnection,
// which takes its credential from a credentials.Provider.
type Database interface {
	Tx(ctx context.Context, log *log.DXLog, isolationLevel sql.IsolationLevel, callback databases.DXDatabaseTxCallback) error
}

// DBSink writes audit events to the table. Every statement is an INSERT built by dxlib from the fixed column names
// below, with every value bound as a parameter; it never reads, updates or deletes a row, so the role it connects
// as needs INSERT alone.
type DBSink struct {
	db    Database
	table string
	// Log receives dxlib's transaction errors. Default dxlib's log.Log.
	Log *log.DXLog
}

var _ auth_audit.Sink = (*DBSink)(nil)

// tableNamePattern is the only form a table name may take: table or schema.table, each a lower-case letter or _
// followed by up to 62 lower-case letters, digits or _. Nothing else reaches the statement's text.
var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}(\.[a-z_][a-z0-9_]{0,62})?$`)

// The columns; nothing else ever names a column. id and recorded_at are filled by the database.
const (
	colOccurredAt = "occurred_at"
	colKind       = "kind"
	colUserUUID   = "user_uuid"
	colActorUUID  = "actor_uuid"
	colSessionID  = "session_id"
	colOutcome    = "outcome"
	colReason     = "reason"
	colDetail     = "detail"
)

// NewDBSink returns a sink on the table, named "table" or "schema.table" in lower-case letters, digits and _.
func NewDBSink(database Database, table string) (*DBSink, error) {
	if database == nil {
		return nil, errors.New("auth_audit/pgsink: NewDBSink needs a Database")
	}
	if !tableNamePattern.MatchString(table) {
		return nil, fmt.Errorf("auth_audit/pgsink: table name %q must be table or schema.table, each a lower-case letter or _ followed by up to 62 lower-case letters, digits or _", truncate(table, 140))
	}
	return &DBSink{db: database, table: table, Log: &log.Log}, nil
}

// Table returns the table the sink writes to.
func (s *DBSink) Table() string { return s.table }

// Write inserts the event as one row and returns once it is committed, or with the database's own error. A zero
// Time is stored as now. Text is stored as given, except that a NUL character and bytes that are not UTF-8, which
// PostgreSQL refuses in text and jsonb, become U+FFFD: otherwise a login name holding one would make the audit
// record of that very attempt fail.
func (s *DBSink) Write(ctx context.Context, e auth_audit.Event) error {
	if e.Kind == "" {
		return fmt.Errorf("auth_audit/pgsink: writing to table %s: the event has no Kind", s.table)
	}
	t := e.Time
	if t.IsZero() {
		t = time.Now()
	}
	detail := make(map[string]string, len(e.Detail))
	for k, v := range e.Detail {
		detail[clean(k)] = clean(v)
	}
	dj, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("auth_audit/pgsink: encoding the detail of a %s event for table %s: %w", e.Kind, s.table, err)
	}
	values := utils.JSON{
		colOccurredAt: t.UTC(),
		colKind:       clean(string(e.Kind)),
		colUserUUID:   clean(e.UserUUID),
		colActorUUID:  clean(e.ActorUUID),
		colSessionID:  clean(e.SessionID),
		colOutcome:    clean(string(e.Outcome)),
		colReason:     clean(e.Reason),
		colDetail:     string(dj),
	}
	err = s.db.Tx(ctx, s.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		_, _, err := dtx.Insert(ctx, s.table, values, nil)
		return err
	})
	if err != nil {
		return fmt.Errorf("auth_audit/pgsink: writing a %s event for user %q to table %s: %w", e.Kind, truncate(e.UserUUID, 64), s.table, err)
	}
	return nil
}

// clean makes s storable in a PostgreSQL text or jsonb value: bytes that are not UTF-8 and NUL characters become
// U+FFFD; everything else is kept.
func clean(s string) string {
	if utf8.ValidString(s) && !strings.ContainsRune(s, 0) {
		return s
	}
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "�")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
