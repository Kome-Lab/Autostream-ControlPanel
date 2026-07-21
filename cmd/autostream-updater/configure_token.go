package main

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

const updaterConfigureTokenMaxBytes = 4096

func readBoundedUpdaterConfigureToken(input io.Reader) (string, error) {
	reader := bufio.NewReader(io.LimitReader(input, updaterConfigureTokenMaxBytes+2))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", errors.New("read Configure Token from standard input")
	}
	return normalizeUpdaterConfigureToken([]byte(line))
}

func normalizeUpdaterConfigureToken(input []byte) (string, error) {
	token := strings.TrimSpace(string(input))
	if token == "" {
		return "", errors.New("Configure Token is required on standard input")
	}
	if len(token) > updaterConfigureTokenMaxBytes {
		return "", errors.New("Configure Token from standard input is too large")
	}
	return token, nil
}
