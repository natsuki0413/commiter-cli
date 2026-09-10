package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/natsuki0413/commiter-cli/internal/config"
	"github.com/natsuki0413/commiter-cli/internal/exitcode"
)

func TestNewRejectsNonLoopbackAndEndpointDecorations(t *testing.T) {
	tests := []string{
		"https://127.0.0.1:11434",
		"http://example.com:11434",
		"http://127.0.0.1:11434/api",
		"http://user@127.0.0.1:11434",
		"http://127.0.0.1:11434?target=remote",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			_, err := New(config.Values{Endpoint: endpoint, Model: "model"})
			if exitcode.Code(err) != exitcode.LLM {
				t.Fatalf("New() error = %v, code = %d", err, exitcode.Code(err))
			}
		})
	}
}

func TestChatFixesSafetyFieldsAndReturnsContent(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/chat" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"model:tag","message":{"content":"{\"schema_version\":1}"},"done":true,"prompt_eval_count":7}`)
	}))
	defer server.Close()

	client, err := New(config.Values{Endpoint: server.URL, Model: "model:tag"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Chat(context.Background(), []Message{{Role: "user", Content: "private prompt"}}, json.RawMessage(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != `{"schema_version":1}` || response.PromptEvalCount != 7 {
		t.Fatalf("response = %#v", response)
	}
	if received["think"] != false || received["stream"] != false || received["keep_alive"] != float64(0) {
		t.Fatalf("fixed fields = think:%v stream:%v keep_alive:%v", received["think"], received["stream"], received["keep_alive"])
	}
	if received["model"] != "model:tag" {
		t.Fatalf("model = %v", received["model"])
	}
	format, ok := received["format"].(map[string]any)
	if !ok || format["type"] != "object" {
		t.Fatalf("format = %#v", received["format"])
	}
}

func TestChatRejectsRedirectWithoutSendingContentToTarget(t *testing.T) {
	targetRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests++
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client, err := New(config.Values{Endpoint: source.URL, Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Chat(context.Background(), []Message{{Role: "user", Content: "must-not-redirect"}}, json.RawMessage(`{"type":"object"}`))
	if err == nil || !strings.Contains(err.Error(), "status 307") {
		t.Fatalf("Chat() error = %v", err)
	}
	if targetRequests != 0 {
		t.Fatalf("redirect target received %d requests", targetRequests)
	}
}

func TestCompatibilityAndModelFailuresAreLLMErrorsWithoutPull(t *testing.T) {
	paths := []string{}
	client := testClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/api/version":
			return jsonResponse(http.StatusOK, `{"version":"0.12.6"}`), nil
		case "/api/tags":
			return jsonResponse(http.StatusOK, `{"models":[]}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	}))

	if err := client.Compatibility(context.Background()); err != nil {
		t.Fatal(err)
	}
	present, err := client.HasModel(context.Background())
	if err != nil || present {
		t.Fatalf("HasModel() = %v, %v", present, err)
	}
	if err := requireModel(context.Background(), client); exitcode.Code(err) != exitcode.LLM {
		t.Fatalf("requireModel() error = %v, code = %d", err, exitcode.Code(err))
	}
	for _, path := range paths {
		if path == "/api/pull" {
			t.Fatal("normal execution attempted to pull a model")
		}
	}
}

func TestCompatibilityRejectsMalformedAPI(t *testing.T) {
	client := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"unexpected":true}`), nil
	}))
	if err := client.Compatibility(context.Background()); exitcode.Code(err) != exitcode.LLM {
		t.Fatalf("Compatibility() error = %v, code = %d", err, exitcode.Code(err))
	}
}

func TestCompatibilityRequiresVersionWithNeededFeatures(t *testing.T) {
	tests := []struct {
		version string
		wantErr bool
	}{
		{version: "0.8.0", wantErr: true},
		{version: "0.9.0-rc1", wantErr: true},
		{version: "0.9.0"},
		{version: "0.12.6"},
		{version: "1.0.0+build"},
		{version: "foo", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			client := testClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, `{"version":"`+test.version+`"}`), nil
			}))
			err := client.Compatibility(context.Background())
			if (err != nil) != test.wantErr {
				t.Fatalf("Compatibility() error = %v, wantErr = %v", err, test.wantErr)
			}
			if err != nil && exitcode.Code(err) != exitcode.LLM {
				t.Fatalf("Compatibility() code = %d", exitcode.Code(err))
			}
		})
	}
}

func testClient(transport http.RoundTripper) *Client {
	client, err := New(config.Values{Endpoint: "http://127.0.0.1:11434", Model: "model"})
	if err != nil {
		panic(err)
	}
	client.http = &http.Client{Transport: transport, CheckRedirect: rejectRedirect}
	return client
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func connectionRefused() error {
	return errors.Join(errors.New("connection refused"), syscall.ECONNREFUSED)
}
