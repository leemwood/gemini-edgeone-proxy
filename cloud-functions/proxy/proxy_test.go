package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	ApiKey = "sk-test"
	handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{"no auth", "", 401},
		{"wrong format", "Token sk-test", 401},
		{"wrong key", "Bearer wrong", 401},
		{"correct key", "Bearer sk-test", 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.wantStatus {
				t.Errorf("got %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestChatRequestParse(t *testing.T) {
	body := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")

	var cr ChatRequest
	if err := json.NewDecoder(req.Body).Decode(&cr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cr.Model != "gemini-2.5-flash" {
		t.Errorf("model: got %q", cr.Model)
	}
	if len(cr.Messages) != 2 {
		t.Fatalf("messages: got %d", len(cr.Messages))
	}
	if s, ok := cr.Messages[0].Content.AsString(); !ok || s != "hi" {
		t.Errorf("msg[0] content: got %q", s)
	}
}

func TestChatToGeminiConversion(t *testing.T) {
	body := `{"model":"gemini-2.5-flash","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"hi"}],"temperature":0.7,"max_tokens":100}`
	var cr ChatRequest
	json.Unmarshal([]byte(body), &cr)

	gemReq, model, err := chatToGemini(&cr)
	if err != nil {
		t.Fatalf("chatToGemini: %v", err)
	}
	if model != "gemini-2.5-flash" {
		t.Errorf("model: got %q", model)
	}
	if gemReq.SystemInstruction == nil {
		t.Error("systemInstruction should not be nil")
	}
	if len(gemReq.Contents) != 1 {
		t.Fatalf("contents: got %d", len(gemReq.Contents))
	}
	if gemReq.Contents[0].Role != "user" {
		t.Errorf("role: got %q", gemReq.Contents[0].Role)
	}
	if gemReq.GenerationConfig == nil || *gemReq.GenerationConfig.Temperature != 0.7 {
		t.Error("generationConfig temperature mismatch")
	}
}

func TestGeminiToChatConversion(t *testing.T) {
	gemResp := &GeminiResponse{
		Candidates: []GeminiCandidate{
			{Content: GeminiContent{Role: "model", Parts: []GeminiPart{{Text: "Hello!"}}}, FinishReason: "STOP"},
		},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 5, CandidatesTokenCount: 2, TotalTokenCount: 7},
	}
	chatResp := geminiToChat(gemResp, "gemini-2.5-flash")
	if len(chatResp.Choices) != 1 {
		t.Fatalf("choices: got %d", len(chatResp.Choices))
	}
	if chatResp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("content: got %q", chatResp.Choices[0].Message.Content)
	}
	if *chatResp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish: got %q", *chatResp.Choices[0].FinishReason)
	}
	if chatResp.Usage.TotalTokens != 7 {
		t.Errorf("tokens: got %d", chatResp.Usage.TotalTokens)
	}
}

func TestResponsesToGeminiConversion(t *testing.T) {
	body := `{"model":"gemini-2.5-flash","instructions":"You are helpful.","input":"hi"}`
	var rr ResponsesRequest
	json.Unmarshal([]byte(body), &rr)

	gemReq, model, err := responsesToGemini(&rr)
	if err != nil {
		t.Fatalf("responsesToGemini: %v", err)
	}
	if model != "gemini-2.5-flash" {
		t.Errorf("model: got %q", model)
	}
	if gemReq.SystemInstruction == nil {
		t.Error("systemInstruction should not be nil")
	}
	if len(gemReq.Contents) != 1 {
		t.Fatalf("contents: got %d", len(gemReq.Contents))
	}
}

func TestResponsesToGeminiConversionArrayInput(t *testing.T) {
	body := `{"model":"gemini-2.5-flash","input":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`
	var rr ResponsesRequest
	json.Unmarshal([]byte(body), &rr)

	gemReq, _, err := responsesToGemini(&rr)
	if err != nil {
		t.Fatalf("responsesToGemini: %v", err)
	}
	if len(gemReq.Contents) != 2 {
		t.Fatalf("contents: got %d", len(gemReq.Contents))
	}
}

