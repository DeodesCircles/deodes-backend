package google

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return k
}

func jwksHandler(t *testing.T, kid string, pub *rsa.PublicKey) http.HandlerFunc {
	t.Helper()
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[{"kid":"` + kid + `","n":"` + n + `","e":"` + e + `"}` +
			`],"extra-ignored":true}`))
	}
}

func mintToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

func baseClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":            "https://accounts.google.com",
		"aud":            "test-client-id",
		"sub":            "google-sub-123",
		"email":          "user@example.com",
		"email_verified": true,
		"name":           "Test User",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Unix(),
	}
}

func TestVerifier_OK(t *testing.T) {
	key := testKey(t)
	srv := httptest.NewServer(jwksHandler(t, "kid-1", &key.PublicKey))
	defer srv.Close()

	v := NewVerifier("test-client-id")
	v.SetCertsURL(srv.URL)
	p, err := v.VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", baseClaims()))
	require.NoError(t, err)
	assert.Equal(t, "google-sub-123", p.Sub)
	assert.Equal(t, "user@example.com", p.Email)
	assert.True(t, p.EmailVerified)
	assert.Equal(t, "Test User", p.Name)
}

func TestVerifier_Rejects(t *testing.T) {
	key := testKey(t)
	other := testKey(t)
	srv := httptest.NewServer(jwksHandler(t, "kid-1", &key.PublicKey))
	defer srv.Close()

	newVerifier := func() *Verifier {
		v := NewVerifier("test-client-id")
		v.SetCertsURL(srv.URL)
		return v
	}

	t.Run("wrong audience", func(t *testing.T) {
		claims := baseClaims()
		claims["aud"] = "someone-else"
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", claims))
		assert.ErrorContains(t, err, "audience")
	})

	t.Run("expired", func(t *testing.T) {
		claims := baseClaims()
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", claims))
		assert.Error(t, err)
	})

	t.Run("unknown kid", func(t *testing.T) {
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, key, "nope", baseClaims()))
		assert.ErrorContains(t, err, "unknown signing key")
	})

	t.Run("wrong key", func(t *testing.T) {
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, other, "kid-1", baseClaims()))
		assert.Error(t, err)
	})

	t.Run("bad issuer", func(t *testing.T) {
		claims := baseClaims()
		claims["iss"] = "https://evil.example.com"
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", claims))
		assert.ErrorContains(t, err, "issuer")
	})

	t.Run("unverified email", func(t *testing.T) {
		claims := baseClaims()
		claims["email_verified"] = false
		_, err := newVerifier().VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", claims))
		assert.ErrorContains(t, err, "not verified")
	})

	t.Run("unconfigured", func(t *testing.T) {
		v := NewVerifier("")
		v.SetCertsURL(srv.URL)
		_, err := v.VerifyIDToken(context.Background(), mintToken(t, key, "kid-1", baseClaims()))
		assert.ErrorContains(t, err, "not configured")
	})
}
