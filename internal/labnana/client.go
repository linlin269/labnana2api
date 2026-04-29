package labnana

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"labnana2api/internal/config"
	"labnana2api/internal/logging"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *logging.Logger
}

type GenerateRequest struct {
	Provider        string           `json:"provider"`
	Model           string           `json:"model"`
	Prompt          string           `json:"prompt"`
	ReferenceImages []ReferenceImage `json:"referenceImages,omitempty"`
	ImageConfig     ImageConfig      `json:"imageConfig,omitempty"`
}

type ReferenceImage struct {
	FileData FileData `json:"fileData"`
}

type FileData struct {
	FileURI  string `json:"fileUri"`
	MimeType string `json:"mimeType"`
}

type ImageConfig struct {
	ImageSize   string `json:"imageSize,omitempty"`
	AspectRatio string `json:"aspectRatio,omitempty"`
}

type GeneratedImage struct {
	MimeType string
	Data     []byte
}

type APIError struct {
	StatusCode int
	Code       int    `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Body       string `json:"-"`
}

func (e *APIError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return fmt.Sprintf("labnana returned %d: %s", e.StatusCode, e.Message)
	}
	if strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("labnana returned %d: %s", e.StatusCode, e.Body)
	}
	return fmt.Sprintf("labnana returned %d", e.StatusCode)
}

func New(cfg config.Config, httpClient *http.Client, logger *logging.Logger) *Client {
	return &Client{
		baseURL:    strings.TrimRight(cfg.Labnana.BaseURL, "/"),
		httpClient: httpClient,
		logger:     logger,
	}
}

func (c *Client) Generate(ctx context.Context, apiKey string, req GenerateRequest) ([]GeneratedImage, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal labnana request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/openapi/v1/images/generation", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		_ = json.Unmarshal(body, apiErr)
		return nil, apiErr
	}

	var upstream labnanaResponse
	if err := json.Unmarshal(body, &upstream); err != nil {
		return nil, fmt.Errorf("parse labnana response: %w", err)
	}

	images := make([]GeneratedImage, 0, 1)
	for _, candidate := range upstream.Candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.InlineData.Data) == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil {
				return nil, fmt.Errorf("decode image data: %w", err)
			}
			images = append(images, GeneratedImage{
				MimeType: strings.TrimSpace(part.InlineData.MimeType),
				Data:     data,
			})
		}
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("labnana returned no image data")
	}
	return images, nil
}

func (c *Client) CheckKey(ctx context.Context, apiKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/openapi/v1/user/subscription", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= http.StatusBadRequest {
		apiErr := &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
		_ = json.Unmarshal(body, apiErr)
		return apiErr
	}
	return nil
}

type labnanaResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				InlineData struct {
					MimeType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
