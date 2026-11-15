package passkey

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/donnyhardyanto/dxlib/databases"
	"github.com/donnyhardyanto/dxlib/databases/db"
	"github.com/donnyhardyanto/dxlib/log"
	"github.com/donnyhardyanto/dxlib/utils"
)

// Database runs a transaction. *databases.DXDatabase implements it; so will a connection made from the credential
// provider's PostgreSQL credentials.
type Database interface {
	Tx(ctx context.Context, log *log.DXLog, isolationLevel sql.IsolationLevel, callback databases.DXDatabaseTxCallback) error
}

// DBStore keeps the passkeys in a PostgreSQL table the service names, through dxlib's databases package. Every
// statement is built by dxlib from fixed column names with the values bound as parameters. The table (README,
// "Passkeys"):
//
//	CREATE TABLE <schema>.<table> (
//	    id              text PRIMARY KEY,           -- credential id, base64url
//	    user_uuid       uuid NOT NULL,
//	    name            text NOT NULL,
//	    credential      text NOT NULL,              -- WebAuthn credential record, JSON
//	    backup_eligible boolean NOT NULL,
//	    backup_state    boolean NOT NULL,
//	    created_at      timestamptz NOT NULL,
//	    last_used_at    timestamptz
//	);
//	CREATE INDEX ON <schema>.<table> (user_uuid);
type DBStore struct {
	db    Database
	table string
	// Log receives dxlib's transaction errors. Default dxlib's log.Log.
	Log *log.DXLog
}

var _ CredentialStore = (*DBStore)(nil)

