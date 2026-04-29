package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	neturl "net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"labnana2api/internal/config"
	"labnana2api/internal/gallery"
	"labnana2api/internal/httpclient"
	"labnana2api/internal/labnana"
	"labnana2api/internal/logging"
	"labnana2api/internal/openai"
	"labnana2api/internal/storage"
	"labnana2api/internal/web"
)

type Server struct {
	configStore  *config.Store
	galleryStore *gallery.Store

	loggerMu sync.RWMutex
	logger   *logging.Logger

	clientMu sync.RWMutex
	client   *http.Client

	storageMu sync.RWMutex
	storage   *storage.Client

	labnanaMu sync.RWMutex
	labClient *labnana.Client

	statsMu  sync.RWMutex
	keyStats map[string]keyRuntimeStats

	keyCursor uint64
	startTime time.Time
}

type keyRuntimeStats struct {
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
	SuccessCount int64     `json:"success_count,omitempty"`
	FailureCount int64     `json:"failure_count,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type keyView struct {
	Name         string    `json:"name"`
	MaskedKey    string    `json:"masked_key"`
	Enabled      bool      `json:"enabled"`
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
	SuccessCount int64     `json:"success_count,omitempty"`
	FailureCount int64     `json:"failure_count,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

func New(configStore *config.Store, galleryStore *gallery.Store) (*Server, error) {
	s := &Server{
		configStore:  configStore,
		galleryStore: galleryStore,
		keyStats:     make(map[string]keyRuntimeStats),
		startTime:    time.Now(),
	}
	if err := s.reloadRuntime(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) Run() error {
	s.configStore.Watch(s.reloadRuntime)

	mux := http.NewServeMux()
	mux.HandleFunc("/", web.HandleIndex)
	mux.HandleFunc("/api/telemetry", s.loggingMiddleware(s.handleTelemetry))
	mux.HandleFunc("/api/config", s.loggingMiddleware(s.handleConfig))
	mux.HandleFunc("/api/keys", s.loggingMiddleware(s.handleKeys))
	mux.HandleFunc("/api/keys/", s.loggingMiddleware(s.handleKeys))
	mux.HandleFunc("/api/gallery", s.loggingMiddleware(s.handleGallery))
	mux.HandleFunc("/media/", s.loggingMiddleware(s.handleMedia))
	mux.HandleFunc("/v1/models", s.loggingMiddleware(s.handleModels))
	mux.HandleFunc("/v1/images/generations", s.loggingMiddleware(s.handleImageGenerations))
	mux.HandleFunc("/v1/images/edits", s.loggingMiddleware(s.handleImageEdits))
	mux.HandleFunc("/v1/chat/completions", s.loggingMiddleware(s.handleChatCompletions))

	cfg := s.ConfigSnapshot()
	addr := fmt.Sprintf(":%d", cfg.Port)
	s.Logger().Info("labnana2api listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *Server) ConfigSnapshot() config.Config {
	return s.configStore.Snapshot()
}

func (s *Server) Logger() *logging.Logger {
	s.loggerMu.RLock()
	defer s.loggerMu.RUnlock()
	return s.logger
}

func (s *Server) HTTPClient() *http.Client {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.client
}

func (s *Server) ObjectStorage() *storage.Client {
	s.storageMu.RLock()
	defer s.storageMu.RUnlock()
	return s.storage
}

func (s *Server) LabClient() *labnana.Client {
	s.labnanaMu.RLock()
	defer s.labnanaMu.RUnlock()
	return s.labClient
}

func (s *Server) reloadRuntime() error {
	if err := s.configStore.Reload(); err != nil {
		return err
	}
	cfg := s.ConfigSnapshot()

	logger, err := logging.NewFromConfig(cfg.LogLevel, cfg.LogFile)
	if err != nil {
		return err
	}
	client := httpclient.New(cfg, logger)

	var objectStorage *storage.Client
	if cfg.ObjectStorage != nil && cfg.ObjectStorage.Enabled {
		objectStorage, err = storage.New(*cfg.ObjectStorage, logger)
		if err != nil {
			logger.Warn("object storage init failed, fallback to local media only: %v", err)
		}
	}

	labClient := labnana.New(cfg, client, logger)

	s.loggerMu.Lock()
	s.logger = logger
	s.loggerMu.Unlock()

	s.clientMu.Lock()
	s.client = client
	s.clientMu.Unlock()

	s.storageMu.Lock()
	s.storage = objectStorage
	s.storageMu.Unlock()

	s.labnanaMu.Lock()
	s.labClient = labClient
	s.labnanaMu.Unlock()

	return nil
}

func (s *Server) loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next(w, r)
		s.Logger().Info("%s %s completed in %.2fms", r.Method, r.URL.Path, float64(time.Since(start).Microseconds())/1000)
	}
}

func (s *Server) handleTelemetry(w http.ResponseWriter, _ *http.Request) {
	items := s.galleryStore.List(0)
	cfg := s.ConfigSnapshot()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":          "running",
		"uptime_seconds":  int64(time.Since(s.startTime).Seconds()),
		"keys_configured": len(cfg.LabnanaKeys),
		"keys_enabled":    len(cfg.ActiveLabnanaKeys()),
		"gallery_items":   len(items),
		"object_storage": map[string]interface{}{
			"enabled": cfg.ObjectStorage != nil && cfg.ObjectStorage.Enabled,
			"bucket":  bucketName(cfg),
		},
		"labnana": map[string]interface{}{
			"base_url":      cfg.Labnana.BaseURL,
			"default_model": cfg.Labnana.DefaultModel,
			"image_size":    cfg.Labnana.DefaultImageSize,
		},
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	cfg := s.ConfigSnapshot()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_key":         maskSecret(cfg.APIKey),
		"public_base_url": cfg.PublicBaseURL,
		"proxy":           cfg.Proxy,
		"no_proxy":        cfg.NoProxy,
		"port":            cfg.Port,
		"log_file":        cfg.LogFile,
		"log_level":       cfg.LogLevel,
		"object_storage": map[string]interface{}{
			"enabled":            cfg.ObjectStorage != nil && cfg.ObjectStorage.Enabled,
			"provider":           cfg.ObjectStorage.Provider,
			"endpoint":           cfg.ObjectStorage.Endpoint,
			"bucket":             cfg.ObjectStorage.Bucket,
			"region":             cfg.ObjectStorage.Region,
			"use_ssl":            cfg.ObjectStorage.UseSSL,
			"public_base_url":    cfg.ObjectStorage.PublicBaseURL,
			"key_prefix":         cfg.ObjectStorage.KeyPrefix,
			"auto_create_bucket": cfg.ObjectStorage.AutoCreateBucket,
			"access_key_id":      maskSecret(cfg.ObjectStorage.AccessKeyID),
			"secret_access_key":  maskSecret(cfg.ObjectStorage.SecretAccessKey),
		},
		"labnana": cfg.Labnana,
	})
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	name, action, hasName, err := parseKeyRoute(r.URL.Path)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		if hasName {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"keys": s.keyViews()})
	case http.MethodPost:
		if hasName && action == "check" {
			if err := s.checkKey(name); err != nil {
				s.writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "key is valid"})
			return
		}
		if hasName {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Name    string `json:"name"`
			Key     string `json:"key"`
			Enabled *bool  `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.configStore.Update(func(cfg *config.Config) error {
			name := strings.TrimSpace(req.Name)
			key := strings.TrimSpace(req.Key)
			if name == "" || key == "" {
				return errors.New("name and key are required")
			}
			for _, item := range cfg.LabnanaKeys {
				if item.Name == name {
					return fmt.Errorf("key %s already exists", name)
				}
			}
			cfg.LabnanaKeys = append(cfg.LabnanaKeys, config.LabnanaKey{
				Name:    name,
				Key:     key,
				Enabled: req.Enabled,
			})
			return nil
		}); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "keys": s.keyViews()})
	case http.MethodPatch:
		if !hasName || action != "" {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Enabled *bool   `json:"enabled"`
			Key     *string `json:"key"`
			Name    *string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.configStore.Update(func(cfg *config.Config) error {
			for i := range cfg.LabnanaKeys {
				if cfg.LabnanaKeys[i].Name != name {
					continue
				}
				if req.Enabled != nil {
					cfg.LabnanaKeys[i].Enabled = req.Enabled
				}
				if req.Key != nil {
					cfg.LabnanaKeys[i].Key = strings.TrimSpace(*req.Key)
				}
				if req.Name != nil {
					nextName := strings.TrimSpace(*req.Name)
					if nextName == "" {
						return errors.New("name cannot be empty")
					}
					cfg.LabnanaKeys[i].Name = nextName
				}
				return nil
			}
			return fmt.Errorf("key %s not found", name)
		}); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "keys": s.keyViews()})
	case http.MethodDelete:
		if !hasName || action != "" {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := s.configStore.Update(func(cfg *config.Config) error {
			next := make([]config.LabnanaKey, 0, len(cfg.LabnanaKeys))
			found := false
			for _, item := range cfg.LabnanaKeys {
				if item.Name == name {
					found = true
					continue
				}
				next = append(next, item)
			}
			if !found {
				return fmt.Errorf("key %s not found", name)
			}
			cfg.LabnanaKeys = next
			return nil
		}); err != nil {
			s.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.statsMu.Lock()
		delete(s.keyStats, name)
		s.statsMu.Unlock()
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "keys": s.keyViews()})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"items": s.galleryStore.List(limit),
	})
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	name := filepath.Base(strings.TrimPrefix(r.URL.Path, "/media/"))
	if name == "" || name == "." || name == "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.galleryStore.MediaDir(), name))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	now := time.Now().Unix()
	s.writeJSON(w, http.StatusOK, openai.ModelsResponse{
		Object: "list",
		Data: []openai.Model{
			{ID: "gpt-image-2", Object: "model", Created: now, OwnedBy: "labnana"},
		},
	})
}

func (s *Server) handleImageGenerations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	req, refs, err := s.parseImageRequest(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := s.generateOpenAIImages(r.Context(), r, req, refs)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleImageEdits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	req, refs, err := s.parseImageRequest(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(refs) == 0 && strings.TrimSpace(req.Image) == "" && strings.TrimSpace(req.ImageURL) == "" {
		s.writeError(w, http.StatusBadRequest, "image edit requires at least one reference image")
		return
	}
	resp, err := s.generateOpenAIImages(r.Context(), r, req, refs)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		s.writeError(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	var req openai.ChatCompletionRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "read request body failed")
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !supportsModel(req.Model) {
		s.writeError(w, http.StatusBadRequest, "only gpt-image-2 is supported")
		return
	}

	prompt, refs := extractChatPromptAndRefs(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		s.writeError(w, http.StatusBadRequest, "missing prompt")
		return
	}

	imageReq := openai.ImageGenerationRequest{
		Model:          req.Model,
		Prompt:         prompt,
		ResponseFormat: "url",
		User:           req.User,
		N:              1,
	}
	resp, err := s.generateOpenAIImages(r.Context(), r, imageReq, refs)
	if err != nil {
		s.writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	markdown := markdownFromImages(resp.Data)
	if req.Stream {
		s.writeChatStream(w, req.Model, markdown)
		return
	}
	finishReason := "stop"
	s.writeJSON(w, http.StatusOK, openai.ChatCompletionResponse{
		ID:      "chatcmpl-" + randomID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []openai.Choice{{
			Index: 0,
			Message: &openai.Message{
				Role:    "assistant",
				Content: markdown,
			},
			FinishReason: &finishReason,
		}},
	})
}

func (s *Server) generateOpenAIImages(ctx context.Context, r *http.Request, req openai.ImageGenerationRequest, initialRefs []referenceInput) (openai.ImageGenerationResponse, error) {
	if !supportsModel(req.Model) {
		req.Model = s.ConfigSnapshot().Labnana.DefaultModel
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = strings.TrimSpace(req.Input)
	}
	if prompt == "" {
		return openai.ImageGenerationResponse{}, errors.New("missing prompt")
	}

	references := append([]referenceInput(nil), initialRefs...)
	if strings.TrimSpace(req.Image) != "" {
		references = append(references, referenceInput{Value: strings.TrimSpace(req.Image)})
	}
	if strings.TrimSpace(req.ImageURL) != "" {
		references = append(references, referenceInput{Value: strings.TrimSpace(req.ImageURL)})
	}
	if len(references) > 4 {
		references = references[:4]
	}

	referenceImages, err := s.prepareReferenceImages(r, references)
	if err != nil {
		return openai.ImageGenerationResponse{}, err
	}

	count := req.N
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		count = 4
	}

	responseFormat := normalizeResponseFormat(req.ResponseFormat)
	aspectRatio := resolveAspectRatio(req.Size, req.AspectRatio, s.ConfigSnapshot().Labnana.DefaultAspectRatio)

	resp := openai.ImageGenerationResponse{
		Created: time.Now().Unix(),
		Data:    make([]openai.ImageGenerationData, 0, count),
	}
	for i := 0; i < count; i++ {
		upstreamReq := labnana.GenerateRequest{
			Provider:        s.ConfigSnapshot().Labnana.DefaultProvider,
			Model:           s.ConfigSnapshot().Labnana.DefaultModel,
			Prompt:          prompt,
			ReferenceImages: referenceImages,
			ImageConfig: labnana.ImageConfig{
				ImageSize:   s.ConfigSnapshot().Labnana.DefaultImageSize,
				AspectRatio: aspectRatio,
			},
		}
		images, keyName, err := s.generateWithKeys(ctx, upstreamReq)
		if err != nil {
			return openai.ImageGenerationResponse{}, err
		}
		for _, image := range images {
			item, err := s.persistGeneratedImage(r, req, prompt, aspectRatio, keyName, image, len(referenceImages), responseFormat)
			if err != nil {
				return openai.ImageGenerationResponse{}, err
			}
			resp.Data = append(resp.Data, item)
			if len(resp.Data) >= count {
				break
			}
		}
		if len(resp.Data) >= count {
			break
		}
	}
	return resp, nil
}

func (s *Server) generateWithKeys(ctx context.Context, req labnana.GenerateRequest) ([]labnana.GeneratedImage, string, error) {
	cfg := s.ConfigSnapshot()
	keys := cfg.ActiveLabnanaKeys()
	if len(keys) == 0 {
		return nil, "", errors.New("no active labnana keys configured")
	}

	start := int(atomic.AddUint64(&s.keyCursor, 1)-1) % len(keys)
	var lastErr error
	for i := 0; i < len(keys); i++ {
		key := keys[(start+i)%len(keys)]
		images, err := s.LabClient().Generate(ctx, key.Key, req)
		if err != nil {
			s.recordKeyFailure(key.Name, err)
			lastErr = err
			continue
		}
		s.recordKeySuccess(key.Name)
		return images, key.Name, nil
	}
	if lastErr == nil {
		lastErr = errors.New("all labnana keys failed")
	}
	return nil, "", lastErr
}

type referenceInput struct {
	Value    string
	Data     []byte
	MimeType string
	Name     string
}

func (s *Server) parseImageRequest(r *http.Request) (openai.ImageGenerationRequest, []referenceInput, error) {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return s.parseMultipartImageRequest(r)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return openai.ImageGenerationRequest{}, nil, err
	}
	var req openai.ImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return openai.ImageGenerationRequest{}, nil, err
	}
	return req, nil, nil
}

func (s *Server) parseMultipartImageRequest(r *http.Request) (openai.ImageGenerationRequest, []referenceInput, error) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		return openai.ImageGenerationRequest{}, nil, err
	}
	req := openai.ImageGenerationRequest{
		Model:          strings.TrimSpace(r.FormValue("model")),
		Prompt:         strings.TrimSpace(r.FormValue("prompt")),
		Input:          strings.TrimSpace(r.FormValue("input")),
		Size:           strings.TrimSpace(r.FormValue("size")),
		ResponseFormat: strings.TrimSpace(r.FormValue("response_format")),
		User:           strings.TrimSpace(r.FormValue("user")),
		AspectRatio:    strings.TrimSpace(r.FormValue("aspect_ratio")),
		Image:          strings.TrimSpace(r.FormValue("image")),
		ImageURL:       strings.TrimSpace(r.FormValue("image_url")),
	}
	if n, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("n"))); n > 0 {
		req.N = n
	}

	refs := make([]referenceInput, 0, 4)
	for _, field := range []string{"image", "image[]"} {
		files := r.MultipartForm.File[field]
		for _, fh := range files {
			ref, err := readMultipartFile(fh)
			if err != nil {
				return openai.ImageGenerationRequest{}, nil, err
			}
			refs = append(refs, ref)
		}
	}
	return req, refs, nil
}

func readMultipartFile(fh *multipart.FileHeader) (referenceInput, error) {
	file, err := fh.Open()
	if err != nil {
		return referenceInput{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return referenceInput{}, err
	}
	mimeType := fh.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	return referenceInput{
		Data:     data,
		MimeType: mimeType,
		Name:     fh.Filename,
	}, nil
}

func (s *Server) prepareReferenceImages(r *http.Request, refs []referenceInput) ([]labnana.ReferenceImage, error) {
	result := make([]labnana.ReferenceImage, 0, len(refs))
	for _, ref := range refs {
		if len(result) >= 4 {
			break
		}
		url, mimeType, err := s.referenceURL(r, ref)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(url) == "" {
			continue
		}
		result = append(result, labnana.ReferenceImage{
			FileData: labnana.FileData{
				FileURI:  url,
				MimeType: mimeType,
			},
		})
	}
	return result, nil
}

func (s *Server) referenceURL(r *http.Request, ref referenceInput) (string, string, error) {
	if len(ref.Data) > 0 {
		mimeType := normalizeMimeType(ref.MimeType, ref.Data)
		url, _, _, err := s.storeMedia(r, ref.Data, mimeType, ref.Name)
		return url, mimeType, err
	}

	value := strings.TrimSpace(ref.Value)
	if value == "" {
		return "", "", nil
	}
	if strings.HasPrefix(value, "data:") {
		mimeType, data, err := decodeDataURL(value)
		if err != nil {
			return "", "", err
		}
		url, _, _, err := s.storeMedia(r, data, mimeType, "reference")
		return url, mimeType, err
	}

	parsed, err := neturl.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("unsupported reference image input")
	}
	return value, mimeTypeFromURL(parsed.Path), nil
}

func (s *Server) persistGeneratedImage(r *http.Request, req openai.ImageGenerationRequest, prompt, aspectRatio, keyName string, image labnana.GeneratedImage, referenceCount int, responseFormat string) (openai.ImageGenerationData, error) {
	if len(image.Data) == 0 {
		return openai.ImageGenerationData{}, errors.New("empty image data")
	}
	mimeType := normalizeMimeType(image.MimeType, image.Data)
	url, localName, objectKey, err := s.storeMedia(r, image.Data, mimeType, req.Model)
	if err != nil {
		return openai.ImageGenerationData{}, err
	}

	if err := s.galleryStore.Add(gallery.Record{
		ID:             strings.TrimSuffix(localName, filepath.Ext(localName)),
		CreatedAt:      time.Now(),
		Prompt:         prompt,
		Model:          fallbackModel(req.Model, s.ConfigSnapshot().Labnana.DefaultModel),
		User:           req.User,
		Size:           s.ConfigSnapshot().Labnana.DefaultImageSize,
		AspectRatio:    aspectRatio,
		MimeType:       mimeType,
		URL:            url,
		LocalFilename:  localName,
		ObjectKey:      objectKey,
		ReferenceCount: referenceCount,
		KeyName:        keyName,
		Source:         "labnana",
	}); err != nil {
		return openai.ImageGenerationData{}, err
	}

	item := openai.ImageGenerationData{RevisedPrompt: prompt}
	if responseFormat == "b64_json" {
		item.B64JSON = base64.StdEncoding.EncodeToString(image.Data)
	} else {
		item.URL = url
	}
	return item, nil
}

func (s *Server) storeMedia(r *http.Request, data []byte, mimeType, sourceName string) (string, string, string, error) {
	ext := extensionFromMime(mimeType)
	if ext == "" {
		ext = ".bin"
	}
	name := randomID() + ext
	if _, err := s.galleryStore.SaveMedia(name, data); err != nil {
		return "", "", "", err
	}

	if objectStorage := s.ObjectStorage(); objectStorage != nil {
		url, objectKey, err := objectStorage.UploadBytes(data, mimeType, sourceName)
		if err == nil {
			return url, name, objectKey, nil
		}
		s.Logger().Warn("object storage upload failed, fallback to local media: %v", err)
	}
	return s.localMediaURL(r, name), name, "", nil
}

func (s *Server) localMediaURL(r *http.Request, name string) string {
	base := strings.TrimRight(strings.TrimSpace(s.ConfigSnapshot().PublicBaseURL), "/")
	if base != "" {
		return base + "/media/" + neturl.PathEscape(name)
	}
	scheme := "http"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/media/%s", scheme, r.Host, neturl.PathEscape(name))
}

func (s *Server) keyViews() []keyView {
	cfg := s.ConfigSnapshot()
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()

	views := make([]keyView, 0, len(cfg.LabnanaKeys))
	for _, item := range cfg.LabnanaKeys {
		stats := s.keyStats[item.Name]
		views = append(views, keyView{
			Name:         item.Name,
			MaskedKey:    maskSecret(item.Key),
			Enabled:      item.IsEnabled(),
			LastUsedAt:   stats.LastUsedAt,
			SuccessCount: stats.SuccessCount,
			FailureCount: stats.FailureCount,
			LastError:    stats.LastError,
		})
	}
	return views
}

func (s *Server) checkKey(name string) error {
	cfg := s.ConfigSnapshot()
	for _, item := range cfg.LabnanaKeys {
		if item.Name != name {
			continue
		}
		err := s.LabClient().CheckKey(context.Background(), item.Key)
		if err != nil {
			s.recordKeyFailure(name, err)
			return err
		}
		s.recordKeySuccess(name)
		return nil
	}
	return fmt.Errorf("key %s not found", name)
}

func (s *Server) recordKeySuccess(name string) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	stats := s.keyStats[name]
	stats.LastUsedAt = time.Now()
	stats.SuccessCount++
	stats.LastError = ""
	s.keyStats[name] = stats
}

func (s *Server) recordKeyFailure(name string, err error) {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	stats := s.keyStats[name]
	stats.LastUsedAt = time.Now()
	stats.FailureCount++
	stats.LastError = err.Error()
	s.keyStats[name] = stats
}

func (s *Server) writeChatStream(w http.ResponseWriter, model, markdown string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	chunk := map[string]interface{}{
		"id":      "chatcmpl-" + randomID(),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{{
			"index": 0,
			"delta": map[string]interface{}{
				"role":    "assistant",
				"content": markdown,
			},
			"finish_reason": nil,
		}},
	}
	data, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	finalChunk := map[string]interface{}{
		"id":      "chatcmpl-" + randomID(),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{{
			"index":         0,
			"delta":         map[string]interface{}{},
			"finish_reason": "stop",
		}},
	}
	finalData, _ := json.Marshal(finalChunk)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", finalData)
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func extractChatPromptAndRefs(messages []openai.Message) (string, []referenceInput) {
	var prompt string
	refs := make([]referenceInput, 0, 4)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch content := msg.Content.(type) {
		case string:
			if msg.Role == "user" && strings.TrimSpace(content) != "" && prompt == "" {
				prompt = strings.TrimSpace(content)
			}
		case []interface{}:
			if msg.Role != "user" {
				continue
			}
			textParts := make([]string, 0, 2)
			for _, raw := range content {
				part, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				switch part["type"] {
				case "text":
					if text, _ := part["text"].(string); strings.TrimSpace(text) != "" {
						textParts = append(textParts, strings.TrimSpace(text))
					}
				case "image_url":
					if imageURL, ok := part["image_url"].(map[string]interface{}); ok {
						if rawURL, _ := imageURL["url"].(string); strings.TrimSpace(rawURL) != "" {
							refs = append(refs, referenceInput{Value: strings.TrimSpace(rawURL)})
						}
					}
				}
			}
			if prompt == "" && len(textParts) > 0 {
				prompt = strings.Join(textParts, "\n")
			}
		}
		if prompt != "" {
			break
		}
	}
	if len(refs) == 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			msg := messages[i]
			content, ok := msg.Content.(string)
			if !ok || msg.Role != "assistant" {
				continue
			}
			for _, url := range extractMarkdownImageLinks(content) {
				refs = append(refs, referenceInput{Value: url})
			}
			if len(refs) > 0 {
				break
			}
		}
	}
	return prompt, refs
}

func extractMarkdownImageLinks(markdown string) []string {
	re := regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	matches := re.FindAllStringSubmatch(markdown, -1)
	links := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
			links = append(links, strings.TrimSpace(match[1]))
		}
	}
	return links
}

func markdownFromImages(items []openai.ImageGenerationData) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.URL) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("![image](%s)", item.URL))
	}
	return strings.Join(lines, "\n\n")
}

func parseKeyRoute(path string) (string, string, bool, error) {
	if path == "/api/keys" {
		return "", "", false, nil
	}
	if !strings.HasPrefix(path, "/api/keys/") {
		return "", "", false, fmt.Errorf("invalid key path")
	}
	raw := strings.Trim(strings.TrimPrefix(path, "/api/keys/"), "/")
	if raw == "" {
		return "", "", false, errors.New("missing key name")
	}
	parts := strings.Split(raw, "/")
	name, err := neturl.PathUnescape(parts[0])
	if err != nil {
		return "", "", false, err
	}
	action := ""
	if len(parts) > 1 {
		action = strings.TrimSpace(parts[1])
	}
	return name, action, true, nil
}

func supportsModel(model string) bool {
	switch strings.TrimSpace(strings.ToLower(model)) {
	case "", "gpt-image-2":
		return true
	default:
		return false
	}
}

func normalizeResponseFormat(format string) string {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "b64_json":
		return "b64_json"
	default:
		return "url"
	}
}

func resolveAspectRatio(size, explicitAspect, fallback string) string {
	if strings.TrimSpace(explicitAspect) != "" {
		return strings.TrimSpace(explicitAspect)
	}
	size = strings.TrimSpace(strings.ToLower(size))
	switch size {
	case "", "1024x1024":
		return fallbackOr(fallback, "1:1")
	case "1024x1536":
		return "2:3"
	case "1536x1024":
		return "3:2"
	case "1024x1792":
		return "9:16"
	case "1792x1024":
		return "16:9"
	default:
		return fallbackOr(fallback, "1:1")
	}
}

func normalizeMimeType(mimeType string, data []byte) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType != "" {
		if idx := strings.Index(mimeType, ";"); idx >= 0 {
			mimeType = mimeType[:idx]
		}
		return mimeType
	}
	return http.DetectContentType(data)
}

func decodeDataURL(value string) (string, []byte, error) {
	idx := strings.Index(value, ",")
	if idx < 0 {
		return "", nil, errors.New("invalid data url")
	}
	meta := value[:idx]
	dataPart := value[idx+1:]
	if !strings.HasSuffix(meta, ";base64") {
		return "", nil, errors.New("only base64 data urls are supported")
	}
	mimeType := strings.TrimPrefix(strings.TrimSuffix(meta, ";base64"), "data:")
	data, err := base64.StdEncoding.DecodeString(dataPart)
	if err != nil {
		return "", nil, err
	}
	return mimeType, data, nil
}

func extensionFromMime(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(mimeType)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/avif":
		return ".avif"
	case "image/svg+xml":
		return ".svg"
	default:
		return ""
	}
}

func mimeTypeFromURL(rawPath string) string {
	switch strings.ToLower(filepath.Ext(rawPath)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	default:
		return "image/png"
	}
}

func fallbackModel(model, fallback string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return fallback
	}
	return model
}

func fallbackOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func bucketName(cfg config.Config) string {
	if cfg.ObjectStorage == nil {
		return ""
	}
	return cfg.ObjectStorage.Bucket
}

func maskSecret(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func randomID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf[:])
}

func (s *Server) authorizeRequest(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	return token == s.ConfigSnapshot().APIKey
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	resp := openai.ErrorResponse{}
	resp.Error.Message = message
	resp.Error.Type = "invalid_request_error"
	s.writeJSON(w, status, resp)
}
