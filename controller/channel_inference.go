package controller

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/common/expfmt"
)

type inferenceEndpointStatus struct {
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

type inferenceModel struct {
	ID          string `json:"id"`
	Root        string `json:"root"`
	MaxModelLen *int64 `json:"max_model_len"`
}

type inferenceMetric struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

type inferenceStatus struct {
	SampledAt  int64                              `json:"sampled_at"`
	Endpoints  map[string]inferenceEndpointStatus `json:"endpoints"`
	Version    string                             `json:"version"`
	Models     []inferenceModel                   `json:"models"`
	Metrics    []inferenceMetric                  `json:"metrics"`
	RawMetrics string                             `json:"raw_metrics"`
}

func GetVLLMChannelStatus(c *gin.Context) {
	getInferenceChannelStatus(c, constant.ChannelTypeVLLM)
}

func GetSGLangChannelStatus(c *gin.Context) {
	getInferenceChannelStatus(c, constant.ChannelTypeSGLang)
}

func getInferenceChannelStatus(c *gin.Context, channelType int) {
	c.Header("Cache-Control", "no-store")
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "Invalid channel id"})
		return
	}
	channel, err := model.GetChannelById(id, true)
	if err != nil || channel == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "Channel not found"})
		return
	}
	if channel.Type != channelType {
		common.ApiError(c, errors.New("Channel type does not match the status endpoint"))
		return
	}
	status, err := fetchInferenceStatus(c.Request.Context(), channel)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, status)
}

func inferenceStatusBaseURL(channel *model.Channel) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(channel.GetBaseURL()), "/")
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Host == "" || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return "", errors.New("Invalid inference server address")
	}
	return baseURL, nil
}

func inferenceStatusHeaders(channel *model.Channel) (http.Header, error) {
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return nil, errors.New("No enabled channel key")
	}
	key = strings.TrimSpace(key)
	headers, err := buildFetchModelsHeaders(channel, key)
	if err != nil {
		return nil, errors.New("Invalid channel header override")
	}
	if (key == "" || key == "EMPTY") && headers.Get("Authorization") == "Bearer "+key {
		headers.Del("Authorization")
	}
	return headers, nil
}

// fetchInferenceStatus reads only fixed paths on an operator-configured channel.
// Each endpoint can fail independently; upstream errors never echo credentials.
func fetchInferenceStatus(ctx context.Context, channel *model.Channel) (*inferenceStatus, error) {
	if channel.Type != constant.ChannelTypeVLLM && channel.Type != constant.ChannelTypeSGLang {
		return nil, errors.New("This operation is only supported for vLLM or SGLang channels")
	}
	baseURL, err := inferenceStatusBaseURL(channel)
	if err != nil {
		return nil, err
	}
	settings := channel.GetSetting()
	var previewHeaders http.Header
	if channel.Id == 0 {
		previewHeaders, err = inferenceStatusHeaders(channel)
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	versionPath, metricPrefix := "/version", "vllm:"
	if channel.Type == constant.ChannelTypeSGLang {
		versionPath, metricPrefix = "/server_info", "sglang:"
	}
	paths := []string{"/health", versionPath, "/v1/models", "/metrics"}
	results := make([]struct {
		inferenceEndpointStatus
		body []byte
		at   int64
	}, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Go(func() {
			result := &results[i]
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
			if err != nil {
				result.Error = "request_failed"
				return
			}
			req.Header = previewHeaders.Clone()
			// Each endpoint is its own exchange, sharing the panel's deadline
			// but holding a separate slot until its own response body closes.
			admission := service.NewChannelDiagnosticAdmission(ctx, 0)
			defer admission.Close()
			call := service.ChannelManagementCall{
				Request: req, Settings: settings, ChannelID: channel.Id,
				Admission: admission, NoRedirect: true,
			}
			if channel.Id > 0 {
				call.Reload = func() (*http.Request, dto.ChannelSettings, error) {
					var current model.Channel
					if err := model.DB.WithContext(ctx).First(&current, channel.Id).Error; err != nil || current.Type != channel.Type {
						return nil, dto.ChannelSettings{}, errors.New("Channel not found")
					}
					base, err := inferenceStatusBaseURL(&current)
					if err != nil {
						return nil, dto.ChannelSettings{}, err
					}
					fresh, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
					if err != nil {
						return nil, dto.ChannelSettings{}, err
					}
					fresh.Header, err = inferenceStatusHeaders(&current)
					return fresh, current.GetSetting(), err
				}
			}
			resp, err := service.DoChannelManagementRequest(call)
			if err != nil {
				result.Error = "request_failed"
				return
			}
			defer resp.Body.Close()
			result.Status = resp.StatusCode
			if resp.StatusCode != http.StatusOK {
				result.Error = "http_error"
				return
			}
			const maxResponseBytes = 4 << 20
			body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
			if err != nil {
				result.Error = "request_failed"
				return
			}
			if len(body) > maxResponseBytes {
				result.Error = "response_too_large"
				return
			}
			result.body = body
			result.at = time.Now().UnixMilli()
		})
	}
	wg.Wait()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, context.Canceled
	}
	status := &inferenceStatus{
		SampledAt: time.Now().UnixMilli(),
		Endpoints: make(map[string]inferenceEndpointStatus, len(paths)),
		Models:    []inferenceModel{},
		Metrics:   []inferenceMetric{},
	}
	for i, path := range paths {
		result := &results[i]
		if result.Error == "" {
			switch path {
			case versionPath:
				// /server_info includes the launch configuration. Decode only the
				// version so API keys and other server settings never reach the browser.
				var version struct {
					Version string `json:"version"`
				}
				if err := common.Unmarshal(result.body, &version); err != nil || version.Version == "" {
					result.Error = "invalid_response"
				} else {
					status.Version = version.Version
				}
			case "/v1/models":
				var models struct {
					Data []inferenceModel `json:"data"`
				}
				if err := common.Unmarshal(result.body, &models); err != nil || models.Data == nil {
					result.Error = "invalid_response"
				} else {
					status.Models = models.Data
				}
			case "/metrics":
				metrics, err := parseInferenceMetrics(string(result.body), metricPrefix)
				if err != nil {
					result.Error = "invalid_response"
				} else {
					status.Metrics = metrics
					status.RawMetrics = string(result.body)
					status.SampledAt = result.at
				}
			}
		}
		status.Endpoints[path] = result.inferenceEndpointStatus
	}
	return status, nil
}

func parseInferenceMetrics(raw string, prefix string) ([]inferenceMetric, error) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(strings.NewReader(raw))
	if err != nil {
		return nil, err
	}
	metrics := []inferenceMetric{}
	for name, family := range families {
		if !strings.HasPrefix(name, prefix) && name != "process_start_time_seconds" {
			continue
		}
		samples, err := expfmt.ExtractSamples(&expfmt.DecodeOptions{}, family)
		if err != nil {
			return nil, err
		}
		for _, sample := range samples {
			name := string(sample.Metric["__name__"])
			value := float64(sample.Value)
			if strings.HasSuffix(name, "_bucket") || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				continue
			}
			labels := make(map[string]string, len(sample.Metric)-1)
			for label, value := range sample.Metric {
				if label != "__name__" {
					labels[string(label)] = string(value)
				}
			}
			metrics = append(metrics, inferenceMetric{Name: name, Labels: labels, Value: value})
		}
	}
	return metrics, nil
}
