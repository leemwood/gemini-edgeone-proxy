package proxy

import (
	"encoding/json"
	"fmt"
	"time"
)

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
