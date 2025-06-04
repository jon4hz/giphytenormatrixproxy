// maunium-stickerpicker - A fast and simple Matrix sticker picker widget.
// Copyright (C) 2024 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"crypto/rand" // Add this import
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp" // Add this import

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"go.mau.fi/util/exerrors"
	"gopkg.in/yaml.v3"
	"maunium.net/go/mautrix/federation"
	"maunium.net/go/mautrix/mediaproxy"
)

//go:embed index.html
//go:embed styles.css
var content embed.FS

type Config struct {
	mediaproxy.BasicConfig  `yaml:",inline"`
	mediaproxy.ServerConfig `yaml:",inline"`
	Destination             string `yaml:"destination"`
	TenorDestination        string `yaml:"tenor_destination"`
	GiphyAPIKey             string `yaml:"giphy_api_key"`
	TenorAPIKey             string `yaml:"tenor_api_key"`
	IndexPath               string `yaml:"index_path"`
	StoragePath             string `yaml:"storage_path"`
	LocalAPIBearer          string `yaml:"local_api_bearer"`
	GifPath                 string `yaml:"gif_path"`
	Locale                  string `yaml:"locale"`
}

var configPath = flag.String("config", "config.yaml", "config file path")
var generateServerKey = flag.Bool("generate-key", false, "generate a new server key and exit")
var config *Config = nil

var (
	giphyIDRegex     = regexp.MustCompile(`^g-[a-zA-Z0-9-_]+$`)
	tenorIDRegex     = regexp.MustCompile(`^t-[a-zA-Z0-9-_]+$`)
	localIDRegex     = regexp.MustCompile(`^l-[a-zA-Z0-9+/=_-]+$`) // Modified for base64
	giphyDestination = "https://i.giphy.com/%s.webp"
	tenorDestination = "https://media.tenor.com/%s/image.webp"
)

// Add these near the top with other vars
var (
	localImagesCache    []LocalImage
	localImagesCacheTS  time.Time
	localImagesCacheTTL = 5 * time.Minute // Configure cache TTL
	localImagesCacheMu  sync.RWMutex
)

func getAllIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var ips []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if !ipnet.IP.IsLoopback() && !ipnet.IP.IsLinkLocalUnicast() {
					ips = append(ips, ipnet.IP.String())
				}
			}
		}
	}
	return ips
}

type LocalImage struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Mimetype string `json:"mimetype"`
}

func detectImageInfo(filepath string) (width, height int, mimetype string, err error) {
	file, err := os.Open(filepath)
	if err != nil {
		return 0, 0, "", err
	}
	defer file.Close()

	// Read the first 512 bytes to detect mimetype
	buffer := make([]byte, 512)
	_, err = file.Read(buffer)
	if err != nil {
		return 0, 0, "", err
	}
	mimetype = http.DetectContentType(buffer)

	// Reset file pointer to start
	file.Seek(0, 0)

	// Decode image for dimensions
	img, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, mimetype, err // Return mimetype even if dimensions fail
	}

	return img.Width, img.Height, mimetype, nil
}

func listLocalImages(storagePath string) ([]LocalImage, error) {
	localImagesCacheMu.RLock()
	if time.Since(localImagesCacheTS) < localImagesCacheTTL && localImagesCache != nil {
		defer localImagesCacheMu.RUnlock()
		return localImagesCache, nil
	}
	localImagesCacheMu.RUnlock()

	// Need to rebuild cache
	localImagesCacheMu.Lock()
	defer localImagesCacheMu.Unlock()

	// Double check if another goroutine already updated the cache
	if time.Since(localImagesCacheTS) < localImagesCacheTTL && localImagesCache != nil {
		return localImagesCache, nil
	}

	files, err := os.ReadDir(storagePath)
	if err != nil {
		return nil, err
	}

	var images []LocalImage

	for _, file := range files {
		if !file.IsDir() {
			fp := path.Join(storagePath, file.Name())
			width, height, mimetype, err := detectImageInfo(fp)

			// Skip files that aren't valid images
			if err != nil {
				log.Printf("Warning: Skipping %s: %v", file.Name(), err)
				continue
			}

			// Base64 encode the filename
			b64name := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(file.Name()))
			id := "l-" + b64name

			images = append(images, LocalImage{
				ID:       id,
				Filename: file.Name(),
				URL:      path.Join("/", config.GifPath, id),
				Width:    width,
				Height:   height,
				Mimetype: mimetype,
			})
		}
	}

	// Update cache
	localImagesCache = images
	localImagesCacheTS = time.Now()

	return images, nil
}

