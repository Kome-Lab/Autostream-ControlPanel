package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// YouTubeRuntimeSaveErrorCode returns a secret-safe, stable classification for
// failures while persisting the short-lived runtime metadata used by stream
// start. It is intentionally independent of the database driver's raw error
// text so callers can put it in logs, audits, and API responses.
func YouTubeRuntimeSaveErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrNotFound):
		return "stream_not_found"
	case isTransientDatabaseConnectionError(err):
		return "database_connection_transient"
	default:
		return "database_write_failed"
	}
}

func isTransientDatabaseConnectionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		switch mysqlErr.Number {
		case 2006, 2013, 2055: // server gone, server lost, extended server lost
			return true
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection reset by peer",
		"broken pipe",
		"server has gone away",
		"server lost",
		"unexpected eof",
		"bad connection",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// IsTransientDatabaseConnectionError reports whether a database operation can
// reasonably be retried because its connection failed transiently. It does not
// expose driver error details and must not be used to classify validation or
// authorization failures.
func IsTransientDatabaseConnectionError(err error) bool {
	return isTransientDatabaseConnectionError(err)
}

func isMariaDBLockConflict(err error) bool {
	var mysqlErr *mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	switch mysqlErr.Number {
	case 1205, 1213: // lock wait timeout, deadlock victim
		return true
	default:
		return false
	}
}

type mariaDBServiceAssignmentConflict struct {
	cause error
}

func (err mariaDBServiceAssignmentConflict) Error() string {
	return ErrServiceAssignmentConflict.Error()
}

func (err mariaDBServiceAssignmentConflict) Unwrap() []error {
	return []error{ErrServiceAssignmentConflict, err.cause}
}

func mariaDBLockConflictAsAssignmentConflict(err error) error {
	if !isMariaDBLockConflict(err) {
		return err
	}
	return mariaDBServiceAssignmentConflict{cause: err}
}
