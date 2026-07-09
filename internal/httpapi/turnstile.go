package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const turnstileSiteverifyEndpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

var (
	errTurnstileFailed      = errors.New("turnstile_failed")
	errTurnstileUnavailable = errors.New("turnstile_unavailable")
)

type TurnstileVerifier interface {
	Verify(ctx context.Context, req TurnstileVerifyRequest) (TurnstileVerifyResult, error)
}

type TurnstileVerifyRequest struct {
	Secret   string
	Token    string
	RemoteIP string
}

type TurnstileVerifyResult struct {
	Success    bool
	ErrorCodes []string
	Hostname   string
	Action     string
}

type HTTPSTurnstileVerifier struct {
	Client   *http.Client
	Endpoint string
}

func (v HTTPSTurnstileVerifier) Verify(ctx context.Context, req TurnstileVerifyRequest) (TurnstileVerifyResult, error) {
	secret := strings.TrimSpace(req.Secret)
	token := strings.TrimSpace(req.Token)
	if secret == "" || token == "" || len(token) > 2048 {
		return TurnstileVerifyResult{}, errTurnstileFailed
	}
	endpoint := strings.TrimSpace(v.Endpoint)
	if endpoint == "" {
		endpoint = turnstileSiteverifyEndpoint
	}
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	payload := map[string]string{
		"secret":   secret,
		"response": token,
	}
	if remoteIP := strings.TrimSpace(req.RemoteIP); remoteIP != "" {
		payload["remoteip"] = remoteIP
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TurnstileVerifyResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return TurnstileVerifyResult{}, errTurnstileUnavailable
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := client.Do(httpReq)
	if err != nil {
		return TurnstileVerifyResult{}, errTurnstileUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1024))
		return TurnstileVerifyResult{}, fmt.Errorf("%w: status %d", errTurnstileUnavailable, res.StatusCode)
	}
	var decoded struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
		Hostname   string   `json:"hostname"`
		Action     string   `json:"action"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16*1024)).Decode(&decoded); err != nil {
		return TurnstileVerifyResult{}, errTurnstileUnavailable
	}
	return TurnstileVerifyResult{
		Success:    decoded.Success,
		ErrorCodes: decoded.ErrorCodes,
		Hostname:   decoded.Hostname,
		Action:     decoded.Action,
	}, nil
}