func TestGeminiToResponsesConversion(t *testing.T) {
	gemResp := &GeminiResponse{
		Candidates: []GeminiCandidate{
			{Content: GeminiContent{Role: "model", Parts: []GeminiPart{{Text: "Hi!"}}}, FinishReason: "STOP"},
		},
		UsageMetadata: &GeminiUsage{PromptTokenCount: 2, CandidatesTokenCount: 1, TotalTokenCount: 3},
	}
	resp := geminiToResponses(gemResp, "gemini-2.5-flash")
	if len(resp.Output) != 1 {
		t.Fatalf("output: got %d", len(resp.Output))
	}
	if resp.Output[0].Type != "message" {
		t.Errorf("type: got %q", resp.Output[0].Type)
	}
	if resp.Output[0].Content[0].Text != "Hi!" {
		t.Errorf("text: got %q", resp.Output[0].Content[0].Text)
	}
}

func TestParseDataURL(t *testing.T) {
	data, mime, err := parseDataURL("data:image/png;base64,abc123")
	if err != nil {
		t.Fatalf("parseDataURL: %v", err)
	}
	if data != "abc123" {
		t.Errorf("data: got %q", data)
	}
	if mime != "image/png" {
		t.Errorf("mime: got %q", mime)
	}
}

func TestGeminiPathParsing(t *testing.T) {
	tests := []struct {
		path     string
		wantOK   bool
	}{
		{"gemini-2.5-flash:generateContent", true},
		{"gemini-2.5-flash:streamGenerateContent", true},
		{"gemini-2.5-flash:badAction", false},
		{"models/gemini-2.5-flash:generateContent", true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// simulate path parsing
			var ok bool
			if idx := bytesLastIndex([]byte(tt.path), []byte(":streamGenerateContent")); idx != -1 {
				ok = true
			} else if idx := bytesLastIndex([]byte(tt.path), []byte(":generateContent")); idx != -1 {
				ok = true
			}
			if ok != tt.wantOK {
				t.Errorf("got ok=%v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func bytesLastIndex(s, sep []byte) int {
	for i := len(s) - len(sep); i >= 0; i-- {
		if string(s[i:i+len(sep)]) == string(sep) {
			return i
		}
	}
	return -1
}

func TestTokenPool(t *testing.T) {
	t.Setenv("TOKEN1", "key1")
	t.Setenv("TOKEN2", "key2")

	pool := NewTokenPool()
	if pool == nil {
		t.Fatal("pool is nil")
	}
	if len(pool.tokens) != 2 {
		t.Fatalf("tokens: got %d", len(pool.tokens))
	}

	tok, err := pool.getToken()
	if err != nil {
		t.Fatalf("getToken: %v", err)
	}
	if tok == "" {
		t.Error("token is empty")
	}

	pool.markRateLimited(tok)
	tok2, err := pool.getToken()
	if err != nil {
		t.Fatalf("getToken after rate limit: %v", err)
	}
	if tok2 == tok {
		t.Error("should have gotten different token")
	}
}

func TestMultimodalContentConversion(t *testing.T) {
	body := `{"model":"gemini-2.5-flash","messages":[{"role":"user","content":[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc123"}}]}]}`
	var cr ChatRequest
	if err := json.Unmarshal([]byte(body), &cr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	gemReq, _, err := chatToGemini(&cr)
	if err != nil {
		t.Fatalf("chatToGemini: %v", err)
	}
	if len(gemReq.Contents) != 1 {
		t.Fatalf("contents: got %d", len(gemReq.Contents))
	}
	if len(gemReq.Contents[0].Parts) != 2 {
		t.Fatalf("parts: got %d", len(gemReq.Contents[0].Parts))
	}
	if gemReq.Contents[0].Parts[0].Text != "what is this?" {
		t.Errorf("text part: got %q", gemReq.Contents[0].Parts[0].Text)
	}
	if gemReq.Contents[0].Parts[1].InlineData == nil {
		t.Error("inline data should not be nil")
	}
}
