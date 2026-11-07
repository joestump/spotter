package subsonic

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToken(t *testing.T) {
	password := "mypassword"
	salt := "abcd1234"

	token := Token(password, salt)

	// The Subsonic API defines the token as hex(md5(password + salt)).
	sum := md5.Sum([]byte(password + salt))
	assert.Equal(t, hex.EncodeToString(sum[:]), token)
	assert.Len(t, token, 32)
}

func TestSaltIsRandom(t *testing.T) {
	first := Salt()
	second := Salt()

	assert.Len(t, first, saltBytes*2)
	assert.NotEqual(t, first, second, "each request needs a fresh salt")
}

func TestAuthParams(t *testing.T) {
	creds := Credentials{Username: "alice", Password: "s3cret"}

	params := creds.AuthParams(nil)

	salt := params.Get("s")
	require.NotEmpty(t, salt)
	assert.Equal(t, "alice", params.Get("u"))
	assert.Equal(t, Token("s3cret", salt), params.Get("t"))
	assert.Equal(t, APIVersion, params.Get("v"))
	assert.Equal(t, ClientName, params.Get("c"))
	assert.Equal(t, "json", params.Get("f"))
}

func TestAuthParamsKeepsCallerParams(t *testing.T) {
	creds := Credentials{Username: "alice", Password: "s3cret"}
	params := url.Values{}
	params.Set("playlistId", "pl-1")

	params = creds.AuthParams(params)

	assert.Equal(t, "pl-1", params.Get("playlistId"))
	assert.NotEmpty(t, params.Get("t"))
}

// A caller that supplies its own salt (a stable cover-art URL, for example) gets
// the same token for the same password and salt.
func TestParamsWithFixedSaltIsDeterministic(t *testing.T) {
	creds := Credentials{Username: "alice", Password: "s3cret"}

	first := creds.Params("static", nil).Encode()
	second := creds.Params("static", nil).Encode()

	assert.Equal(t, first, second)
	assert.NotContains(t, first, "f=json", "no response format is requested without AuthParams")
}
