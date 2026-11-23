package common

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplayableBodiesOverOneStorageKeepOwnCursors(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	payload := []byte(`{"model":"test-model","input":"` + strings.Repeat("x", 4<<10) + `"}`)
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "file"}[disk], func(t *testing.T) {
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
			storage, err := CreateBodyStorage(payload)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, storage.Close()) })
			require.Equal(t, disk, storage.IsDisk())

			// The transport may still be writing attempt 1 when the retry loop
			// seeks the shared cursor and attempt 2 forwards the same body.
			first, err := http.NewRequest(http.MethodPost, "https://example.com", NewReplayableBodyReader(storage))
			require.NoError(t, err)
			head := make([]byte, len(payload)/2)
			_, err = io.ReadFull(first.Body, head)
			require.NoError(t, err)
			_, err = storage.Seek(0, io.SeekStart)
			require.NoError(t, err)
			secondBody := NewReplayableBodyReader(storage)
			second, err := http.NewRequest(http.MethodPost, "https://example.com", secondBody)
			require.NoError(t, err)
			sent, err := io.ReadAll(second.Body)
			require.NoError(t, err)
			assert.Equal(t, payload, sent)
			rest, err := io.ReadAll(first.Body)
			require.NoError(t, err)
			assert.Equal(t, payload, append(head, rest...))

			// Closing request bodies, as net/http does, keeps the storage for
			// GetBody replays and later attempts.
			require.NoError(t, first.Body.Close())
			require.NoError(t, second.Body.Close())
			_, err = first.Body.Read(make([]byte, 1))
			assert.ErrorIs(t, err, http.ErrBodyReadAfterClose)
			replay, err := secondBody.NewReader()
			require.NoError(t, err)
			replayed, err := io.ReadAll(replay)
			require.NoError(t, err)
			require.NoError(t, replay.Close())
			assert.Equal(t, payload, replayed)

			require.NoError(t, storage.Close())
			_, err = io.ReadAll(NewReplayableBodyReader(storage))
			assert.ErrorIs(t, err, ErrStorageClosed)
		})
	}
}

func TestUnmarshalBodyReusableNoCopySurvivesBodyReplacement(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	input := strings.Repeat("image-data", 32<<10)
	payload := []byte(`{"model":"first","input":"` + input + `","metadata":{"count":0,"enabled":false}}`)
	for _, disk := range []bool{false, true} {
		name := "memory"
		if disk {
			name = "file"
		}
		t.Run(name, func(t *testing.T) {
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
			storage, err := CreateBodyStorage(payload)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, storage.Close()) })
			require.Equal(t, disk, storage.IsDisk())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(payload))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(KeyBodyStorage, storage)
			var decoded struct {
				Model    string          `json:"model"`
				Input    string          `json:"input"`
				Metadata json.RawMessage `json:"metadata"`
			}
			require.NoError(t, UnmarshalBodyReusableNoCopy(c, &decoded))
			assert.Equal(t, "first", decoded.Model)
			assert.Equal(t, input, decoded.Input)
			assert.Equal(t, `{"count":0,"enabled":false}`, string(decoded.Metadata))
			replayed, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			assert.Equal(t, payload, replayed)
			replacement, err := CreateBodyStorage([]byte(`{"model":"second"}`))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, replacement.Close()) })
			c.Set(KeyBodyStorage, replacement)
			require.NoError(t, storage.Close())
			var next map[string]any
			require.NoError(t, UnmarshalBodyReusableNoCopy(c, &next))
			assert.Equal(t, "second", next["model"])
			assert.Equal(t, "first", decoded.Model)
			assert.Equal(t, input, decoded.Input)
			assert.Equal(t, `{"count":0,"enabled":false}`, string(decoded.Metadata))
		})
	}
}

