package google

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// googleCertsURL serves Google's public signing keys (JWK set) for ID tokens.
const googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"

// certsTTL bounds how long fetched keys are reused before refetching.
const certsTTL = time.Hour

// Profile is the verified identity inside a Google ID token.
type Profile struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
}

// Verifier checks Google ID tokens against Google's public keys and the
// expected audience (our OAuth client ID). Keys are cached for certsTTL;
// an unknown kid forces one refetch to survive rotations without restart.
type Verifier struct {
	clientID  string
	certsURL  string
	http      *http.Client
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// NewVerifier builds a verifier. Empty clientID fails fast at Verify time
// (fail-closed) rather than at construction, so wiring never panics.
func NewVerifier(clientID string) *Verifier {
	return &Verifier{
		clientID: clientID,
		certsURL: googleCertsURL,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// SetHTTPClient overrides transport. Intended for tests.
func (v *Verifier) SetHTTPClient(c *http.Client) { v.http = c }

// SetCertsURL overrides the JWK endpoint. Intended for tests.
func (v *Verifier) SetCertsURL(url string) { v.certsURL = url }

// VerifyIDToken validates raw (signature, issuer, audience, expiry) and
// returns the profile. Any failure is an error — callers treat it as 401.
func (v *Verifier) VerifyIDToken(ctx context.Context, raw string) (*Profile, error) {
	if v.clientID == "" {
		return nil, fmt.Errorf("google login is not configured")
	}
	token, err := jwt.ParseWithClaims(raw, jwt.MapClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method %q", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		return v.keyForKid(ctx, kid)
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}))
	if err != nil {
		return nil, fmt.Errorf("invalid google id token: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid google id token claims")
	}
	iss, _ := claims["iss"].(string)
	if iss != "accounts.google.com" && iss != "https://accounts.google.com" {
		return nil, fmt.Errorf("invalid token issuer %q", iss)
	}
	if !audienceMatches(claims["aud"], v.clientID) {
		return nil, fmt.Errorf("token audience mismatch")
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil || time.Now().After(exp.Time) {
		return nil, fmt.Errorf("token expired")
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	if sub == "" || email == "" {
		return nil, fmt.Errorf("token missing sub or email")
	}
	verified, _ := claims["email_verified"].(bool)
	if !verified {
		return nil, fmt.Errorf("google email is not verified")
	}
	name, _ := claims["name"].(string)
	return &Profile{Sub: sub, Email: email, EmailVerified: true, Name: name}, nil
}

func audienceMatches(aud any, clientID string) bool {
	switch a := aud.(type) {
	case string:
		return a == clientID
	case []any:
		for _, v := range a {
			if s, ok := v.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

func (v *Verifier) keyForKid(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.keys != nil && time.Since(v.fetchedAt) < certsTTL {
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
	}
	// Unknown or stale kid: refetch once (covers Google key rotations).
	keys, err := v.fetchKeys(ctx)
	if err != nil {
		return nil, err
	}
	v.keys = keys
	v.fetchedAt = time.Now()
	k, ok := keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	return k, nil
}

type jwkSet struct {
	Keys []struct {
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

func (v *Verifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.certsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching google certs: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("google certs endpoint returned %d", resp.StatusCode)
	}
	var set jwkSet
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, fmt.Errorf("decoding google certs: %w", err)
	}
	out := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		e := 0
		for _, b := range eBytes {
			e = e*256 + int(b)
		}
		if k.Kid == "" || e <= 0 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no usable google signing keys")
	}
	return out, nil
}
