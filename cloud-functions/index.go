package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// === auth.go ===


var apiKey string

func init() {
	apiKey = os.Getenv("APIKEY")
}

func hasApiKey() bool {
	return apiKey != ""
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiKey == "" {
			http.Error(w, `{"error":{"message":"server not configured: APIKEY not set","type":"server_error"}}`, http.StatusInternalServerError)
			return
		}
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":{"message":"missing or invalid Authorization header","type":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != apiKey {
			http.Error(w, `{"error":{"message":"invalid API key","type":"unauthorized"}}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// === token_manager.go ===


type tokenState struct {
	key      string
	cooldown time.Time
}

type tokenPool struct {
	mu     sync.Mutex
	tokens []tokenState
}

func newTokenPool() *tokenPool {
	var tokens []tokenState
	for _, env := range os.Environ() {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "TOKEN") && v != "" {
			tokens = append(tokens, tokenState{key: v})
		}
	}
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].key < tokens[j].key
	})
	if len(tokens) == 0 {
		return nil
	}
	return &tokenPool{tokens: tokens}
}

func (p *tokenPool) getToken() (string, error) {
	if p == nil {
		return "", fmt.Errorf("no tokens configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for i := range p.tokens {
		ts := &p.tokens[i]
		if now.After(ts.cooldown) {
			ts.cooldown = time.Time{}
			return ts.key, nil
		}
	}
	return "", fmt.Errorf("all tokens in cooldown")
}

func (p *tokenPool) markRateLimited(token string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	for i := range p.tokens {
		if p.tokens[i].key == token {
			p.tokens[i].cooldown = time.Now().Add(60 * time.Second)
			return
		}
	}
}

func (p *tokenPool) count() int {
	if p == nil {
		return 0
	}
	return len(p.tokens)
}

// === gemini_client.go ===


const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type geminiClient struct {
	pool   *tokenPool
	client *http.Client
}

func newGeminiClient(pool *tokenPool) *geminiClient {
	return &geminiClient{
		pool: pool,
		client: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (c *geminiClient) do(path string, body []byte) (*http.Response, []byte, string, error) {
	token, err := c.pool.getToken()
	if err != nil {
		return nil, nil, "", fmt.Errorf("token error: %w", err)
	}

	url := fmt.Sprintf("%s%s?key=%s", geminiBaseURL, path, token)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, token, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, token, err
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		c.pool.markRateLimited(token)
		resp.Body.Close()
		return nil, nil, token, fmt.Errorf("rate limited, token rotated")
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, token, err
	}

	return resp, respBody, token, nil
}

func (c *geminiClient) doWithRetry(path string, body []byte, maxRetries int) (*http.Response, []byte, error) {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		resp, respBody, _, err := c.do(path, body)
		if err != nil {
			lastErr = err
			if i < maxRetries {
				continue
			}
			return nil, nil, lastErr
		}
		return resp, respBody, nil
	}
	return nil, nil, lastErr
}

// === util.go ===


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

// === convert_chat.go ===


// --- OpenAI Chat Completions types ---

type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ChatMessage   `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []ChatTool      `json:"tools,omitempty"`
}

type ChatMessage struct {
	Role    string        `json:"role"`
	Content ChatContent   `json:"content"`
	Name    string        `json:"name,omitempty"`
}

type ChatContent json.RawMessage

func (c *ChatContent) UnmarshalJSON(data []byte) error {
	*c = ChatContent(data)
	return nil
}

func (c *ChatContent) AsString() (string, bool) {
	var s string
	if err := json.Unmarshal(*c, &s); err != nil {
		return "", false
	}
	return s, true
}

func (c *ChatContent) AsParts() ([]ChatContentPart, bool) {
	var parts []ChatContentPart
	if err := json.Unmarshal(*c, &parts); err != nil {
		return nil, false
	}
	return parts, true
}

type ChatContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type ChatTool struct {
	Type     string           `json:"type"`
	Function *json.RawMessage `json:"function,omitempty"`
}

type ChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []ChatChoice   `json:"choices"`
	Usage   *ChatUsage     `json:"usage,omitempty"`
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      *ChatRespMsg `json:"message,omitempty"`
	Delta        *ChatRespDelta `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason"`
}

type ChatRespMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRespDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Gemini types ---

type GeminiRequest struct {
	SystemInstruction *GeminiContent   `json:"systemInstruction,omitempty"`
	Contents          []GeminiContent  `json:"contents"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	Tools             []GeminiTool     `json:"tools,omitempty"`
	SafetySettings    []SafetySetting  `json:"safetySettings,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text       string     `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GenerationConfig struct {
	Temperature     *float64        `json:"temperature,omitempty"`
	MaxOutputTokens *int            `json:"maxOutputTokens,omitempty"`
	TopP            *float64        `json:"topP,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
}

type GeminiTool struct {
	FunctionDeclarations []FunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type FunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type GeminiResponse struct {
	Candidates    []GeminiCandidate `json:"candidates"`
	UsageMetadata *GeminiUsage      `json:"usageMetadata,omitempty"`
}

type GeminiCandidate struct {
	Content      GeminiContent    `json:"content"`
	FinishReason string           `json:"finishReason,omitempty"`
}

type GeminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// --- Streaming chunk for OpenAI SSE ---

type ChatStreamChunk struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []ChatStreamChoice `json:"choices"`
}

type ChatStreamChoice struct {
	Index        int             `json:"index"`
	Delta        ChatRespDelta   `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

// --- Conversion: Chat Request → Gemini Request ---

func chatToGemini(req *ChatRequest) (*GeminiRequest, string, error) {
	var systemParts []GeminiPart
	var contents []GeminiContent

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			if s, ok := msg.Content.AsString(); ok {
				systemParts = append(systemParts, GeminiPart{Text: s})
			}
			continue
		}

		role := msg.Role
		if role == "assistant" {
			role = "model"
		}

		parts, err := contentToParts(&msg.Content)
		if err != nil {
			return nil, "", err
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, GeminiContent{Role: role, Parts: parts})
	}

	gemReq := &GeminiRequest{
		Contents: contents,
	}
	if len(systemParts) > 0 {
		gemReq.SystemInstruction = &GeminiContent{Parts: systemParts}
	}

	if req.Temperature != nil || req.MaxTokens != nil || req.TopP != nil {
		cfg := &GenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
			TopP:            req.TopP,
		}
		if req.Stop != nil {
			var stops []string
			if err := json.Unmarshal(req.Stop, &stops); err == nil {
				cfg.StopSequences = stops
			}
		}
		gemReq.GenerationConfig = cfg
	}

	for _, t := range req.Tools {
		if t.Type == "function" && t.Function != nil {
			var fd FunctionDeclaration
			if err := json.Unmarshal(*t.Function, &fd); err != nil {
				continue
			}
			gemReq.Tools = append(gemReq.Tools, GeminiTool{
				FunctionDeclarations: []FunctionDeclaration{fd},
			})
		}
	}

	model := req.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}

	return gemReq, model, nil
}

func contentToParts(content *ChatContent) ([]GeminiPart, error) {
	if s, ok := content.AsString(); ok {
		if s == "" {
			return nil, nil
		}
		return []GeminiPart{{Text: s}}, nil
	}

	parts, ok := content.AsParts()
	if !ok {
		return nil, fmt.Errorf("unsupported content type")
	}

	var out []GeminiPart
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, GeminiPart{Text: p.Text})
		case "image_url":
			if p.ImageURL != nil {
				data, mime, err := parseDataURL(p.ImageURL.URL)
				if err != nil {
					return nil, fmt.Errorf("invalid image_url: %w", err)
				}
				out = append(out, GeminiPart{InlineData: &InlineData{
					MimeType: mime,
					Data:     data,
				}})
			}
		}
	}
	return out, nil
}

