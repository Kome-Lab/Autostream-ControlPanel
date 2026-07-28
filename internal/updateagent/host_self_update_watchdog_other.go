//go:build !linux

package updateagent

import (
	"context"
	"errors"
)

func RecoverHostSelfUpdate(context.Context, string) error {
	return errors.New("host self-update recovery is supported only on Linux")
}
