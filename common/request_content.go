package common

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

const (
	requestContentContextKey  = "request_content_snapshot"
	requestContentEnabledEnv  = "REQUEST_CONTENT_LOG_ENABLED"
	requestContentMaxBytesEnv = "REQUEST_CONTENT_LOG_MAX_BYTES"

	RequestContentDefaultMaxBytes = 262144
	RequestContentMinMaxBytes     = 256
	RequestContentMaxMaxBytes     = 128 << 20

	RequestContentStatusRecorded    = "recorded"
	RequestContentStatusTruncated   = "truncated"
	RequestContentStatusNoContent   = "no_content"
	RequestContentStatusUnsupported = "unsupported"
	RequestContentStatusInvalid     = "invalid"
	RequestContentStatusUnavailable = "unavailable"
	RequestContentStatusDisabled    = "disabled"
	RequestContentStatusStorageFail = "storage_failed"
)

// requestContentFields is the closed whitelist of client prompt fields. Media,
// headers, credentials and every other request member are never captured.
var requestContentFields = []string{"messages", "prompt", "input", "instructions", "system", "contents", "systemInstruction"}

// RequestContentSnapshot owns the selected original input fields. It must never
// be written to consume parameters, Other, audit events or process logs.
type RequestContentSnapshot struct {
	Ref            string     `json:"ref"`
	RequestID      string     `json:"request_id"`
	UserID         int        `json:"-"`
	SubmitNodeName string     `json:"submit_node_name"`
	CreatedAt      int64      `json:"-"`
	Status         string     `json:"status"`
	CapturedBytes  int        `json:"captured_bytes"`
	Truncated      bool       `json:"truncated"`
	Payload        RawMessage `json:"-"`
}

// RequestContentMetadata is the only request-content value allowed in
// privileged log metadata; it never carries the payload.
type RequestContentMetadata struct {
	Ref           string `json:"ref,omitempty"`
	Status        string `json:"status"`
	CapturedBytes int    `json:"captured_bytes"`
	Truncated     bool   `json:"truncated"`
}

func requestContentCaptureConfig() (enabled bool, maxBytes int) {
	enabled = GetEnvOrDefaultBool(requestContentEnabledEnv, true)
	maxBytes = GetEnvOrDefault(requestContentMaxBytesEnv, RequestContentDefaultMaxBytes)
	if maxBytes < RequestContentMinMaxBytes {
		maxBytes = RequestContentMinMaxBytes
	}
	// Do not permit a capture larger than the server's request-body bound.
	upper := RequestContentMaxMaxBytes
	if mb := constant.MaxRequestBodyMB; mb > 0 && mb < upper>>20 {
		upper = mb << 20
	}
	return enabled, min(maxBytes, upper)
}

type requestContentCapture struct {
	mu       sync.Mutex
	snapshot RequestContentSnapshot
	captured bool
	enabled  bool
	maxBytes int
}

// BeginRequestContentCapture marks an authenticated inference request before any
// protocol preparation, plugin or parameter override can rewrite its body. It
// returns true only for the middleware that owns this capture's lifetime.
func BeginRequestContentCapture(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodPost || c.GetInt("id") <= 0 {
		return false
	}
	if _, exists := c.Get(requestContentContextKey); exists {
		return false
	}
	if c.GetString(RequestIdKey) == "" {
		c.Set(RequestIdKey, NewRequestId())
	}
	enabled, maxBytes := requestContentCaptureConfig()
	status := RequestContentStatusUnavailable
	if !enabled {
		status = RequestContentStatusDisabled
	}
	c.Set(requestContentContextKey, &requestContentCapture{
		snapshot: RequestContentSnapshot{
			Ref: GetUUID(), RequestID: c.GetString(RequestIdKey), UserID: c.GetInt("id"),
			SubmitNodeName: NodeName, CreatedAt: GetTimestamp(), Status: status,
		},
		enabled: enabled, maxBytes: maxBytes,
		captured: !enabled,
	})
	return true
}