var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}(\.[a-z_][a-z0-9_]{0,62})?$`)

// The columns; nothing else ever names a column.
const (
	colID             = "id"
	colUserUUID       = "user_uuid"
	colName           = "name"
	colCredential     = "credential"
	colBackupEligible = "backup_eligible"
	colBackupState    = "backup_state"
	colCreatedAt      = "created_at"
	colLastUsedAt     = "last_used_at"
)

var allColumns = []string{colID, colUserUUID, colName, colCredential, colBackupEligible, colBackupState, colCreatedAt, colLastUsedAt}

// NewDBStore returns a store on the table, named "table" or "schema.table" in lower-case letters, digits and _.
func NewDBStore(database Database, table string) (*DBStore, error) {
	if database == nil {
		return nil, errors.New("passkey: NewDBStore needs a Database")
	}
	if !tableNamePattern.MatchString(table) {
		return nil, fmt.Errorf("passkey: table name %q must be table or schema.table, each a lower-case letter or _ followed by up to 62 lower-case letters, digits or _", truncate(table, 140))
	}
	return &DBStore{db: database, table: table, Log: &log.Log}, nil
}

// tx runs fn in a READ COMMITTED transaction. fn returns its own refusal (not found, last way in) in result, so a
// refusal commits a transaction that wrote nothing instead of being logged by dxlib as a database error.
func (s *DBStore) tx(ctx context.Context, op string, fn func(dtx *databases.DXDatabaseTx) (result error, err error)) error {
	var result error
	err := s.db.Tx(ctx, s.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		r, err := fn(dtx)
		result = r
		return err
	})
	if err != nil {
		return fmt.Errorf("passkey store %s on table %s: %w", op, s.table, err)
	}
	return result
}

// rows selects the passkeys matching where, ordered by id, locking them when forUpdate is set (always in id order,
// so two transactions lock a user's rows in the same order and cannot deadlock).
func (s *DBStore) rows(ctx context.Context, dtx *databases.DXDatabaseTx, where utils.JSON, forUpdate bool) ([]Passkey, error) {
	_, rr, err := dtx.Select(ctx, s.table, nil, allColumns, where, nil, nil, nil,
		db.DXDatabaseTableFieldsOrderBy{colID: "asc"}, nil, nil, forUpdate)
	if err != nil {
		return nil, err
	}
	out := make([]Passkey, 0, len(rr))
	for _, r := range rr {
		p, err := rowToPasskey(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Add stores a new passkey.
func (s *DBStore) Add(ctx context.Context, p Passkey, maxPerUser int) error {
	return s.tx(ctx, "add", func(dtx *databases.DXDatabaseTx) (error, error) {
		same, err := s.rows(ctx, dtx, utils.JSON{colID: p.ID}, false)
		if err != nil {
			return nil, err
		}
		if len(same) > 0 {
			return ErrDuplicate, nil
		}
		mine, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: p.UserUUID}, true)
		if err != nil {
			return nil, err
		}
		if maxPerUser > 0 && len(mine) >= maxPerUser {
			return ErrTooMany, nil
		}
		_, _, err = dtx.Insert(ctx, s.table, utils.JSON{
			colID: p.ID, colUserUUID: p.UserUUID, colName: p.Name, colCredential: string(p.Credential),
			colBackupEligible: p.BackupEligible, colBackupState: p.BackupState, colCreatedAt: p.CreatedAt.UTC(),
		}, nil)
		if err != nil && strings.Contains(err.Error(), "23505") {
			return ErrDuplicate, nil
		}
		return nil, err
	})
}

// ByUser returns the user's passkeys, oldest first.
func (s *DBStore) ByUser(ctx context.Context, userUUID string) ([]Passkey, error) {
	var out []Passkey
	err := s.tx(ctx, "list", func(dtx *databases.DXDatabaseTx) (error, error) {
		var err error
		out, err = s.rows(ctx, dtx, utils.JSON{colUserUUID: userUUID}, false)
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ByID returns the passkey with this ID.
func (s *DBStore) ByID(ctx context.Context, id string) (*Passkey, error) {
	var found *Passkey
	err := s.tx(ctx, "find", func(dtx *databases.DXDatabaseTx) (error, error) {
		rr, err := s.rows(ctx, dtx, utils.JSON{colID: id}, false)
		if err != nil {
			return nil, err
		}
		if len(rr) == 0 {
			return ErrNotFound, nil
		}
		found = &rr[0]
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// RecordUse stores the credential record and time after a login.
func (s *DBStore) RecordUse(ctx context.Context, userUUID, id string, credential []byte, usedAt time.Time) error {
	return s.tx(ctx, "record use", func(dtx *databases.DXDatabaseTx) (error, error) {
		res, _, err := dtx.Update(ctx, s.table, utils.JSON{colCredential: string(credential), colLastUsedAt: usedAt.UTC()},
			utils.JSON{colID: id, colUserUUID: userUUID}, nil)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n == 0 {
			return ErrNotFound, nil
		}
		return nil, nil
	})
}

// Remove deletes one of the user's passkeys, after allow agrees. The user's rows stay locked from the count to the
// delete, so two removals of the same user's passkeys run one after the other and allow always sees the true count.
func (s *DBStore) Remove(ctx context.Context, userUUID, id string, allow func(remaining int) error) error {
	return s.tx(ctx, "remove", func(dtx *databases.DXDatabaseTx) (error, error) {
		mine, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: userUUID}, true)
		if err != nil {
			return nil, err
		}
		held := false
		for _, p := range mine {
			if p.ID == id {
				held = true
				break
			}
		}
		if !held {
			return ErrNotFound, nil
		}
		if allow != nil {
			if err := allow(len(mine) - 1); err != nil {
				return err, nil
			}
		}
		// databases/db.TxDelete, not DXDatabaseTx.TxDelete: dxlib v1.133.0's method returns a nil error when the
		// delete fails.
		res, _, err := db.TxDelete(ctx, dtx.Tx, s.table, utils.JSON{colID: id, colUserUUID: userUUID}, nil)
		if err != nil {
			return nil, err
		}
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n != 1 {
			return nil, fmt.Errorf("deleting passkey %s of user %s removed %d rows, not 1", truncate(id, 16), userUUID, n)
		}
		return nil, nil
	})
}

func rowToPasskey(r utils.JSON) (Passkey, error) {
	var p Passkey
	var err error
	str := func(col string) string {
		if err != nil {
			return ""
		}
		var s string
		s, err = asText(r[col], col)
		return s
	}
	p.ID = str(colID)
	p.UserUUID = strings.ToLower(str(colUserUUID))
	p.Name = str(colName)
	p.Credential = []byte(str(colCredential))
	if err != nil {
		return p, err
	}
	if p.BackupEligible, err = asBool(r[colBackupEligible], colBackupEligible); err != nil {
		return p, err
	}
	if p.BackupState, err = asBool(r[colBackupState], colBackupState); err != nil {
		return p, err
	}
	if p.CreatedAt, err = asTime(r[colCreatedAt], colCreatedAt); err != nil {
		return p, err
	}
	if r[colLastUsedAt] != nil {
		if p.LastUsedAt, err = asTime(r[colLastUsedAt], colLastUsedAt); err != nil {
			return p, err
		}
	}
	return p, nil
}

func asText(v interface{}, col string) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []byte:
		return string(x), nil
	case [16]byte:
		return fmt.Sprintf("%x-%x-%x-%x-%x", x[0:4], x[4:6], x[6:8], x[8:10], x[10:16]), nil
	default:
		return "", fmt.Errorf("column %s: unexpected value type %T", col, v)
	}
}

func asBool(v interface{}, col string) (bool, error) {
	if b, ok := v.(bool); ok {
		return b, nil
	}
	return false, fmt.Errorf("column %s: unexpected value type %T", col, v)
}

func asTime(v interface{}, col string) (time.Time, error) {
	if t, ok := v.(time.Time); ok {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("column %s: unexpected value type %T", col, v)
}
