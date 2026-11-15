package user_photo

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

// Database runs a transaction. *databases.DXDatabase implements it.
type Database interface {
	Tx(ctx context.Context, log *log.DXLog, isolationLevel sql.IsolationLevel, callback databases.DXDatabaseTxCallback) error
}

// DBStore keeps the photo rows in a PostgreSQL table the service names, through dxlib's databases package. Every
// statement is built by dxlib from fixed column names with the values bound as parameters. The table (README,
// "User photos"):
//
//	CREATE TABLE <schema>.<table> (
//	    user_uuid  uuid PRIMARY KEY,
//	    object_key text NOT NULL,        -- '' once the photo was removed
//	    version    bigint NOT NULL,
//	    changed_at timestamptz NOT NULL
//	);
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
	colUserUUID  = "user_uuid"
	colObjectKey = "object_key"
	colVersion   = "version"
	colChangedAt = "changed_at"
)

var allColumns = []string{colUserUUID, colObjectKey, colVersion, colChangedAt}

// NewDBStore returns a store on the table, named "table" or "schema.table" in lower-case letters, digits and _.
func NewDBStore(database Database, table string) (*DBStore, error) {
	if database == nil {
		return nil, errors.New("user_photo: NewDBStore needs a Database")
	}
	if !tableNamePattern.MatchString(table) {
		return nil, fmt.Errorf("user_photo: table name %q must be table or schema.table, each a lower-case letter or _ followed by up to 62 lower-case letters, digits or _", truncate(table, 140))
	}
	return &DBStore{db: database, table: table, Log: &log.Log}, nil
}

// tx runs fn in a READ COMMITTED transaction. A refusal (not found) comes back in result, so it commits a
// transaction that wrote nothing instead of being logged by dxlib as a database error.
func (s *DBStore) tx(ctx context.Context, op string, fn func(dtx *databases.DXDatabaseTx) (result error, err error)) error {
	var result error
	err := s.db.Tx(ctx, s.Log, sql.LevelReadCommitted, func(dtx *databases.DXDatabaseTx) error {
		r, err := fn(dtx)
		result = r
		return err
	})
	if err != nil {
		return fmt.Errorf("user photo store %s on table %s: %w", op, s.table, err)
	}
	return result
}

func (s *DBStore) row(ctx context.Context, dtx *databases.DXDatabaseTx, userUUID string, forUpdate bool) (*Record, error) {
	_, rr, err := dtx.Select(ctx, s.table, nil, allColumns, utils.JSON{colUserUUID: userUUID}, nil, nil, nil,
		db.DXDatabaseTableFieldsOrderBy{colUserUUID: "asc"}, nil, nil, forUpdate)
	if err != nil {
		return nil, err
	}
	if len(rr) == 0 {
		return nil, nil
	}
	rec, err := rowToRecord(rr[0])
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Get returns the user's row.
func (s *DBStore) Get(ctx context.Context, userUUID string) (*Record, error) {
	var found *Record
	err := s.tx(ctx, "get", func(dtx *databases.DXDatabaseTx) (error, error) {
		r, err := s.row(ctx, dtx, userUUID, false)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return ErrNotFound, nil
		}
		found = r
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// Replace points the user's row at objectKey with the next version, the row locked from the read to the write.
func (s *DBStore) Replace(ctx context.Context, userUUID, objectKey string, at time.Time) (*Record, Record, error) {
	var before *Record
	var after Record
	err := s.tx(ctx, "replace", func(dtx *databases.DXDatabaseTx) (error, error) {
		cur, err := s.row(ctx, dtx, userUUID, true)
		if err != nil {
			return nil, err
		}
		if objectKey == "" && (cur == nil || cur.ObjectKey == "") {
			return ErrNotFound, nil
		}
		next := Record{UserUUID: userUUID, ObjectKey: objectKey, Version: 1, ChangedAt: at.UTC().Truncate(time.Microsecond)}
		values := utils.JSON{colObjectKey: next.ObjectKey, colChangedAt: next.ChangedAt}
		if cur != nil {
			next.Version = cur.Version + 1
			values[colVersion] = next.Version
			res, _, err := dtx.Update(ctx, s.table, values, utils.JSON{colUserUUID: userUUID}, nil)
			if err != nil {
				return nil, err
			}
			if err := oneRow(res, "updating", userUUID); err != nil {
				return nil, err
			}
		} else {
			values[colVersion] = next.Version
			values[colUserUUID] = userUUID
			if _, _, err := dtx.Insert(ctx, s.table, values, nil); err != nil {
				if isUniqueViolation(err) {
					// Another transaction stored this user's first row between the select and the insert. The
					// failed insert aborted the transaction, so the conflict goes back as the callback's error.
					return nil, fmt.Errorf("%w: %v", ErrConflict, err)
				}
				return nil, err
			}
		}
		before, after = cur, next
		return nil, nil
	})
	if err != nil {
		return nil, Record{}, err
	}
	return before, after, nil
}

// Delete removes the user's row and returns it.
func (s *DBStore) Delete(ctx context.Context, userUUID string) (*Record, error) {
	var found *Record
	err := s.tx(ctx, "delete", func(dtx *databases.DXDatabaseTx) (error, error) {
		cur, err := s.row(ctx, dtx, userUUID, true)
		if err != nil {
			return nil, err
		}
		if cur == nil {
			return ErrNotFound, nil
		}
		// databases/db.TxDelete, not DXDatabaseTx.TxDelete: dxlib v1.133.0's method has its error check inverted
		// (databases/database_tx_delete.go:12).
		res, _, err := db.TxDelete(ctx, dtx.Tx, s.table, utils.JSON{colUserUUID: userUUID}, nil)
		if err != nil {
			return nil, err
		}
		if err := oneRow(res, "deleting", userUUID); err != nil {
			return nil, err
		}
		found = cur
		return nil, nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func isUniqueViolation(err error) bool { return err != nil && strings.Contains(err.Error(), "23505") }

func oneRow(res sql.Result, what, userUUID string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%s the photo row of user %s touched %d rows, not 1", what, userUUID, n)
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
	if rec.ObjectKey, err = asText(r[colObjectKey], colObjectKey); err != nil {
		return rec, err
	}
	switch v := r[colVersion].(type) {
	case int64:
		rec.Version = v
	case int32:
		rec.Version = int64(v)
	case int:
		rec.Version = int64(v)
	default:
		return rec, fmt.Errorf("column %s: unexpected value type %T", colVersion, r[colVersion])
	}
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
