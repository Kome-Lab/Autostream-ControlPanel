//go:build !linux

package updateagent

import (
	"context"
	"errors"
)

func ServeLocalExecutor(context.Context, string) error {
	return errors.New("local executor requires Linux")
}
