package httpapi

import (
	"net/http"

	"github.com/example/autostream-control-panel/internal/mediaassets"
)

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func limitRequestBody(next http.Handler, maximum int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && isUnsafeMethod(r.Method) {
			requestLimit := maximum
			if r.Method == http.MethodPost && r.URL.Path == "/media-assets" {
				requestLimit = mediaassets.MaxUploadBytes + (1 << 20)
			}
			r.Body = http.MaxBytesReader(w, r.Body, requestLimit)
		}
		next.ServeHTTP(w, r)
	})
}
