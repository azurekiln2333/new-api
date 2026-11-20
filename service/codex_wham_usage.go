package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
)

// CodexWhamRequestSender dispatches a prepared Wham request after its default
// headers are set. The caller applies channel headers, transport and admission;
// each fetch function reads and closes the returned response body.
type CodexWhamRequestSender func(*http.Request) (*http.Response, error)

func FetchCodexWhamUsage(
	ctx context.Context,
	send CodexWhamRequestSender,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if send == nil {
		return 0, nil, fmt.Errorf("nil request sender")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bu+"/backend-api/wham/usage", nil)
	if err != nil {
		return 0, nil, err
	}
	setCodexWhamRequestHeaders(req, at, aid)

	resp, err := send(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func FetchCodexWhamRateLimitResetCredits(
	ctx context.Context,
	send CodexWhamRequestSender,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if send == nil {
		return 0, nil, fmt.Errorf("nil request sender")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bu+"/backend-api/wham/rate-limit-reset-credits", nil)
	if err != nil {
		return 0, nil, err
	}
	setCodexWhamRequestHeaders(req, at, aid)

	resp, err := send(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func ConsumeCodexWhamRateLimitResetCredit(
	ctx context.Context,
	send CodexWhamRequestSender,
	baseURL string,
	accessToken string,
	accountID string,
) (statusCode int, body []byte, err error) {
	if send == nil {
		return 0, nil, fmt.Errorf("nil request sender")
	}
	bu := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if bu == "" {
		return 0, nil, fmt.Errorf("empty baseURL")
	}
	at := strings.TrimSpace(accessToken)
	aid := strings.TrimSpace(accountID)
	if at == "" {
		return 0, nil, fmt.Errorf("empty accessToken")
	}
	if aid == "" {
		return 0, nil, fmt.Errorf("empty accountID")
	}

	requestBody, err := common.Marshal(map[string]string{
		"redeem_request_id": uuid.NewString(),
	})
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		bu+"/backend-api/wham/rate-limit-reset-credits/consume",
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return 0, nil, err
	}
	setCodexWhamRequestHeaders(req, at, aid)
	req.Header.Set("Content-Type", "application/json")

	resp, err := send(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, body, nil
}

func setCodexWhamRequestHeaders(req *http.Request, accessToken string, accountID string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("chatgpt-account-id", accountID)
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("originator") == "" {
		req.Header.Set("originator", "codex_cli_rs")
	}
}
