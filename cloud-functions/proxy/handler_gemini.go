package proxy

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func HandleGeminiNative(client *GeminiClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")

		var model, action string
		if idx := strings.LastIndex(path, ":streamGenerateContent"); idx != -1 {
			model = path[:idx]
			action = "streamGenerateContent"
		} else if idx := strings.LastIndex(path, ":generateContent"); idx != -1 {
			model = path[:idx]
			action = "generateContent"
		} else {
			writeError(w, http.StatusNotFound, "unknown action in path: "+path)
			return
		}

		isStream := action == "streamGenerateContent"
		var geminiPath string
		if isStream {
			geminiPath = fmt.Sprintf("/models/%s:streamGenerateContent?alt=sse", model)
		} else {
			geminiPath = fmt.Sprintf("/models/%s:generateContent", model)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read body")
			return
		}

		token, err := client.pool.getToken()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no available tokens")
			return
		}

		url := fmt.Sprintf("%s%s?key=%s", geminiBaseURL, geminiPath, token)

		proxyReq, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create proxy request")
			return
		}
		proxyReq.Header.Set("Content-Type", "application/json")

		resp, err := client.client.Do(proxyReq)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			client.pool.markRateLimited(token)
			writeError(w, http.StatusTooManyRequests, "rate limited, try another token")
			return
		}

		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		if isStream {
			flusher, ok := w.(http.Flusher)
			if ok {
				buf := make([]byte, 4096)
				for {
					n, err := resp.Body.Read(buf)
					if n > 0 {
						w.Write(buf[:n])
						flusher.Flush()
					}
					if err != nil {
						break
					}
				}
				return
			}
		}

		io.Copy(w, resp.Body)
	}
}
