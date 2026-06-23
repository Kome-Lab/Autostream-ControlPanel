package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const totpStep = 30 * time.Second

func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func TOTPCode(secret string, now time.Time) (string, error) {
	return totpCodeForCounter(secret, uint64(now.UTC().Unix()/int64(totpStep.Seconds())))
}

func VerifyTOTP(secret, code string, now time.Time) bool {
	code = normalizeTOTPCode(code)
	if len(code) != 6 {
		return false
	}
	counter := now.UTC().Unix() / int64(totpStep.Seconds())
	for offset := int64(-1); offset <= 1; offset++ {
		expected, err := totpCodeForCounter(secret, uint64(counter+offset))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return true
		}
	}
	return false
}

func ProvisioningURI(issuer, account, secret string) string {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		issuer = "AutoStream"
	}
	account = strings.TrimSpace(account)
	if account == "" {
		account = "user"
	}
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", strings.TrimSpace(secret))
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	return "otpauth://totp/" + label + "?" + query.Encode()
}

func GenerateRecoveryCodes(count int) ([]string, error) {
	if count <= 0 {
		count = 10
	}
	codes := make([]string, 0, count)
	for len(codes) < count {
		raw := make([]byte, 9)
		if _, err := rand.Read(raw); err != nil {
			return nil, err
		}
		encoded := strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
		codes = append(codes, encoded[:4]+"-"+encoded[4:8]+"-"+encoded[8:12])
	}
	return codes, nil
}

func HashRecoveryCode(code string) string {
	return HashToken(normalizeRecoveryCode(code))
}

func normalizeTOTPCode(code string) string {
	return strings.ReplaceAll(strings.TrimSpace(code), " ", "")
}

func normalizeRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

func totpCodeForCounter(secret string, counter uint64) (string, error) {
	secret = strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(secret, " ", "")))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	binaryCode := int(sum[offset]&0x7f)<<24 |
		int(sum[offset+1]&0xff)<<16 |
		int(sum[offset+2]&0xff)<<8 |
		int(sum[offset+3]&0xff)
	return fmt.Sprintf("%06d", binaryCode%1000000), nil
}
