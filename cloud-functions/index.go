package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	pool := newTokenPool()
	if pool == nil {
		log.Fatal("no TOKEN* environment variables configured")
	}
	log.Printf("loaded %d Gemini tokens", len(pool.tokens))

	client := newGeminiClient(pool)

	if apiKey == "" {
		log.Fatal("APIKEY environment variable not set")
	}
	log.Println("APIKEY configured")

	mux := http.NewServeMux()

	mux.Handle("POST /v1/chat/completions", authMiddleware(handleChatCompletions(client)))
	mux.Handle("POST /v1/responses", authMiddleware(handleResponses(client)))
	mux.Handle("GET /v1/models", authMiddleware(handleModels(client)))
	mux.Handle("POST /v1beta/models/{path...}", authMiddleware(handleGeminiNative(client)))

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("listening on :%s", port)
	if err := http.ListenAndServe(fmt.Sprintf(":%s", port), mux); err != nil {
		log.Fatal(err)
	}
}
