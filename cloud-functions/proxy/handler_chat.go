package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func HandleChatCompletions(client *GeminiClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		gemReq, model, err := chatToGemini(&req)
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

		if req.Stream {
			handleChatStream(w, client, path, body, model)
			return
		}

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

		chatResp := geminiToChat(&gemResp, model)
		writeJSON(w, http.StatusOK, chatResp)
	}
}

func handleChatStream(w http.ResponseWriter, client *GeminiClient, path string, body []byte, model string) {
	streamPath := strings.Replace(path, ":generateContent", ":streamGenerateContent?alt=sse", 1)

	resp, respBody, _, err := client.do(streamPath, body)
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	created := time.Now().Unix()
	chunkID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())

	scanner := bufio.NewScanner(strings.NewReader(string(respBody)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var gemResp GeminiResponse
		if err := json.Unmarshal([]byte(data), &gemResp); err != nil {
			continue
		}

		chunk := geminiStreamChunkToChatChunk(&gemResp, model, chunkID, created)
		chunkJSON, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", chunkJSON)
		flusher.Flush()
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}
