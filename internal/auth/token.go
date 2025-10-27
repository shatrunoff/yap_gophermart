package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Signer — HMAC-подписчик токенов.
type Signer struct {
	secret []byte
	ttl    time.Duration
}

// NewSigner создаёт подписчик.
// ttl — время жизни токена.
func NewSigner(secret []byte, ttl time.Duration) *Signer {
	return &Signer{secret: secret, ttl: ttl}
}

// Token содержит полезную нагрузку токена.
type Token struct {
	UserID int64
	Exp    time.Time
}

// Encode генерирует компактный токен: base64(payload).base64(mac)
func (s *Signer) Encode(t Token) (string, error) {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[0:8], uint64(t.UserID))
	binary.BigEndian.PutUint64(payload[8:16], uint64(t.Exp.Unix()))
	mac := s.mac(payload)
	return fmt.Sprintf("%s.%s", base64.RawURLEncoding.EncodeToString(payload), base64.RawURLEncoding.EncodeToString(mac)), nil
}

// Decode проверяет подпись и срок действия и возвращает полезную нагрузку.
func (s *Signer) Decode(token string) (Token, error) {
	var out Token
	var payloadB64, macB64 string
	n, _ := fmt.Sscanf(token, "%s.%s", &payloadB64, &macB64)
	if n != 2 {
		// Разделим вручную, т.к. Scan с %s не разделит по '.'
		dot := -1
		for i := 0; i < len(token); i++ {
			if token[i] == '.' {
				dot = i
				break
			}
		}
		if dot < 0 {
			return out, errors.New("invalid token format")
		}
		payloadB64 = token[:dot]
		macB64 = token[dot+1:]
	}

	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil || len(payload) != 16 {
		return out, errors.New("invalid token payload")
	}
	mac, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return out, errors.New("invalid token mac")
	}
	if !hmac.Equal(s.mac(payload), mac) {
		return out, errors.New("invalid token signature")
	}
	uid := int64(binary.BigEndian.Uint64(payload[0:8]))
	exp := int64(binary.BigEndian.Uint64(payload[8:16]))
	if time.Now().Unix() > exp {
		return out, errors.New("token expired")
	}
	out.UserID = uid
	out.Exp = time.Unix(exp, 0)
	return out, nil
}

func (s *Signer) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, s.secret)
	h.Write(payload)
	sum := h.Sum(nil)
	// Усечём до 16 байт
	return sum[:16]
}
