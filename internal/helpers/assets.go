package helpers

import (
	"embed"
)

var assetsCache = map[string][]byte{}

func ReadAssetContent(fs embed.FS, path string) []byte {
	if content, cached := assetsCache[path]; cached {
		return content
	}

	data, err := fs.ReadFile(path)
	if err != nil {
		return nil
	}

	assetsCache[path] = data
	return data
}
