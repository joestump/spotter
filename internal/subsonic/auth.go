// Governing: SPEC music-provider-integration REQ-PROV-051 (Subsonic API protocol, random salt per request), ADR-0005 (Navidrome credentials)
//
// Package subsonic builds the authentication parameters the Subsonic API
// (Navidrome) expects. Both the Navidrome provider and the Navidrome enricher
// call the Subsonic REST API, and they must derive the token the same way or the
// server sees two different clients for one account.
package subsonic

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"
)

const (
	// APIVersion is the Subsonic protocol version spotter targets.
	APIVersion = "1.16.1"
	// ClientName identifies spotter to the server in the "c" parameter.
	ClientName = "spotter"

	// saltBytes is the length of the random salt; hex-encoded it is twice as long.
	saltBytes = 8
)

// Credentials are the Subsonic credentials of one user: their username and their
// password as stored on the user's Navidrome auth record.
type Credentials struct {
	Username string
	Password string
}

// Params returns params with the Subsonic auth and protocol parameters for the
// given salt (u, t, s, v, c). A nil params starts a fresh set; params already
// present are kept, so callers can add their own endpoint parameters first.
func (c Credentials) Params(salt string, params url.Values) url.Values {
	if params == nil {
		params = url.Values{}
	}
	params.Set("u", c.Username)
	params.Set("t", Token(c.Password, salt))
	params.Set("s", salt)
	params.Set("v", APIVersion)
	params.Set("c", ClientName)
	return params
}

// AuthParams returns params with auth parameters built from a fresh random salt
// and the JSON response format requested. Every Subsonic endpoint that returns
// JSON wants this.
func (c Credentials) AuthParams(params url.Values) url.Values {
	params = c.Params(Salt(), params)
	params.Set("f", "json")
	return params
}

// Token derives the Subsonic auth token: hex(md5(password + salt)). The Subsonic
// protocol defines this digest; it is what the server compares against, so it
// cannot be replaced with a stronger hash without breaking authentication.
func Token(password, salt string) string {
	sum := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(sum[:])
}

// Salt returns a random hex salt. The protocol wants a fresh one per request, so
// a replayed token cannot be reused.
func Salt() string {
	b := make([]byte, saltBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is not expected; a time-derived salt keeps the
		// request working rather than failing the sync.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