// --- Conversion: Gemini Response → Chat Response ---

func geminiToChat(gemResp *GeminiResponse, model string) *ChatResponse {
	resp := &ChatResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
	}

	for i, cand := range gemResp.Candidates {
		text := extractText(cand.Content)
		finish := mapFinishReason(cand.FinishReason)
		resp.Choices = append(resp.Choices, ChatChoice{
			Index: i,
			Message: &ChatRespMsg{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: &finish,
		})
	}

	if gemResp.UsageMetadata != nil {
		resp.Usage = &ChatUsage{
			PromptTokens:     gemResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gemResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gemResp.UsageMetadata.TotalTokenCount,
		}
	}

	return resp
}

func geminiStreamChunkToChatChunk(gemResp *GeminiResponse, model string, chunkID string, created int64) *ChatStreamChunk {
	chunk := &ChatStreamChunk{
		ID:      chunkID,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
	}

	for _, cand := range gemResp.Candidates {
		text := extractText(cand.Content)
		finish := mapFinishReason(cand.FinishReason)
		choice := ChatStreamChoice{
			Index: 0,
			Delta: ChatRespDelta{Content: text},
		}
		if finish != "" {
			choice.FinishReason = &finish
		}
		chunk.Choices = append(chunk.Choices, choice)
	}

	return chunk
}

func extractText(content GeminiContent) string {
	for _, p := range content.Parts {
		if p.Text != "" {
			return p.Text
		}
	}
	return ""
}

func mapFinishReason(r string) string {
	switch r {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	case "RECITATION":
		return "content_filter"
	default:
		return "stop"
	}
}

// === convert_responses.go ===


// --- OpenAI Responses API types ---

type ResponsesRequest struct {
	Model        string           `json:"model"`
	Instructions *string          `json:"instructions,omitempty"`
	Input        ResponsesInput   `json:"input"`
	Tools        []ResponsesTool  `json:"tools,omitempty"`
	Temperature  *float64         `json:"temperature,omitempty"`
	MaxOutputTokens *int          `json:"max_output_tokens,omitempty"`
	TopP         *float64         `json:"top_p,omitempty"`
	Stream       bool             `json:"stream,omitempty"`
}

type ResponsesInput json.RawMessage

func (ri *ResponsesInput) UnmarshalJSON(data []byte) error {
	*ri = ResponsesInput(data)
	return nil
}

