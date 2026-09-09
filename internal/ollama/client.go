package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
)

const maxResponseBytes = 1 << 20

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Model              string
	Content            string
	TotalDuration      int64
	LoadDuration       int64
	PromptEvalCount    int
	PromptEvalDuration int64
	EvalCount          int
	EvalDuration       int64
}

type Client struct {
	endpoint *url.URL
	model    string
	http     *http.Client
}

func New(values config.Values) (*Client, error) {
	endpoint, err := validateEndpoint(values.Endpoint)
	if err != nil {
		return nil, llmError("Ollama endpoint must be a loopback HTTP URL")
	}
	if strings.TrimSpace(values.Model) == "" {
		return nil, llmError("Ollama model is not configured")
	}
	return &Client{
		endpoint: endpoint,
		model:    values.Model,
		http: &http.Client{
			Transport:     loopbackTransport(endpoint),
			CheckRedirect: rejectRedirect,
		},
	}, nil
}

func validateEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "http" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("invalid endpoint")
	}
	if endpoint.Path != "" && endpoint.Path != "/" {
		return nil, errors.New("invalid endpoint path")
	}
	host := endpoint.Hostname()
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, errors.New("endpoint is not loopback")
		}
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/")
	return endpoint, nil
}

func loopbackTransport(endpoint *url.URL) *http.Transport {
	expectedHost := endpoint.Hostname()
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(host, expectedHost) {
				return nil, errors.New("Ollama request target is not the configured loopback endpoint")
			}
			addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("cannot resolve Ollama loopback endpoint")
			}
			for _, resolved := range addresses {
				if !resolved.IP.IsLoopback() {
					return nil, errors.New("Ollama endpoint resolved outside loopback")
				}
			}
			dialer := &net.Dialer{}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
		},
	}
}

func rejectRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

func (c *Client) Compatibility(ctx context.Context) error {
	var response struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/api/version", &response); err != nil {
		return err
	}
	if strings.TrimSpace(response.Version) == "" {
		return llmError("Ollama API compatibility check failed")
	}
	return nil
}

func (c *Client) HasModel(ctx context.Context) (bool, error) {
	var response struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := c.get(ctx, "/api/tags", &response); err != nil {
		return false, err
	}
	for _, installed := range response.Models {
		if modelMatches(c.model, installed.Name) || modelMatches(c.model, installed.Model) {
			return true, nil
		}
	}
	return false, nil
}

func modelMatches(configured, installed string) bool {
	if configured == installed {
		return true
	}
	return !strings.Contains(configured, ":") && installed == configured+":latest"
}

func (c *Client) Chat(ctx context.Context, messages []Message, schema json.RawMessage) (ChatResponse, error) {
	if len(messages) == 0 || !validSchema(schema) {
		return ChatResponse{}, llmError("Ollama chat requires messages and a JSON Schema")
	}
	payload := struct {
		Model     string          `json:"model"`
		Messages  []Message       `json:"messages"`
		Format    json.RawMessage `json:"format"`
		Think     bool            `json:"think"`
		Stream    bool            `json:"stream"`
		KeepAlive int             `json:"keep_alive"`
	}{
		Model: c.model, Messages: messages, Format: schema,
		Think: false, Stream: false, KeepAlive: 0,
	}

	var response struct {
		Model   string `json:"model"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Done               bool  `json:"done"`
		TotalDuration      int64 `json:"total_duration"`
		LoadDuration       int64 `json:"load_duration"`
		PromptEvalCount    int   `json:"prompt_eval_count"`
		PromptEvalDuration int64 `json:"prompt_eval_duration"`
		EvalCount          int   `json:"eval_count"`
		EvalDuration       int64 `json:"eval_duration"`
	}
	if err := c.post(ctx, "/api/chat", payload, &response); err != nil {
		return ChatResponse{}, err
	}
	if !response.Done || strings.TrimSpace(response.Message.Content) == "" {
		return ChatResponse{}, llmError("Ollama returned an incomplete chat response")
	}
	return ChatResponse{
		Model: response.Model, Content: response.Message.Content,
		TotalDuration: response.TotalDuration, LoadDuration: response.LoadDuration,
		PromptEvalCount: response.PromptEvalCount, PromptEvalDuration: response.PromptEvalDuration,
		EvalCount: response.EvalCount, EvalDuration: response.EvalDuration,
	}, nil
}

func validSchema(schema json.RawMessage) bool {
	var object map[string]any
	return len(schema) > 0 && json.Unmarshal(schema, &object) == nil && object != nil
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint.String()+path, nil)
	if err != nil {
		return llmError("cannot create Ollama request")
	}
	return c.do(request, target)
}

func (c *Client) post(ctx context.Context, path string, payload, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return llmError("cannot encode Ollama request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String()+path, bytes.NewReader(body))
	if err != nil {
		return llmError("cannot create Ollama request")
	}
	request.Header.Set("Content-Type", "application/json")
	return c.do(request, target)
}

func (c *Client) do(request *http.Request, target any) error {
	response, err := c.http.Do(request)
	if err != nil {
		return transportError{cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return llmError(fmt.Sprintf("Ollama API request failed with status %d", response.StatusCode))
	}
	limited := &io.LimitedReader{R: response.Body, N: maxResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(target); err != nil {
		return llmError("Ollama API returned an invalid response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return llmError("Ollama API returned an invalid response")
	}
	if limited.N <= 0 {
		return llmError("Ollama API response exceeds the size limit")
	}
	return nil
}

type transportError struct{ cause error }

func (e transportError) Error() string { return "cannot connect to the local Ollama API" }
func (e transportError) Unwrap() error { return e.cause }

func llmError(message string) error {
	return exitcode.New(exitcode.LLM, message)
}
