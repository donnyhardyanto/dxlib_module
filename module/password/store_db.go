package password

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
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

// DBStore keeps the passwords in a PostgreSQL table the service names, through dxlib's databases package. Every
// statement is built by dxlib from fixed column names with the values bound as parameters. The table (README,
// "Passwords"):
//
//	CREATE TABLE <schema>.<table> (
//	    user_uuid            uuid PRIMARY KEY,
//	    ciphertext           text NOT NULL,      -- transit ciphertext of the uuid and the Argon2id hash
//	    key_version          integer NOT NULL,   -- transit key version of ciphertext
//	    must_change_password boolean NOT NULL,
//	    changed_at           timestamptz NOT NULL
//	);
//	CREATE INDEX ON <schema>.<table> (key_version);
type DBStore struct {
	db    Database
	table string
	// Log receives dxlib's transaction errors. Default dxlib's log.Log.
	Log *log.DXLog
}

var _ Store = (*DBStore)(nil)

var tableNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}(\.[a-z_][a-z0-9_]{0,62})?$`)

// The columns; nothing else ever names a column.
const (
	colUserUUID   = "user_uuid"
	colCiphertext = "ciphertext"
	colKeyVersion = "key_version"
	colMustChange = "must_change_password"
	colChangedAt  = "changed_at"
)

var allColumns = []string{colUserUUID, colCiphertext, colKeyVersion, colMustChange, colChangedAt}

// NewDBStore returns a store on the table, named "table" or "schema.table" in lower-case letters, digits and _.
func NewDBStore(database Database, table string) (*DBStore, error) {
	if database == nil {
		return nil, errors.New("password: NewDBStore needs a Database")
	}
	if !tableNamePattern.MatchString(table) {
		return nil, fmt.Errorf("password: table name %q must be table or schema.table, each a lower-case letter or _ followed by up to 62 lower-case letters, digits or _", truncate(table, 140))
	}
	return &DBStore{db: database, table: table, Log: &log.Log}, nil
}

// tx runs fn in a READ COMMITTED transaction. fn returns its own refusal (not found, conflict, last way in) in
// result, so a refusal commits a transaction that wrote nothing instead of being logged by dxlib as a database
// error. A failed statement goes back as err, so the transaction is rolled back.
func (s *DBStore) tx(ctx context.Context, op string, fn func(dtx *databases.DXDatabaseTx) (result error, err error)) error {
	var result error
	err := s.db.Tx(ctx, s.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		r, err := fn(dtx)
		result = r
		return err
	})
	if err != nil {
		return fmt.Errorf("password store %s on table %s: %w", op, s.table, err)
	}
	return result
}

func (s *DBStore) rows(ctx context.Context, dtx *databases.DXDatabaseTx, where utils.JSON, limit any, forUpdate bool) ([]Record, error) {
	_, rr, err := dtx.Select(ctx, s.table, nil, allColumns, where, nil, nil, nil,
		db.DXDatabaseTableFieldsOrderBy{colUserUUID: "asc"}, limit, nil, forUpdate)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(rr))
	for _, r := range rr {
		rec, err := rowToRecord(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func recordValues(r Record) utils.JSON {
	return utils.JSON{colCiphertext: r.Ciphertext, colKeyVersion: r.KeyVersion, colMustChange: r.MustChange, colChangedAt: r.ChangedAt.UTC()}
}

func isUniqueViolation(err error) bool { return err != nil && strings.Contains(err.Error(), "23505") }

// Get returns the user's record.
func (s *DBStore) Get(ctx context.Context, userUUID string) (*Record, error) {
	var found *Record
	err := s.tx(ctx, "get", func(dtx *databases.DXDatabaseTx) (error, error) {
		rr, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: userUUID}, nil, false)
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

// Put stores the record, replacing the user's earlier one.
func (s *DBStore) Put(ctx context.Context, r Record) error {
	return s.tx(ctx, "put", func(dtx *databases.DXDatabaseTx) (error, error) {
		cur, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: r.UserUUID}, nil, true)
		if err != nil {
			return nil, err
		}
		if len(cur) > 0 {
			res, _, err := dtx.Update(ctx, s.table, recordValues(r), utils.JSON{colUserUUID: r.UserUUID}, nil)
			if err != nil {
				return nil, err
			}
			return nil, oneRow(res, "updating", r.UserUUID)
		}
		v := recordValues(r)
		v[colUserUUID] = r.UserUUID
		if _, _, err := dtx.Insert(ctx, s.table, v, nil); err != nil {
			if isUniqueViolation(err) {
				// Another transaction stored this user's first password between the select and the insert. The
				// failed insert aborted the transaction, so the conflict goes back as the callback's error and
				// dxlib rolls back.
				return nil, fmt.Errorf("%w: %v", ErrConflict, err)
			}
			return nil, err
		}
		return nil, nil
	})
}

// Swap replaces the record while its ciphertext is unchanged. The row is locked, compared and updated by its user
// uuid alone: dxlib v1.133.0's Update binds one value per column name, so a column named in both SET and WHERE
// would be set to its WHERE value.
func (s *DBStore) Swap(ctx context.Context, oldCiphertext string, r Record) error {
	return s.tx(ctx, "swap", func(dtx *databases.DXDatabaseTx) (error, error) {
		cur, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: r.UserUUID}, nil, true)
		if err != nil {
			return nil, err
		}
		if len(cur) == 0 {
			return ErrNotFound, nil
		}
		if cur[0].Ciphertext != oldCiphertext {
			return ErrConflict, nil
		}
		res, _, err := dtx.Update(ctx, s.table, recordValues(r), utils.JSON{colUserUUID: r.UserUUID}, nil)
		if err != nil {
			return nil, err
		}
		return nil, oneRow(res, "replacing", r.UserUUID)
	})
}

// ByKeyVersion returns up to limit records of the key version, by user uuid.
func (s *DBStore) ByKeyVersion(ctx context.Context, version, limit int) ([]Record, error) {
	var out []Record
	var lim any
	if limit > 0 {
		lim = int64(limit)
	}
	err := s.tx(ctx, "list by key version", func(dtx *databases.DXDatabaseTx) (error, error) {
		var err error
		out, err = s.rows(ctx, dtx, utils.JSON{colKeyVersion: version}, lim, false)
		return nil, err
	})
	return out, err
}

// Remove deletes the user's record after allow agrees; the row stays locked from the check to the delete.
func (s *DBStore) Remove(ctx context.Context, userUUID string, allow func() error) error {
	return s.tx(ctx, "remove", func(dtx *databases.DXDatabaseTx) (error, error) {
		cur, err := s.rows(ctx, dtx, utils.JSON{colUserUUID: userUUID}, nil, true)
		if err != nil {
			return nil, err
		}
		if len(cur) == 0 {
			return ErrNotFound, nil
		}
		if allow != nil {
			if err := allow(); err != nil {
				return err, nil
			}
		}
		// databases/db.TxDelete, not DXDatabaseTx.TxDelete: dxlib v1.133.0's method returns a nil error when the
		// delete fails.
		res, _, err := db.TxDelete(ctx, dtx.Tx, s.table, utils.JSON{colUserUUID: userUUID}, nil)
		if err != nil {
			return nil, err
		}
		return nil, oneRow(res, "deleting", userUUID)
	})
}

func oneRow(res sql.Result, what, userUUID string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%s the password of user %s touched %d rows, not 1", what, userUUID, n)
	}
	return nil
}

func rowToRecord(r utils.JSON) (Record, error) {
	var rec Record
	var err error
	if rec.UserUUID, err = asText(r[colUserUUID], colUserUUID); err != nil {
		return rec, err
	}
	rec.UserUUID = strings.ToLower(rec.UserUUID)
	if rec.Ciphertext, err = asText(r[colCiphertext], colCiphertext); err != nil {
		return rec, err
	}
	if rec.KeyVersion, err = asInt(r[colKeyVersion], colKeyVersion); err != nil {
		return rec, err
	}
	b, ok := r[colMustChange].(bool)
	if !ok {
		return rec, fmt.Errorf("column %s: unexpected value type %T", colMustChange, r[colMustChange])
	}
	rec.MustChange = b
	t, ok := r[colChangedAt].(time.Time)
	if !ok {
		return rec, fmt.Errorf("column %s: unexpected value type %T", colChangedAt, r[colChangedAt])
	}
	rec.ChangedAt = t.UTC()
	return rec, nil
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

func asInt(v interface{}, col string) (int, error) {
	switch x := v.(type) {
	case int64:
		return int(x), nil
	case int32:
		return int(x), nil
	case int:
		return x, nil
	default:
		return 0, fmt.Errorf("column %s: unexpected value type %T", col, v)
	}
}
