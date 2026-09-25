package common

import (
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/gin-contrib/static"
)

// Credit: https://github.com/gin-contrib/static/issues/19

type embedFileSystem struct {
	http.FileSystem
	allowRoot bool
}

func (e *embedFileSystem) Exists(prefix string, path string) bool {
	_, err := e.Open(path)
	if err != nil {
		return false
	}
	return true
}

func (e *embedFileSystem) Open(name string) (http.File, error) {
	if name == "/" && !e.allowRoot {
		// This will make sure the index page goes to NoRouter handler,
		// which will use the replaced index bytes with analytic codes.
		return nil, os.ErrNotExist
	}
	return e.FileSystem.Open(name)
}

func EmbedFolder(fsEmbed embed.FS, targetPath string) static.ServeFileSystem {
	return embedFolder(fsEmbed, targetPath, false)
}

// EmbedFolderWithIndex serves a folder's index.html at its root. It is used
// for standalone static sections such as the API documentation, while the
// main frontend keeps its dynamic index fallback.
func EmbedFolderWithIndex(fsEmbed embed.FS, targetPath string) static.ServeFileSystem {
	return embedFolder(fsEmbed, targetPath, true)
}

func embedFolder(fsEmbed embed.FS, targetPath string, allowRoot bool) static.ServeFileSystem {
	efs, err := fs.Sub(fsEmbed, targetPath)
	if err != nil {
		panic(err)
	}
	return &embedFileSystem{
		FileSystem: http.FS(efs),
		allowRoot:  allowRoot,
	}
}
