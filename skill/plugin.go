package skill

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"time"
)

// PluginOrigin is the connector origin written in the plugin's files; a
// relay serving the zip puts its own in its place (PluginZip).
const PluginOrigin = "https://ask-form.xyz"

// PluginZip zips the chat-app plugin (askform.Plugin) under one top-level
// "ask-form/" folder, the shape both Claude's "Upload plugin" and ChatGPT's
// plugin upload take, with origin as its connector's address.
func PluginZip(plugin fs.FS, origin string) ([]byte, error) {
	var repl []byte
	if origin != "" {
		clean, _, err := validOrigin(origin)
		if err != nil {
			return nil, err
		}
		// Marshal as a JSON string so the replacement is guaranteed valid JSON.
		repl, err = json.Marshal(clean + "/mcp")
		if err != nil {
			return nil, err
		}
	}
	wantJSON, _ := json.Marshal(PluginOrigin + "/mcp")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := fs.WalkDir(plugin, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(plugin, p)
		if err != nil {
			return err
		}
		if base := path.Base(p); (base == ".mcp.json" || base == "mcp.json") && len(repl) > 0 {
			b = bytes.ReplaceAll(b, wantJSON, repl)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "ask-form/" + p, Method: zip.Deflate, Modified: time.Unix(0, 0)})
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