// RequestContent returns a detached, immutable snapshot. Payload owns its bytes.
func RequestContent(c *gin.Context) *RequestContentSnapshot {
	if c == nil {
		return nil
	}
	state, ok := requestContentState(c)
	if !ok {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	snapshot := state.snapshot
	snapshot.Payload = slices.Clone(snapshot.Payload)
	return &snapshot
}

// RestoreRequestContent installs a persisted submission snapshot on a later
// request/task operation. It never changes the executing node's identity.
func RestoreRequestContent(c *gin.Context, snapshot *RequestContentSnapshot) {
	if c == nil || snapshot == nil || snapshot.Ref == "" {
		return
	}
	_, maxBytes := requestContentCaptureConfig()
	restored := *snapshot
	restored.Payload = slices.Clone(snapshot.Payload)
	c.Set(requestContentContextKey, &requestContentCapture{
		snapshot: restored, enabled: true, maxBytes: maxBytes, captured: true,
	})
}

func requestContentState(c *gin.Context) (*requestContentCapture, bool) {
	value, exists := c.Get(requestContentContextKey)
	if !exists {
		return nil, false
	}
	state, ok := value.(*requestContentCapture)
	return state, ok
}

// captureRequestContent copies the whitelisted original fields out of the first
// body storage of this request. The caller may replace or close that storage;
// the snapshot keeps its own bytes.
func captureRequestContent(c *gin.Context, storage BodyStorage) {
	state, ok := requestContentState(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.captured {
		return
	}
	state.captured = true
	if !state.enabled {
		state.snapshot.Status = RequestContentStatusDisabled
		return
	}
	state.snapshot.Status = RequestContentStatusInvalid
	reader, err := storage.NewReader()
	if err != nil {
		state.snapshot.Status = RequestContentStatusUnavailable
		return
	}
	defer reader.Close()
	mediaType, parameters, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		state.snapshot.Status = RequestContentStatusUnsupported
		return
	}
	fields := map[string]any{}
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		// A typed projection skips unrelated top-level extensions and media
		// without copying them into the diagnostic snapshot.
		var original *struct {
			Messages          RawMessage `json:"messages"`
			Prompt            RawMessage `json:"prompt"`
			Input             RawMessage `json:"input"`
			Instructions      RawMessage `json:"instructions"`
			System            RawMessage `json:"system"`
			Contents          RawMessage `json:"contents"`
			SystemInstruction RawMessage `json:"systemInstruction"`
		}
		if DecodeJsonBody(reader, &original) != nil || original == nil {
			return
		}
		selected := [7]RawMessage{original.Messages, original.Prompt, original.Input, original.Instructions, original.System, original.Contents, original.SystemInstruction}
		for i, raw := range selected {
			if len(raw) == 0 || GetJsonType(raw) == "null" {
				continue
			}
			var field any
			if Unmarshal(raw, &field) != nil {
				return
			}
			fields[requestContentFields[i]] = field
		}
	case mediaType == "multipart/form-data":
		parts := multipart.NewReader(reader, parameters["boundary"])
		for {
			part, partErr := parts.NextPart()
			if partErr == io.EOF {
				break
			}
			if partErr != nil {
				return
			}
			name := part.FormName()
			partType := part.Header.Get("Content-Type")
			if part.FileName() != "" || !slices.Contains(requestContentFields, name) || fields[name] != nil ||
				(partType != "" && !strings.EqualFold(strings.TrimSpace(strings.SplitN(partType, ";", 2)[0]), "text/plain")) {
				_ = part.Close()
				continue
			}
			text, readErr := io.ReadAll(io.LimitReader(part, int64(state.maxBytes)+1))
			_ = part.Close()
			if readErr != nil {
				return
			}
			if len(text) > state.maxBytes {
				state.snapshot.Truncated = true
				text = text[:state.maxBytes]
			}
			fields[name] = strings.ToValidUTF8(string(text), "")
		}
	default:
		state.snapshot.Status = RequestContentStatusUnsupported
		return
	}
	if len(fields) == 0 {
		state.snapshot.Status = RequestContentStatusNoContent
		return
	}
	// The budget includes JSON punctuation, escaped strings and field names. The
	// final check is authoritative even for heavily escaped values.
	budget := state.maxBytes - 2
	bounded := boundRequestContent(fields, &budget, 0, &state.snapshot.Truncated)
	payload, err := Marshal(bounded)
	if err != nil || len(payload) > state.maxBytes || GetJsonType(payload) != "object" {
		state.snapshot.Status = RequestContentStatusUnavailable
		return
	}
	state.snapshot.Payload = payload
	state.snapshot.CapturedBytes = len(payload)
	state.snapshot.Status = RequestContentStatusRecorded
	if state.snapshot.Truncated {
		state.snapshot.Status = RequestContentStatusTruncated
	}
}

