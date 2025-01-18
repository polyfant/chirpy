package main

import (
	 _ "github.com/lib/pq"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/joho/godotenv"
)

// apiConfig holds our stateful, in-memory data for tracking metrics
type apiConfig struct {
	fileserverHits atomic.Int32
}

type ChirpValidRequest struct {
	Body string `json:"body"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type ValidResponse struct {
	Valid bool `json:"valid"`
}

func getDatabaseConnectionString() string {
	connStr := os.Getenv("DB_URL")
	if connStr == "" {
		// Fallback to default connection string if not set
		connStr = "postgres://postgres:postgres@localhost:5432/chirpy"
		log.Println("DB_URL not set, using default connection string")
	}
	return connStr
}

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using default or system environment variables")
	}

	const filepathRoot = "."
	const port = "8080"

	// Create an instance of apiConfig
	apiCfg := &apiConfig{}

	mux := http.NewServeMux()

	// Wrap the file server with our metrics middleware
	fileServerHandler := http.FileServer(http.Dir(filepathRoot))
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", fileServerHandler)))

	// Add new routes for metrics and reset
	mux.HandleFunc("GET /api/healthz", handlerReadiness)
	mux.HandleFunc("GET /admin/metrics", apiCfg.handlerMetrics)
	mux.HandleFunc("POST /admin/reset", apiCfg.handlerReset)
	mux.HandleFunc("POST /api/validate_chirp", handlerValidateChirp)

	srv := &http.Server{
		Addr:    "0.0.0.0:" + port,
		Handler: mux,
	}

	log.Printf("Serving files from %s on port: %s\n", filepathRoot, port)
	log.Fatal(srv.ListenAndServe())
}

func handlerReadiness(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// middlewareMetricsInc increments the fileserver hits counter for each request
func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

// handlerMetrics returns the metrics page as HTML
func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	htmlTemplate := `<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`
	w.Write([]byte(fmt.Sprintf(htmlTemplate, cfg.fileserverHits.Load())))
}

// handlerReset resets the fileserver hits counter to 0
func (cfg *apiConfig) handlerReset(w http.ResponseWriter, r *http.Request) {
	cfg.fileserverHits.Store(0)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hits counter reset to 0"))
}

func handlerValidateChirp(w http.ResponseWriter, r *http.Request) {
    // 1. Decode the incoming JSON request body
    decoder := json.NewDecoder(r.Body)
    var req ChirpValidRequest
    err := decoder.Decode(&req)
    if err != nil {
        // Handle JSON decoding error
        respondWithError(w, http.StatusBadRequest, "Invalid JSON")
        return
    }

    // 2. Validate the chirp length
    if len(req.Body) > 140 {
        // Chirp is too long
        respondWithError(w, http.StatusBadRequest, "Chirp is too long")
        return
    }

    // 3. Apply profanity filter
	lowerBody := strings.ToLower(req.Body)
	cleanedBody := req.Body
    profaneWords := []string{"kerfuffle", "sharbert", "fornax"}
	
    
    for _, word := range profaneWords {
		if strings.Contains(lowerBody, word) {
			cleanedBody = strings.ReplaceAll(cleanedBody, strings.ToLower(word), "****")
			cleanedBody = strings.ReplaceAll(cleanedBody, strings.ToUpper(word), "****")
		}
	}

    // 4. If valid, respond with success and cleaned body
    respondWithJSON(w, http.StatusOK, struct {
        Valid       bool   `json:"valid"`
        CleanedBody string `json:"cleaned_body"`
    }{
        Valid:       true,
        CleanedBody: cleanedBody,
    })
}

func respondWithError(w http.ResponseWriter, statusCode int, message string) {
    respondWithJSON(w, statusCode, ErrorResponse{Error: message})
}

// Helper function to respond with JSON
func respondWithJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
    dat, err := json.Marshal(payload)
    if err != nil {
        log.Printf("Error marshalling JSON: %s", err)
        w.WriteHeader(http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    w.Write(dat)
}