package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Ollama talks to a local Ollama server. It keeps every token on this machine,
// but output quality depends heavily on the available GPU, so it is not the
// recommended default for candidate-facing text.
type Ollama struct {
	BaseURL   string
	ModelName string
	Timeout   time.Duration
	client    *http.Client
}

func NewOllama(baseURL, model string, timeout time.Duration) *Ollama {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://localhost:11434"
	}
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Ollama{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		ModelName: model,
		Timeout:   timeout,
		client:    &http.Client{Timeout: timeout},
	}
}

func (o *Ollama) Name() string { return "ollama" }

func (o *Ollama) Model() string {
	if strings.TrimSpace(o.ModelName) == "" {
		return "llama3.1"
	}
	return o.ModelName
}

func (o *Ollama) Complete(ctx context.Context, req Request) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	options := map[string]any{}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	if req.MaxTokens > 0 {
		options["num_predict"] = req.MaxTokens
	}
	body, err := json.Marshal(map[string]any{
		"model":   o.Model(),
		"system":  req.System,
		"prompt":  req.Prompt,
		"stream":  false,
		"options": options,
	})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := o.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama returned %d: %s", response.StatusCode, truncate(string(payload), 300))
	}
	var decoded struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err = json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("ollama returned an unreadable response: %w", err)
	}
	if decoded.Error != "" {
		return "", fmt.Errorf("ollama error: %s", decoded.Error)
	}
	return decoded.Response, nil
}
