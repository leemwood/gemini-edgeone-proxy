package main

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