func TestMultipartBodyStorageReplaysFilesAndFields(t *testing.T) {
	previous := GetDiskCacheConfig()
	previousLimit := constant.MaxFileDownloadMB
	t.Cleanup(func() { SetDiskCacheConfig(previous); constant.MaxFileDownloadMB = previousLimit })
	constant.MaxFileDownloadMB = 1
	fileContents := bytes.Repeat([]byte("image"), 300000)
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	require.NoError(t, writer.WriteField("tag", "one"))
	require.NoError(t, writer.WriteField("tag", "two"))
	part, err := writer.CreateFormFile("image", "image.png")
	require.NoError(t, err)
	_, err = part.Write(fileContents)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "file"}[disk], func(t *testing.T) {
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
			storage, err := CreateBodyStorage(payload.Bytes())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, storage.Close()) })
			require.Equal(t, disk, storage.IsDisk())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(payload.Bytes()))
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			c.Set(KeyBodyStorage, storage)
			form, err := ParseMultipartFormReusable(c)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, form.RemoveAll()) })
			assert.Equal(t, []string{"one", "two"}, form.Value["tag"])
			require.Len(t, form.File["image"], 1)
			file, err := form.File["image"][0].Open()
			require.NoError(t, err)
			actual, err := io.ReadAll(file)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			assert.Equal(t, fileContents, actual)
			// Upstream rebuilding may have replaced the multipart boundary.
			c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=outbound")
			var fields map[string]any
			require.NoError(t, UnmarshalBodyReusableNoCopy(c, &fields))
			assert.Equal(t, map[string]any{"tag": []any{"one", "two"}}, fields)
			replay, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			assert.Equal(t, payload.Bytes(), replay)
		})
	}
}

func TestReusableJSONBodyRequiresOneCompleteValue(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	for _, contentType := range []string{"application/json", "multipart/form-data"} {
		for _, disk := range []bool{false, true} {
			for _, suffix := range []string{" \n\t", " {}", " null", " garbage"} {
				t.Run(contentType+"/"+map[bool]string{false: "memory", true: "file"}[disk]+"/"+suffix, func(t *testing.T) {
					SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
					body := `{"ignored":"` + strings.Repeat("a", 256<<10) + `","n":0}` + suffix
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/body", strings.NewReader(body))
					c.Request.Header.Set("Content-Type", contentType)
					t.Cleanup(func() { CleanupBodyStorage(c) })
					var decoded struct {
						N *uint `json:"n"`
					}
					err := UnmarshalBodyReusableNoCopy(c, &decoded)
					if strings.TrimSpace(suffix) != "" {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					require.NotNil(t, decoded.N)
					assert.Zero(t, *decoded.N)
				})
			}
		}
	}
}

func BenchmarkUnmarshalBodyReusableImageRequest(b *testing.B) {
	previous := GetDiskCacheConfig()
	SetDiskCacheConfig(DiskCacheConfig{Path: b.TempDir()})
	b.Cleanup(func() { SetDiskCacheConfig(previous) })
	payload := []byte(`{"model":"image-model","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,` + strings.Repeat("A", 1<<20) + `"}}]}]}`)
	for _, mode := range []string{"memory_copy", "memory_no_copy", "file_read_all", "file_stream"} {
		b.Run(mode, func(b *testing.B) {
			var storage BodyStorage
			if strings.HasPrefix(mode, "file") {
				var err error
				storage, err = newDiskStorage(payload, GetDiskCachePath())
				require.NoError(b, err)
			} else {
				storage = newMemoryStorage(payload)
			}
			b.Cleanup(func() { require.NoError(b, storage.Close()) })
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(KeyBodyStorage, storage)
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			for b.Loop() {
				var request dto.GeneralOpenAIRequest
				var err error
				switch mode {
				case "memory_no_copy":
					err = UnmarshalBodyReusableNoCopy(c, &request)
				case "file_read_all":
					var data []byte
					data, err = storage.Bytes()
					if err == nil {
						err = Unmarshal(data, &request)
					}
				default:
					err = UnmarshalBodyReusable(c, &request)
				}
				require.NoError(b, err)
			}
		})
	}
}

func BenchmarkMultipartBodyStorage(b *testing.B) {
	previous := GetDiskCacheConfig()
	previousLimit := constant.MaxFileDownloadMB
	SetDiskCacheConfig(DiskCacheConfig{Path: b.TempDir()})
	constant.MaxFileDownloadMB = 1
	b.Cleanup(func() { SetDiskCacheConfig(previous); constant.MaxFileDownloadMB = previousLimit })
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	require.NoError(b, writer.WriteField("model", "image-model"))
	part, err := writer.CreateFormFile("image", "image.png")
	require.NoError(b, err)
	_, err = io.Copy(part, strings.NewReader(strings.Repeat("A", 8<<20)))
	require.NoError(b, err)
	require.NoError(b, writer.Close())
	storage, err := newDiskStorage(payload.Bytes(), GetDiskCachePath())
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, storage.Close()) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	c.Set(KeyBodyStorage, storage)
	for _, mode := range []string{"file_read_all", "file_stream"} {
		b.Run(mode, func(b *testing.B) {
			b.SetBytes(storage.Size())
			b.ReportAllocs()
			for b.Loop() {
				var form *multipart.Form
				if mode == "file_read_all" {
					body, err := storage.Bytes()
					require.NoError(b, err)
					form, err = multipart.NewReader(bytes.NewReader(body), writer.Boundary()).ReadForm(multipartMemoryLimit())
					require.NoError(b, err)
				} else {
					var err error
					form, err = ParseMultipartFormReusable(c)
					require.NoError(b, err)
				}
				require.NoError(b, form.RemoveAll())
			}
		})
	}
}

