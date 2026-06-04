package proxy

import (
	"net/http"
	"os"
	"strings"
)

var ApiKey string

func init() {
	ApiKey = os.Getenv("APIKEY")
}

func HasApiKey() bool {
	return ApiKey != ""
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ApiKey == "" {
			http.Error(w, `{"error":{"message":"server not configured: APIKEY not set","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":{"message":"missing or invalid Authorization header","type":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != ApiKey {
			http.Error(w, `{"error":{"message":"invalid API key","type":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
