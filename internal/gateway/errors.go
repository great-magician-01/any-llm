package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/great-magician-01/any-llm/internal/logger"
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
	if status >= 400 {
		logger.Warn("gateway error response",
			"format", inFormat,
			"status", status,
			"type", errType,
			"message", truncate(message, 500),
		)
	}
}

// mapErrorType maps an upstream HTTP status code to an error type string
// appropriate for the client-facing (inFormat) response. When the upstream
// already provides a type that matches the client format, it is preserved.
func mapErrorType(inFormat string, status int, upstreamType string) string {
	switch inFormat {
	case "anthropic":
		switch status {
		case 400:
			return "invalid_request_error"
		case 401:
			return "authentication_error"
		case 403:
			return "permission_error"
		case 404:
			return "not_found_error"
		case 413:
			return "request_too_large"
		case 429:
			return "rate_limit_error"
		case 529:
			return "overloaded_error"
		}
		if status >= 500 {
			return "api_error"
		}
		return "api_error"
	default:
		switch status {
		case 400:
			return "invalid_request_error"
		case 401:
			return "authentication_error"
		case 403:
			return "permission_error"
		case 404:
			return "not_found_error"
		case 429:
			return "rate_limit_error"
		}
		if status >= 500 {
			return "server_error"
		}
		return "api_error"
	}
}
