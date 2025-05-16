package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/polyfant/chirpy/internal/database"
)

type apiConfig struct {
    fileserverHits atomic.Int32
	db				*database.Queries
    platform       string
	
}

type User struct {
    ID        uuid.UUID `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Email     string    `json:"email"`
}

type createUserRequest struct {
    Email string `json:"email"`
}
type Chirp struct {
    ID        uuid.UUID `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    Body      string    `json:"body"`
    UserID    uuid.UUID `json:"user_id"`
}

type createChirpRequest struct {
    Body   string    `json:"body"`
    UserID uuid.UUID `json:"user_id"`
}

func main() {
	const filepathRoot = "."
	const port = "8080"
	    if err := godotenv.Load(".env"); err != nil {
		    log.Printf("Error loading .env file: %v", err)
	    }
    
    platform := os.Getenv("PLATFORM")
    fmt.Printf("Platform: %s\n", platform)
	
	dbConn, err := sql.Open("postgres", os.Getenv("DB_URL"))
    if err != nil {
        log.Fatal(err)
    }
    
    dbQueries := database.New(dbConn)

	// Create an instance of apiConfig
	 apiCfg := &apiConfig{
        platform: os.Getenv("PLATFORM"),
		db: dbQueries,
    }

	mux := http.NewServeMux()
	
	// Wrap the file server with our metrics middleware
	fileServerHandler := http.FileServer(http.Dir(filepathRoot))
	mux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", fileServerHandler)))
	
	// API routes
	mux.HandleFunc("/api/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			handlerReadiness(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			apiCfg.handlerCreateUser(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/api/chirps", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			apiCfg.handlerListChirps(w, r)
		case http.MethodPost:
			apiCfg.handlerCreateChirp(w, r)
		default:
			http.NotFound(w, r)
		}
	})

	// Handle /api/chirps/{id} pattern
	mux.HandleFunc("/api/chirps/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			apiCfg.handlerGetChirp(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/admin/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			apiCfg.handlerMetrics(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	mux.HandleFunc("/admin/reset", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			apiCfg.handlerReset(w, r)
		} else {
			http.NotFound(w, r)
		}
	})

	srv := &http.Server{
		Addr:    "0.0.0.0:" + port,
		Handler: mux,
	}

	log.Printf("Serving files from %s on port: %s\n", filepathRoot, port)
	log.Fatal(srv.ListenAndServe())
}

