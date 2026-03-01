package dashboard

import (
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	pathpkg "path"
	"path/filepath"
	"strings"

	hivemindassets "github.com/zyrakk/hivemind"
)

func registerFrontendRoutes(mux *http.ServeMux, logger *slog.Logger) {
	handler, err := newFrontendHandler(logger)
	if err != nil {
		logger.Error("failed to initialize embedded dashboard assets", slog.Any("error", err))
		return
	}

	// Catch-all GET route for SPA assets and client-side routes.
	mux.Handle("GET /", handler)
}

func newFrontendHandler(logger *slog.Logger) (http.Handler, error) {
	distFS, err := fs.Sub(hivemindassets.DashboardDistFS, "dashboard/dist")
	if err != nil {
		return nil, err
	}

	indexHTML, err := fs.ReadFile(distFS, "index.html")
	if err != nil {
		return nil, err
	}

	fileServer := http.FileServer(http.FS(distFS))

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(indexHTML)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/health" {
			http.NotFound(w, r)
			return
		}

		cleanPath := strings.TrimPrefix(pathpkg.Clean(r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			serveIndex(w, r)
			return
		}

		if cleanPath == "index.html" {
			serveIndex(w, r)
			return
		}

		if existsFile(distFS, cleanPath) {
			assetReq := r.Clone(r.Context())
			assetReq.URL.Path = "/" + cleanPath
			if contentType := mime.TypeByExtension(filepath.Ext(cleanPath)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			fileServer.ServeHTTP(w, assetReq)
			return
		}

		if logger != nil {
			logger.Debug("serving SPA fallback", slog.String("path", r.URL.Path))
		}
		serveIndex(w, r)
	}), nil
}

func existsFile(fsys fs.FS, path string) bool {
	info, err := fs.Stat(fsys, path)
	if err != nil {
		return false
	}

	return !info.IsDir()
}
