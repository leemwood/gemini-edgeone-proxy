package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"gemini-edgeone-proxy/proxy"
)

func main() {
	pool := proxy.NewTokenPool()
	if pool == nil {
		log.Fatal("no TOKEN* environment variables configured")
	}
	log.Printf("loaded %d Gemini tokens", pool.Len())

	client := proxy.NewGeminiClient(pool)
	log.Println("Gemini client ready")

	if !proxy.HasApiKey() {
		log.Fatal("APIKEY environment variable not set")
	}
	log.Println("APIKEY configured")

	mux := http.NewServeMux()

	mux.Handle("POST /v1/chat/completions", proxy.AuthMiddleware(proxy.HandleChatCompletions(client)))
	mux.Handle("POST /v1/responses", proxy.AuthMiddleware(proxy.HandleResponses(client)))
	mux.Handle("GET /v1/models", proxy.AuthMiddleware(proxy.HandleModels(client)))
	mux.Handle("POST /v1beta/models/{path...}", proxy.AuthMiddleware(proxy.HandleGeminiNative(client)))

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