func handlerReadiness(w http.ResponseWriter, _ *http.Request) {
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
func (cfg *apiConfig) handlerMetrics(w http.ResponseWriter, _ *http.Request) {
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
    if cfg.platform != "dev" {
        respondWithError(w, http.StatusForbidden, "Forbidden")
        return
    }

    // Delete all users first (this will cascade delete chirps due to ON DELETE CASCADE)
    err := cfg.db.DeleteAllUsers(r.Context())
    if err != nil {
        fmt.Printf("Reset error: %v\n", err)
        respondWithError(w, http.StatusInternalServerError, "Couldn't reset database")
        return
    }

    cfg.fileserverHits.Store(0)
    w.WriteHeader(http.StatusOK)
}

func (cfg *apiConfig) handlerCreateUser(w http.ResponseWriter, r *http.Request) {
    decoder := json.NewDecoder(r.Body)
    params := createUserRequest{}
    err := decoder.Decode(&params)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Invalid request body")
        return
    }

    dbUser, err := cfg.db.CreateUser(r.Context(), params.Email)
    if err != nil {
        if strings.Contains(err.Error(), "duplicate key value") {
            respondWithError(w, http.StatusBadRequest, "Email already exists")
            return
        }
        respondWithError(w, http.StatusInternalServerError, "Couldn't create user")
        return
    }

    // Convert database user to API user
    user := User{
        ID:        dbUser.ID,
        CreatedAt: dbUser.CreatedAt,
        UpdatedAt: dbUser.UpdatedAt,
        Email:     dbUser.Email,
    }

    respondWithJSON(w, http.StatusCreated, user)
}
// handlerListChirps handles GET /api/chirps and returns all chirps ordered by created_at ascending
// handlerGetChirp handles GET /api/chirps/{chirpID} and returns a single chirp by ID
func (cfg *apiConfig) handlerGetChirp(w http.ResponseWriter, r *http.Request) {
    log.Printf("=== handlerGetChirp called ===")
    log.Printf("Request URL: %s", r.URL.String())
    log.Printf("Method: %s", r.Method)
    
    // Extract the chirp ID from the URL path
    path := r.URL.Path
    log.Printf("Full path: %s", path)
    
    // Ensure the path starts with /api/chirps/
    if !strings.HasPrefix(path, "/api/chirps/") {
        log.Printf("Path doesn't start with /api/chirps/")
        http.NotFound(w, r)
        return
    }
    
    idStr := path[len("/api/chirps/"):]
    log.Printf("Extracted ID string: '%s'", idStr)
    
    // Remove any trailing slashes
    idStr = strings.TrimSuffix(idStr, "/")
    log.Printf("After trimming slashes: '%s'", idStr)
    
    // Ensure the ID is not empty
    if idStr == "" {
        log.Println("No ID provided, listing all chirps")
        // If no ID is provided, list all chirps (forward to the list handler)
        cfg.handlerListChirps(w, r)
        return
    }
    
    log.Printf("Attempting to parse UUID: %s", idStr)
    chirpID, err := uuid.Parse(idStr)
    if err != nil {
        log.Printf("Failed to parse UUID: %v", err)
        respondWithError(w, http.StatusBadRequest, "Invalid chirp ID")
        return
    }
    
    log.Printf("Looking up chirp with UUID: %s", chirpID)

    dbChirp, err := cfg.db.GetChirp(r.Context(), chirpID)
    if err != nil {
        log.Printf("Database error when looking up chirp %s: %v", chirpID, err)
        if err == sql.ErrNoRows {
            respondWithError(w, http.StatusNotFound, "Chirp not found")
        } else {
            respondWithError(w, http.StatusInternalServerError, "Error retrieving chirp")
        }
        return
    }
    log.Printf("Successfully retrieved chirp: %+v", dbChirp)

    // Convert database chirp to API chirp
    chirp := Chirp{
        ID:        dbChirp.ID,
        CreatedAt: dbChirp.CreatedAt,
        UpdatedAt: dbChirp.UpdatedAt,
        Body:      dbChirp.Body,
        UserID:    dbChirp.UserID,
    }

    respondWithJSON(w, http.StatusOK, chirp)
}

// handlerListChirps handles GET /api/chirps and returns all chirps ordered by created_at ascending
func (cfg *apiConfig) handlerListChirps(w http.ResponseWriter, r *http.Request) {
    chirps, err := cfg.db.ListChirps(r.Context())
    if err != nil {
        respondWithError(w, http.StatusInternalServerError, "Couldn't fetch chirps")
        return
    }
    // Convert []database.Chirp to []Chirp for API response
    apiChirps := make([]Chirp, len(chirps))
    for i, c := range chirps {
        apiChirps[i] = Chirp{
            ID:        c.ID,
            CreatedAt: c.CreatedAt,
            UpdatedAt: c.UpdatedAt,
            Body:      c.Body,
            UserID:    c.UserID,
        }
    }
    respondWithJSON(w, http.StatusOK, apiChirps)
}

func (cfg *apiConfig) handlerCreateChirp(w http.ResponseWriter, r *http.Request) {
    decoder := json.NewDecoder(r.Body)
    params := createChirpRequest{}
    err := decoder.Decode(&params)
    if err != nil {
        respondWithError(w, http.StatusBadRequest, "Invalid request payload")
        return
    }

    if len(params.Body) > 140 {
        respondWithError(w, http.StatusBadRequest, "Chirp is too long")
        return
    }

    dbChirp, err := cfg.db.CreateChirp(r.Context(), database.CreateChirpParams{
        ID:        uuid.New(),
        CreatedAt: time.Now().UTC(),
        UpdatedAt: time.Now().UTC(),
        Body:      params.Body,
        UserID:    params.UserID,
    })
    if err != nil {
        fmt.Printf("Database error: %v\n", err)
        respondWithError(w, http.StatusInternalServerError, "Couldn't create chirp")
        return
    }

    // Convert database chirp to API chirp
    chirp := Chirp{
        ID:        dbChirp.ID,
        CreatedAt: dbChirp.CreatedAt,
        UpdatedAt: dbChirp.UpdatedAt,
        Body:      dbChirp.Body,
        UserID:    dbChirp.UserID,
    }

    respondWithJSON(w, http.StatusCreated, chirp)
}

// Helper functions
func respondWithError(w http.ResponseWriter, code int, msg string) {
    respondWithJSON(w, code, map[string]string{"error": msg})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
    response, _ := json.Marshal(payload)
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    w.Write(response)
}
