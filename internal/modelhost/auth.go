package modelhost

import "encoding/base64"

// basicAuth builds a Basic scheme value. It lives here so no adapter has to handle the secret
// itself and no host logs it.
func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}
