//go:build !linux && !windows

package updateagent

import (
	"context"
	"errors"
)

func upgradeHostRuntimeFromVerifiedBundle(
	context.Context,
	ManualHostUpgradeRequest,
) (ManualHostUpgradeResult, error) {
	return ManualHostUpgradeResult{}, errors.New(
		"manual Host runtime upgrade is supported only on Linux",
	)
}
