package proxy

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type tokenState struct {
	key      string
	cooldown time.Time
}

type TokenPool struct {
	mu     sync.Mutex
	tokens []tokenState
}

func NewTokenPool() *TokenPool {
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
	return &TokenPool{tokens: tokens}
}

func (p *TokenPool) getToken() (string, error) {
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

func (p *TokenPool) markRateLimited(token string) {
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

func (p *TokenPool) Len() int {
	if p == nil {
		return 0
	}
	return len(p.tokens)
}
