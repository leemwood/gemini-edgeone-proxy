package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func parseDataURL(url string) (data string, mime string, err error) {
	const prefix = "data:"
	if !strings.HasPrefix(url, prefix) {
		return "", "", fmt.Errorf("not a data URL")
	}
	rest := url[len(prefix):]
	commaIdx := strings.Index(rest, ",")
	if commaIdx == -1 {
		return "", "", fmt.Errorf("invalid data URL")
	}
	mediaPart := rest[:commaIdx]
	data = rest[commaIdx+1:]

	if idx := strings.Index(mediaPart, ";base64"); idx != -1 {
		mime = mediaPart[:idx]
	} else {
		mime = mediaPart
	}
	if mime == "" {
		mime = "image/png"
	}
	return data, mime, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "server_error",
		},
	})
}
