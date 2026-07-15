package gateway

import (
	"encoding/json"
	"net/http"
)

func WriteError(w http.ResponseWriter, status int, inFormat, message, errType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	var body []byte
	switch inFormat {
	case "anthropic":
		body, _ = json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errType, "message": message},
		})
	default:
		body, _ = json.Marshal(map[string]any{
			"error": map[string]any{"message": message, "type": errType},
		})
	}
	w.Write(body)
}