// requestContentTestContext builds an authenticated relay context whose body is
// stored exactly like the real ingress path.
func requestContentTestContext(t *testing.T, contentType string, payload []byte) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", contentType)
	c.Set("id", 7)
	storage, err := CreateBodyStorage(payload)
	require.NoError(t, err)
	c.Set(KeyBodyStorage, storage)
	t.Cleanup(func() { CleanupBodyStorage(c) })
	BeginRequestContentCapture(c)
	return c
}

func TestRequestContentCaptureKeepsOriginalPromptBeforeRewrite(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })

	payload := []byte(`{"model":"gpt-4o","messages":[{"role":"system","content":"be brief"},{"role":"user","content":[{"type":"text","text":"original prompt"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}],"api_key":"must-not-appear","prompt":"legacy prompt"}`)
	SetDiskCacheConfig(DiskCacheConfig{Enabled: false})
	for _, disk := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "file"}[disk], func(t *testing.T) {
			SetDiskCacheConfig(DiskCacheConfig{Enabled: disk, ThresholdMB: 0, MaxSizeMB: 16, Path: t.TempDir()})
			c := requestContentTestContext(t, "application/json", payload)
			// The protocol/plugin layer reads the body first; capture happens there.
			_, err := GetRequestBody(c)
			require.NoError(t, err)

			snapshot := RequestContent(c)
			require.NotNil(t, snapshot)
			assert.Equal(t, RequestContentStatusRecorded, snapshot.Status)
			assert.Equal(t, NodeName, snapshot.SubmitNodeName)
			assert.NotEmpty(t, snapshot.Ref)
			assert.NotContains(t, string(snapshot.Payload), "must-not-appear")
			assert.Contains(t, string(snapshot.Payload), "original prompt")
			assert.Contains(t, string(snapshot.Payload), "[inline media omitted]")
			assert.NotContains(t, string(snapshot.Payload), "data:image/png;base64")
			assert.Contains(t, string(snapshot.Payload), "legacy prompt")
			serialized, err := Marshal(snapshot)
			require.NoError(t, err)
			assert.NotContains(t, string(serialized), "original prompt", "snapshots must not serialize prompt text into diagnostic parameters")

			// A later rewrite of the storage must not retroactively change the
			// original snapshot, and closing the storage keeps it readable.
			replacement, err := CreateBodyStorage([]byte(`{"messages":[{"role":"user","content":"rewritten"}]}`))
			require.NoError(t, err)
			CleanupBodyStorage(c)
			c.Set(KeyBodyStorage, replacement)
			_, err = GetRequestBody(c)
			require.NoError(t, err, "captured snapshot must not depend on the closed storage")
			again := RequestContent(c)
			require.NotNil(t, again)
			assert.Equal(t, string(snapshot.Payload), string(again.Payload))
		})
	}
}

