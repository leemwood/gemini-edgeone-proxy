package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func handleModels(client *geminiClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := client.pool.getToken()
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no available tokens")
			return
		}

		url := fmt.Sprintf("%s/models?key=%s", geminiBaseURL, token)
		resp, err := client.client.Get(url)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusTooManyRequests {
			client.pool.markRateLimited(token)
			writeError(w, http.StatusTooManyRequests, "rate limited")
			return
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read models")
			return
		}

		var geminiModels struct {
			Models []struct {
				Name        string `json:"name"`
				DisplayName string `json:"displayName"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &geminiModels); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
			return
		}

		type openaiModel struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}
		var models []openaiModel
		for _, m := range geminiModels.Models {
			name := m.Name
			if len(name) > 7 && name[:7] == "models/" {
				name = name[7:]
			}
			models = append(models, openaiModel{
				ID:      name,
				Object:  "model",
				Created: 1686935002,
				OwnedBy: "google",
			})
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   models,
		})
	}
}
