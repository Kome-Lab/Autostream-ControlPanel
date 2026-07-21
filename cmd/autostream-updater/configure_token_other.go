//go:build !linux

package main

import (
	"context"
	"os"
)

func defaultReadUpdaterConfigureToken(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token, err := readBoundedUpdaterConfigureToken(os.Stdin)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return token, nil
}
