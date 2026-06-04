package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func handleResponses(client *geminiClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ResponsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		gemReq, model, err := responsesToGemini(&req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		body, err := json.Marshal(gemReq)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to marshal request")
			return
		}

		path := fmt.Sprintf("/models/%s:generateContent", model)

		resp, respBody, err := client.doWithRetry(path, body, 2)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}

		if resp.StatusCode != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(respBody)
			return
		}

		var gemResp GeminiResponse
		if err := json.Unmarshal(respBody, &gemResp); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to parse Gemini response")
			return
		}

		respObj := geminiToResponses(&gemResp, model)
		writeJSON(w, http.StatusOK, respObj)
	}
}