type ResponsesTool struct {
	Type     string          `json:"type"`
	Name     string          `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
}

type ResponsesResponse struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Model     string            `json:"model"`
	Output    []ResponsesOutput `json:"output"`
	Usage     *ResponsesUsage   `json:"usage,omitempty"`
}

type ResponsesOutput struct {
	ID      string              `json:"id"`
	Type    string              `json:"type"`
	Status  string              `json:"status"`
	Role    string              `json:"role"`
	Content []ResponsesContentPart `json:"content"`
}

type ResponsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// --- Conversion: Responses Request → Gemini Request ---

func responsesToGemini(req *ResponsesRequest) (*GeminiRequest, string, error) {
	gemReq := &GeminiRequest{}

	if req.Instructions != nil && *req.Instructions != "" {
		gemReq.SystemInstruction = &GeminiContent{
			Parts: []GeminiPart{{Text: *req.Instructions}},
		}
	}

	contents, err := responsesInputToContents(req.Input)
	if err != nil {
		return nil, "", err
	}
	gemReq.Contents = contents

	if req.Temperature != nil || req.MaxOutputTokens != nil || req.TopP != nil {
		gemReq.GenerationConfig = &GenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxOutputTokens,
			TopP:            req.TopP,
		}
	}

	for _, t := range req.Tools {
		if t.Type == "function" {
			gemReq.Tools = append(gemReq.Tools, GeminiTool{
				FunctionDeclarations: []FunctionDeclaration{{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				}},
			})
		}
	}

	model := req.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}

	return gemReq, model, nil
}

func responsesInputToContents(input ResponsesInput) ([]GeminiContent, error) {
	var s string
	if err := json.Unmarshal(input, &s); err == nil {
		return []GeminiContent{{Role: "user", Parts: []GeminiPart{{Text: s}}}}, nil
	}

	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(input, &msgs); err != nil {
		return nil, fmt.Errorf("invalid input format: %w", err)
	}

	var contents []GeminiContent
	for _, msg := range msgs {
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "system" {
			continue
		}

		chatContent := ChatContent(msg.Content)
		parts, err := contentToParts(&chatContent)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		contents = append(contents, GeminiContent{Role: role, Parts: parts})
	}
	return contents, nil
}

// --- Conversion: Gemini Response → Responses Response ---

func geminiToResponses(gemResp *GeminiResponse, model string) *ResponsesResponse {
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	var output []ResponsesOutput
	for _, cand := range gemResp.Candidates {
		text := extractText(cand.Content)
		output = append(output, ResponsesOutput{
			ID:     msgID,
			Type:   "message",
			Status: "completed",
			Role:   "assistant",
			Content: []ResponsesContentPart{
				{Type: "output_text", Text: text},
			},
		})
	}

	resp := &ResponsesResponse{
		ID:        fmt.Sprintf("resp_%d", time.Now().UnixNano()),
		Object:    "response",
		CreatedAt: time.Now().Unix(),
		Model:     model,
		Output:    output,
	}

	if gemResp.UsageMetadata != nil {
		resp.Usage = &ResponsesUsage{
			InputTokens:  gemResp.UsageMetadata.PromptTokenCount,
			OutputTokens: gemResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:  gemResp.UsageMetadata.TotalTokenCount,
		}
	}

	return resp
}

// === handler_chat.go ===


func handleChatCompletions(client *geminiClient) http.HandlerFunc {
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

func handleChatStream(w http.ResponseWriter, client *geminiClient, path string, body []byte, model string) {
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

// === handler_responses.go ===


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

// === handler_gemini.go ===


func handleGeminiNative(client *geminiClient) http.HandlerFunc {
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

// === handler_models.go ===


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

// === main ===

var appMux *http.ServeMux

func init() {
	pool := newTokenPool()
	if pool == nil {
		log.Print("WARNING: no TOKEN* environment variables configured")
	} else {
		log.Printf("loaded %d Gemini tokens", pool.count())
	}

	client := newGeminiClient(pool)

	if !hasApiKey() {
		log.Print("WARNING: APIKEY environment variable not set")
	} else {
		log.Print("APIKEY configured")
	}

	mux := http.NewServeMux()

	mux.Handle("POST /v1/chat/completions", authMiddleware(handleChatCompletions(client)))
	mux.Handle("POST /v1/responses", authMiddleware(handleResponses(client)))
	mux.Handle("GET /v1/models", authMiddleware(handleModels(client)))
	mux.Handle("POST /v1beta/models/{path...}", authMiddleware(handleGeminiNative(client)))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"service": "Gemini EdgeOne Proxy",
			"endpoints": map[string]string{
				"chat_completions": "POST /v1/chat/completions",
				"responses":        "POST /v1/responses",
				"gemini_generate":  "POST /v1beta/models/{model}:generateContent",
				"gemini_stream":    "POST /v1beta/models/{model}:streamGenerateContent",
				"models":           "GET /v1/models",
			},
			"docs": "https://github.com/leemwood/gemini-edgeone-proxy",
		})
	})

	appMux = mux
}

// === main (Handler mode: EdgeOne calls indexHandler) ===

func indexHandler(w http.ResponseWriter, r *http.Request) {
	appMux.ServeHTTP(w, r)
}