// boundRequestContent keeps prompt structure while removing inline media and
// credentials in structured members. Limits apply before persistence so the
// stored value always stays valid UTF-8 JSON.
func boundRequestContent(value any, budget *int, depth int, truncated *bool) any {
	if depth > 64 || *budget < 4 {
		*truncated = true
		return nil
	}
	switch item := value.(type) {
	case string:
		if prefix := strings.TrimSpace(item); len(prefix) >= 5 && strings.EqualFold(prefix[:5], "data:") {
			item = "[inline media omitted]"
		}
		if len(item) <= *budget {
			encoded, _ := Marshal(item)
			if len(encoded) <= *budget {
				*budget -= len(encoded)
				return item
			}
		}
		*truncated = true
		low, high := 0, min(len(item), *budget)
		for low < high {
			middle := low + (high-low+1)/2
			candidate := strings.ToValidUTF8(item[:middle], "")
			encoded, _ := Marshal(candidate)
			if len(encoded) <= *budget {
				low = middle
			} else {
				high = middle - 1
			}
		}
		item = strings.ToValidUTF8(item[:low], "")
		encoded, _ := Marshal(item)
		*budget -= len(encoded)
		return item
	case []any:
		result := make([]any, 0, min(len(item), 1024))
		*budget -= 2
		for index, element := range item {
			if index >= 1024 || *budget < 8 {
				*truncated = true
				break
			}
			*budget--
			result = append(result, boundRequestContent(element, budget, depth+1, truncated))
		}
		return result
	case map[string]any:
		result := make(map[string]any)
		*budget -= 2
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for index, key := range keys {
			lower := strings.ToLower(key)
			element := item[key]
			if depth > 0 {
				// Allow only text/message structure, never arbitrary nested
				// provider metadata, credential fields, or file contents.
				switch lower {
				case "role", "type", "name", "content", "parts", "text", "input_text", "output_text", "image_url", "audio_url", "source", "mime_type", "mimetype", "format", "detail", "inline_data", "inlinedata", "input_audio", "input_file":
				case "url":
					// A media URL can include a signed access credential. Plain
					// URLs in prompt strings are unaffected.
					if _, ok := element.(string); !ok {
						continue
					}
					url, _ := element.(string)
					if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(url)), "data:") {
						element = "[media omitted]"
					}
				case "data", "b64_json", "base64", "file_data", "filedata", "file_url", "file_uri", "fileuri", "file_id", "filename", "audio", "image", "video", "document", "bytes", "bytesbase64encoded":
					element = "[media omitted]"
				default:
					continue
				}
			}
			keyJSON, _ := Marshal(key)
			if index >= 1024 || len(keyJSON)+8 > *budget {
				*truncated = true
				break
			}
			*budget -= len(keyJSON) + 2
			result[key] = boundRequestContent(element, budget, depth+1, truncated)
		}
		return result
	default:
		encoded, err := Marshal(item)
		if err != nil || len(encoded) > *budget {
			*truncated = true
			return nil
		}
		*budget -= len(encoded)
		return item
	}
}