func TestRequestContentCaptureStatusesAndBudget(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	SetDiskCacheConfig(DiskCacheConfig{Enabled: false})

	t.Run("no_content", func(t *testing.T) {
		c := requestContentTestContext(t, "application/json", []byte(`{"model":"gpt-4o","stream":true}`))
		_, err := GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusNoContent, snapshot.Status)
		assert.Empty(t, snapshot.Payload)
	})

	t.Run("invalid", func(t *testing.T) {
		c := requestContentTestContext(t, "application/json", []byte(`{"messages":[`))
		_, err := GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusInvalid, snapshot.Status)
		assert.Empty(t, snapshot.Payload)
	})

	t.Run("unsupported", func(t *testing.T) {
		c := requestContentTestContext(t, "application/octet-stream", []byte{0x00, 0x01})
		_, err := GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusUnsupported, snapshot.Status)
	})

	t.Run("bounded_valid_json", func(t *testing.T) {
		t.Setenv(requestContentMaxBytesEnv, "2048")
		long := strings.Repeat("😀\"\\\n", 4000)
		body, err := Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": long}}})
		require.NoError(t, err)
		c := requestContentTestContext(t, "application/json", body)
		_, err = GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusTruncated, snapshot.Status)
		assert.True(t, snapshot.Truncated)
		assert.LessOrEqual(t, len(snapshot.Payload), 2048)
		assert.Equal(t, "object", GetJsonType(snapshot.Payload), "captured payload must stay valid JSON")
		var decoded map[string]any
		require.NoError(t, Unmarshal(snapshot.Payload, &decoded))
		assert.Equal(t, len(snapshot.Payload), snapshot.CapturedBytes)
	})

	t.Run("structured_media_and_credentials", func(t *testing.T) {
		c := requestContentTestContext(t, "application/json", []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"keep https://example.test/reference"},{"type":"image_url","image_url":{"url":"https://cdn.test/image?token=signed-secret"}},{"type":"input_audio","input_audio":{"data":"encoded-secret","format":"wav"}}],"api_key":"nested-secret","metadata":{"access_token":"metadata-secret"}}]}`))
		_, err := GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusRecorded, snapshot.Status)
		assert.Contains(t, string(snapshot.Payload), "keep https://example.test/reference")
		assert.Contains(t, string(snapshot.Payload), `"role":"user"`)
		assert.Contains(t, string(snapshot.Payload), "[media omitted]")
		for _, secret := range []string{"signed-secret", "encoded-secret", "nested-secret", "metadata-secret"} {
			assert.NotContains(t, string(snapshot.Payload), secret)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Setenv(requestContentEnabledEnv, "false")
		c := requestContentTestContext(t, "application/json", []byte(`{"messages":[{"role":"user","content":"secret"}]}`))
		_, err := GetRequestBody(c)
		require.NoError(t, err)
		snapshot := RequestContent(c)
		require.NotNil(t, snapshot)
		assert.Equal(t, RequestContentStatusDisabled, snapshot.Status)
		assert.Empty(t, snapshot.Payload)
	})
}

func TestRequestContentCaptureMultipartTextFieldsOnly(t *testing.T) {
	previous := GetDiskCacheConfig()
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	SetDiskCacheConfig(DiskCacheConfig{Enabled: false})
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	require.NoError(t, writer.WriteField("prompt", "draw a lighthouse"))
	require.NoError(t, writer.WriteField("authorization", "Bearer secret"))
	part, err := writer.CreateFormFile("image", "secret.png")
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte("binary"), 1000))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	c := requestContentTestContext(t, writer.FormDataContentType(), payload.Bytes())
	_, err = GetRequestBody(c)
	require.NoError(t, err)
	snapshot := RequestContent(c)
	require.NotNil(t, snapshot)
	assert.Equal(t, RequestContentStatusRecorded, snapshot.Status)
	assert.JSONEq(t, `{"prompt":"draw a lighthouse"}`, string(snapshot.Payload))
}
