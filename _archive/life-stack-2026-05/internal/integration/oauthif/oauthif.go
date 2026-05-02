package oauthif

import (
	"context"
	"io"
	"net/http"
)

// Requester makes authenticated HTTP requests via OAuth.
type Requester interface {
	Request(ctx context.Context, service, method, url string, body io.Reader) (*http.Response, error)
}

// TokenProvider extends Requester with direct token access.
type TokenProvider interface {
	Requester
	RefreshTokenIfNeeded(service string) (accessToken string, err error)
}
