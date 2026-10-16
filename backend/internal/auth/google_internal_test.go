package auth

import (
	"testing"

	"google.golang.org/api/idtoken"
)

func payload(iss, aud string, claims map[string]any) *idtoken.Payload {
	return &idtoken.Payload{Issuer: iss, Audience: aud, Subject: "s1", Claims: claims}
}

func TestClaimsFromPayloadChecksIssuerAndAudience(t *testing.T) {
	good := map[string]any{"email": "a@b.com", "name": "A", "email_verified": true}
	for _, iss := range []string{"accounts.google.com", "https://accounts.google.com"} {
		c, err := claimsFromPayload(payload(iss, "cid", good), "cid")
		if err != nil || c.Sub != "s1" || c.Email != "a@b.com" || c.Name != "A" || !c.EmailVerified {
			t.Errorf("issuer %q: %+v %v", iss, c, err)
		}
	}
	bad := map[string]*idtoken.Payload{
		"evil issuer":    payload("https://evil.example.com", "cid", good),
		"http issuer":    payload("http://accounts.google.com", "cid", good),
		"empty issuer":   payload("", "cid", good),
		"wrong audience": payload("accounts.google.com", "other", good),
		"nil":            nil,
	}
	for name, p := range bad {
		if _, err := claimsFromPayload(p, "cid"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := claimsFromPayload(payload("accounts.google.com", "", good), ""); err == nil {
		t.Error("empty client id accepted")
	}
}

func TestClaimsFromPayloadEmailVerifiedForms(t *testing.T) {
	cases := map[string]struct {
		v    any
		want bool
	}{"true": {true, true}, "false": {false, false}, "string true": {"true", true}, "string false": {"false", false}, "missing": {nil, false}, "number": {1, false}}
	for name, tc := range cases {
		claims := map[string]any{"email": "a@b.com"}
		if tc.v != nil {
			claims["email_verified"] = tc.v
		}
		c, err := claimsFromPayload(payload("accounts.google.com", "cid", claims), "cid")
		if err != nil || c.EmailVerified != tc.want {
			t.Errorf("%s: %+v %v", name, c, err)
		}
	}
}

func TestProductionVerifierWithoutClientIDFails(t *testing.T) {
	if _, err := NewIDTokenVerifier("").Verify(t.Context(), "tok"); err == nil {
		t.Error("want error")
	}
}