func generateRandomBearer() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		log.Fatal(err)
	}
	return base64.URLEncoding.EncodeToString(b)
}

func main() {
	flag.Parse()
	if *generateServerKey {
		fmt.Println(federation.GenerateSigningKey().SynapseString())
	} else {
		cfgFile := exerrors.Must(os.ReadFile(*configPath))
		var cfg Config
		exerrors.PanicIfNotNil(yaml.Unmarshal(cfgFile, &cfg))
		config = &cfg

		// Set default locale if not specified
		if cfg.Locale == "" {
			cfg.Locale = "en_US"
		}
		cfg.Locale = strings.ReplaceAll(cfg.Locale, "-", "_")

		if cfg.LocalAPIBearer == "" {
			cfg.LocalAPIBearer = generateRandomBearer()
			log.Printf("Generated Local API Bearer: %s", cfg.LocalAPIBearer)
		}

		if cfg.GifPath == "" {
			cfg.GifPath = "/gif/"
		}

		router := mux.NewRouter()

		// Create index handler function
		indexHandler := func(w http.ResponseWriter, r *http.Request) {
			tmpl, err := template.ParseFS(content, "index.html")
			if err != nil {
				http.Error(w, "Error parsing template", http.StatusInternalServerError)
				return
			}

			data := struct {
				ServerName     string
				HasLocalFiles  bool
				HasGiphyKey    bool
				HasTenorKey    bool
				LocalAPIBearer string
				GifPath        string
				Locale         string
			}{
				ServerName:     cfg.ServerName,
				HasLocalFiles:  false,
				HasGiphyKey:    cfg.GiphyAPIKey != "",
				HasTenorKey:    cfg.TenorAPIKey != "",
				LocalAPIBearer: cfg.LocalAPIBearer,
				GifPath:        cfg.GifPath,
				Locale:         cfg.Locale,
			}

			// Check if local storage has any files
			if files, err := listLocalImages(cfg.StoragePath); err == nil {
				log.Printf("Found %d local files", len(files))
				data.HasLocalFiles = len(files) > 0
			} else {
				log.Printf("Error listing local files: %v", err)
			}

			w.Header().Set("Content-Type", "text/html")
			if err := tmpl.Execute(w, data); err != nil {
				log.Printf("Error executing template: %v", err)
				http.Error(w, "Error executing template", http.StatusInternalServerError)
				return
			}
		}

		// Register index handler at custom path if configured
		if cfg.IndexPath == "" {
			cfg.IndexPath = "/"
		}
		router.HandleFunc(cfg.IndexPath, indexHandler)

		router.PathPrefix("/styles.css").Handler(http.FileServer(http.FS(content)))

		// Debug endpoint to show redirect URL
		router.HandleFunc(path.Join(cfg.GifPath, "{id}"), func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			id := vars["id"]

			var url string
			switch {
			case giphyIDRegex.MatchString(id):
				url = fmt.Sprintf(giphyDestination, strings.TrimPrefix(id, "g-"))
				http.Redirect(w, r, url, http.StatusFound)
				return
			case tenorIDRegex.MatchString(id):
				url = fmt.Sprintf(tenorDestination, strings.TrimPrefix(id, "t-"))
				http.Redirect(w, r, url, http.StatusFound)
				return
			case localIDRegex.MatchString(id): // Add this case
				// Decode the base64 filename
				b64name := strings.TrimPrefix(id, "l-")
				filename, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(b64name)
				if err != nil {
					http.Error(w, "Invalid filename encoding", http.StatusBadRequest)
					return
				}
				path := filepath.Join(cfg.StoragePath, string(filename))
				// Normalize path
				path = filepath.Clean(path)
				if !strings.HasPrefix(path, cfg.StoragePath) {
					http.Error(w, "Invalid path", http.StatusBadRequest)
					return
				}
				http.ServeFile(w, r, path)
				return
			default:
				http.Error(w, "Invalid ID format", http.StatusBadRequest)
				return
			}
		})

		// Serve local storage listing
		router.HandleFunc("/api/local", func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			expectedHeader := "Bearer " + cfg.LocalAPIBearer

			if authHeader != expectedHeader {
				http.NotFound(w, r)
				return
			}

			images, err := listLocalImages(cfg.StoragePath)
			if err != nil {
				http.Error(w, "Error reading storage directory", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(images)
		})

		// --- Giphy Proxy Endpoints ---
		router.HandleFunc("/api/giphy/search", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("q")
			offset := r.URL.Query().Get("offset")
			limit := r.URL.Query().Get("limit")
			stickers := r.URL.Query().Get("stickers")
			country := r.URL.Query().Get("country")
			if limit == "" {
				limit = "50"
			}
			if offset == "" {
				offset = "0"
			}
			isStickers := stickers == "true"
			basePath := map[bool]string{true: "stickers", false: "gifs"}[isStickers]

			u := url.URL{
				Scheme: "https",
				Host:   "api.giphy.com",
				Path:   fmt.Sprintf("/v1/%s/search", basePath),
			}
			params := url.Values{}
			params.Set("limit", limit)
			params.Set("offset", offset)
			params.Set("q", q)
			params.Set("api_key", cfg.GiphyAPIKey)
			if country != "" {
				params.Set("country_code", country)
			}
			u.RawQuery = params.Encode()

			resp, err := http.Get(u.String())
			if err != nil {
				http.Error(w, "Failed to fetch from Giphy", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})

		router.HandleFunc("/api/giphy/trending", func(w http.ResponseWriter, r *http.Request) {
			offset := r.URL.Query().Get("offset")
			limit := r.URL.Query().Get("limit")
			stickers := r.URL.Query().Get("stickers")
			country := r.URL.Query().Get("country")
			if limit == "" {
				limit = "50"
			}
			if offset == "" {
				offset = "0"
			}
			isStickers := stickers == "true"
			basePath := map[bool]string{true: "stickers", false: "gifs"}[isStickers]

			u := url.URL{
				Scheme: "https",
				Host:   "api.giphy.com",
				Path:   fmt.Sprintf("/v1/%s/trending", basePath),
			}
			params := url.Values{}
			params.Set("limit", limit)
			params.Set("offset", offset)
			params.Set("api_key", cfg.GiphyAPIKey)
			if country != "" {
				params.Set("country_code", country)
			}
			u.RawQuery = params.Encode()

			resp, err := http.Get(u.String())
			if err != nil {
				http.Error(w, "Failed to fetch from Giphy", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})

		// --- Tenor Proxy Endpoints ---
		router.HandleFunc("/api/tenor/search", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query().Get("q")
			pos := r.URL.Query().Get("pos")
			limit := r.URL.Query().Get("limit")
			locale := r.URL.Query().Get("locale")
			country := r.URL.Query().Get("country")
			if limit == "" {
				limit = "50"
			}

			u := url.URL{
				Scheme: "https",
				Host:   "tenor.googleapis.com",
				Path:   "/v2/search",
			}
			params := url.Values{}
			params.Set("limit", limit)
			params.Set("q", q)
			params.Set("key", cfg.TenorAPIKey)
			if pos != "" {
				params.Set("pos", pos)
			}
			if locale != "" {
				params.Set("locale", locale)
			}
			if country != "" {
				params.Set("country", country)
			}
			u.RawQuery = params.Encode()

			resp, err := http.Get(u.String())
			if err != nil {
				http.Error(w, "Failed to fetch from Tenor", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})

		router.HandleFunc("/api/tenor/featured", func(w http.ResponseWriter, r *http.Request) {
			limit := r.URL.Query().Get("limit")
			locale := r.URL.Query().Get("locale")
			country := r.URL.Query().Get("country")
			if limit == "" {
				limit = "50"
			}

			u := url.URL{
				Scheme: "https",
				Host:   "tenor.googleapis.com",
				Path:   "/v2/featured",
			}
			params := url.Values{}
			params.Set("key", cfg.TenorAPIKey)
			params.Set("limit", limit)
			if locale != "" {
				params.Set("locale", locale)
			}
			if country != "" {
				params.Set("country", country)
			}
			u.RawQuery = params.Encode()

			resp, err := http.Get(u.String())
			if err != nil {
				http.Error(w, "Failed to fetch from Tenor", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
		})

		mp, err := mediaproxy.NewFromConfig(cfg.BasicConfig, getMedia)
		if err != nil {
			// if server key is "CHANGE_ME" it will panic here, generate a new key to the user
			key := cfg.BasicConfig.ServerKey
			if key == "CHANGE ME" {
				key = federation.GenerateSigningKey().SynapseString()
				log.Printf("Generated new server key: %s", key)
				log.Printf("Please update your config file (%s) with the new key", *configPath)
			}
			log.Fatalf("Error creating media proxy: %s", err)
		}
		mp.KeyServer.Version.Name = "giphytenormatrixproxy - tenor giphy proxy"
		if cfg.Destination != "" {
			giphyDestination = cfg.Destination
		}
		if cfg.TenorDestination != "" {
			tenorDestination = cfg.TenorDestination
		}

		// Register media proxy routes
		mp.RegisterRoutes(router)

		// Start the server
		server := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.ServerConfig.Hostname, cfg.ServerConfig.Port),
			Handler: handlers.CombinedLoggingHandler(os.Stderr, router),
		}

		log.Printf("Server starting on %s:%d", cfg.ServerConfig.Hostname, cfg.ServerConfig.Port)
		if cfg.ServerConfig.Hostname == "0.0.0.0" {
			if ips := getAllIPs(); len(ips) > 0 {
				addresses := make([]string, len(ips))
				for i, ip := range ips {
					addresses[i] = fmt.Sprintf("%s:%d", ip, cfg.ServerConfig.Port)
				}
				log.Printf("Available on: %s", strings.Join(addresses, ", "))
			}
		}
		exerrors.PanicIfNotNil(server.ListenAndServe())
	}
}

func getMedia(_ context.Context, id string, _ map[string]string) (response mediaproxy.GetMediaResponse, err error) {
	// This is not related to giphy, but random cats are always fun
	if id == "cat" {
		return &mediaproxy.GetMediaResponseURL{
			URL:       "https://cataas.com/cat",
			ExpiresAt: time.Now(),
		}, nil
	}

	log.Printf("Getting media for ID: %s", id)

	switch {
	case giphyIDRegex.MatchString(id):
		return &mediaproxy.GetMediaResponseURL{
			URL: fmt.Sprintf(giphyDestination, strings.TrimPrefix(id, "g-")),
		}, nil
	case tenorIDRegex.MatchString(id):
		return &mediaproxy.GetMediaResponseURL{
			URL: fmt.Sprintf(tenorDestination, strings.TrimPrefix(id, "t-")),
		}, nil
	case localIDRegex.MatchString(id):
		url := fmt.Sprintf("https://%s%s%s", config.BasicConfig.ServerName, config.GifPath, id)
		return &mediaproxy.GetMediaResponseURL{
			URL: url,
		}, nil
	default:
		return nil, mediaproxy.ErrInvalidMediaIDSyntax
	}
}
