package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed frontend/dist/*
var distFS embed.FS

// GetFileSystem returns an http.Handler serving the built frontend files.
func GetFileSystem() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		return nil, err
	}
	return http.FileServer(http.FS(sub)), nil
}
