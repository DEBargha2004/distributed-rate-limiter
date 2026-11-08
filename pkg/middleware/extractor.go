package middleware

import (
	"net/http"
)

// ExtractIP extracts the client IP address from RemoteAddr or X-Forwarded-For.
func ExtractIP(r *http.Request) string {
	// TODO: Extract client IP
	panic("TODO: implement ExtractIP")
}

// ExtractAPIKey extracts a key from the Authorization or X-API-Key header.
func ExtractAPIKey(r *http.Request) string {
	// TODO: Extract API key
	panic("TODO: implement ExtractAPIKey")
}
