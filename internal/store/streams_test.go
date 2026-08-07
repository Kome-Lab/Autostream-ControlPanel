package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestNewUUIDShape(t *testing.T) {
	id := newUUID()
	if len(id) != 36 || strings.Count(id, "-") != 4 {
		t.Fatalf("bad uuid: %s", id)
	}
}

func TestYouTubeRuntimeSaveErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "stream not found", err: ErrNotFound, want: "stream_not_found"},
		{name: "driver bad connection", err: driver.ErrBadConn, want: "database_connection_transient"},
		{name: "mysql connection lost", err: &mysql.MySQLError{Number: 2013, Message: "Lost connection"}, want: "database_connection_transient"},
		{name: "connection reset", err: errors.New("write tcp: connection reset by peer"), want: "database_connection_transient"},
		{name: "deadline", err: context.DeadlineExceeded, want: "database_write_failed"},
		{name: "write failure", err: errors.New("duplicate key"), want: "database_write_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := YouTubeRuntimeSaveErrorCode(test.err); got != test.want {
				t.Fatalf("YouTubeRuntimeSaveErrorCode(%v) = %q, want %q", test.err, got, test.want)
			}
		})
	}
}
