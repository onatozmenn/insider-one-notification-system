package requestid

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

type contextKey struct{}

const header = "X-Request-ID"

// Middleware keeps a client-provided request ID or generates a new one.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(header)
		if id == "" {
			id = uuid.NewString()
		}

		w.Header().Set(header, id)
		ctx := context.WithValue(r.Context(), contextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FromContext returns the request ID stored by Middleware.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
