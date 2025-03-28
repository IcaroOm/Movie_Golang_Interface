package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
)

//go:embed templates/*
var templates embed.FS

const TMDB_API_KEY = "503706704089e1555e6f2e13e59136f8"
const TMDB_BASE_URL = "https://api.themoviedb.org/3"
const IMAGE_BASE_URL = "https://image.tmdb.org/t/p/w500"

type Movie struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	PosterPath  string  `json:"poster_path"`
    ReleaseDate string  `json:"release_date"`
	Overview    string  `json:"overview"`
	VoteAverage float64 `json:"vote_average"`
	GenreIDs    []int   `json:"genre_ids"`
	Genres      []Genre
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TrendingResponse struct {
	Results []Movie `json:"results"`
}

func enableCORS(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
        w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Authorization")

        if r.Method == "OPTIONS" {
            return
        }

        next.ServeHTTP(w, r)
    })
}

func main() {
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"formatYear": func(date string) string {
			if len(date) >= 4 {
				return date[:4]
			}
			return ""
		},
		"sliceYear": func(s string) string {
			if len(s) >= 4 {
				return s[:4]
			}
			return ""
		},
		"genreName": func(genres []Genre) string {
			if len(genres) > 0 {
				return genres[0].Name
			}
			return ""
		},
		"imageURL": func(path string) string {
			if path == "" {
				return ""
			}
			return IMAGE_BASE_URL + path
		},
	}).ParseFS(templates, "templates/*.html"))

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tmpl.ExecuteTemplate(w, "index.html", nil)
	})

	mux.HandleFunc("/movies", func(w http.ResponseWriter, r *http.Request) {
		searchQuery := r.URL.Query().Get("search")
		var movies []Movie
		var err error

		if searchQuery != "" {
			movies, err = fetchSearchMovies(searchQuery)
		} else {
			movies, err = fetchTrendingMovies()
		}

		if err != nil {
			log.Printf("Error fetching movies: %v", err)
			http.Error(w, "Failed to fetch movies", http.StatusInternalServerError)
			return
		}

		for _, movie := range movies {
			err := tmpl.ExecuteTemplate(w, "movie-card.html", movie)
			if err != nil {
				log.Printf("Template error: %v", err)
			}
		}
	})

	mux.HandleFunc("/save-movie", func(w http.ResponseWriter, r *http.Request) {
		// Get the authorization header from the original request
		authHeader := r.Header.Get("Authorization")

		// Read and parse JSON body
        var movieData struct {
            Title  string  `json:"title"`
            Year   int     `json:"year"`
            Plot   string  `json:"plot"`
            Rating float64 `json:"rating"`
        }

        if err := json.NewDecoder(r.Body).Decode(&movieData); err != nil {
            respondWithJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON format"})
            return
        }

		apiReqBody, _ := json.Marshal(movieData)
        req, err := http.NewRequest("POST", "http://127.0.0.1:8000/api/movies", bytes.NewBuffer(apiReqBody))
		if err != nil {
			http.Error(w, "Error creating request", http.StatusInternalServerError)
			return
		}

		// Set headers for your API
		req.Header.Set("Content-Type", "application/json")
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}

		// Send to your API
		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Error connecting to API", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Forward the response
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	handler := enableCORS(mux)

	log.Println("Server running on :5000")
	http.ListenAndServe(":5000", handler)
}

func respondWithJSON(w http.ResponseWriter, statusCode int, payload interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(statusCode)
    json.NewEncoder(w).Encode(payload)
}

func fetchSearchMovies(query string) ([]Movie, error) {
	url := fmt.Sprintf("%s/search/movie?api_key=%s&query=%s",
		TMDB_BASE_URL,
		TMDB_API_KEY,
		url.QueryEscape(query),
	)

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("search API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search API returned status: %d", resp.StatusCode)
	}

	var result struct {
		Results []Movie `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	genres, err := fetchGenres()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch genres: %w", err)
	}

	for i := range result.Results {
		result.Results[i].Genres = getMovieGenres(result.Results[i].GenreIDs, genres)
	}

	return result.Results, nil
}

func fetchTrendingMovies() ([]Movie, error) {
	url := fmt.Sprintf("%s/trending/movie/day?api_key=%s", TMDB_BASE_URL, TMDB_API_KEY)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status: %d", resp.StatusCode)
	}

	var result TrendingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	genres, err := fetchGenres()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch genres: %w", err)
	}

	for i := range result.Results {
		result.Results[i].Genres = getMovieGenres(result.Results[i].GenreIDs, genres)
	}

	return result.Results, nil
}

func fetchGenres() ([]Genre, error) {
	url := fmt.Sprintf("%s/genre/movie/list?api_key=%s", TMDB_BASE_URL, TMDB_API_KEY)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("genre API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("genre API returned status: %d", resp.StatusCode)
	}

	var genreResponse struct {
		Genres []Genre `json:"genres"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&genreResponse); err != nil {
		return nil, fmt.Errorf("failed to decode genres: %w", err)
	}

	return genreResponse.Genres, nil
}

func getMovieGenres(genreIDs []int, allGenres []Genre) []Genre {
	var movieGenres []Genre
	for _, genreID := range genreIDs {
		for _, genre := range allGenres {
			if genre.ID == genreID {
				movieGenres = append(movieGenres, genre)
				break
			}
		}
	}
	return movieGenres
}