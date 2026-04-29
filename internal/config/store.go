package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultPort                = 18082
	DefaultLogLevel            = "info"
	DefaultObjectStorageRegion = "cn-east-1"
	DefaultModel               = "gpt-image-2"
	DefaultProvider            = "openai"
	DefaultImageSize           = "2K"
)

type Config struct {
	APIKey        string               `json:"api_key"`
	PublicBaseURL string               `json:"public_base_url"`
	Proxy         string               `json:"proxy"`
	NoProxy       []string             `json:"no_proxy,omitempty"`
	Port          int                  `json:"port"`
	LogFile       string               `json:"log_file"`
	LogLevel      string               `json:"log_level"`
	ObjectStorage *ObjectStorageConfig `json:"object_storage,omitempty"`
	Labnana       LabnanaConfig        `json:"labnana"`
	LabnanaKeys   []LabnanaKey         `json:"labnana_keys"`
	Note          []string             `json:"note,omitempty"`
}

type LabnanaConfig struct {
	BaseURL            string `json:"base_url"`
	Timeout            int    `json:"timeout"`
	DefaultProvider    string `json:"default_provider"`
	DefaultModel       string `json:"default_model"`
	DefaultImageSize   string `json:"default_image_size"`
	DefaultAspectRatio string `json:"default_aspect_ratio"`
}

type LabnanaKey struct {
	Name         string    `json:"name"`
	Key          string    `json:"key"`
	Enabled      *bool     `json:"enabled,omitempty"`
	LastUsedAt   time.Time `json:"last_used_at,omitempty"`
	SuccessCount int64     `json:"success_count,omitempty"`
	FailureCount int64     `json:"failure_count,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type ObjectStorageConfig struct {
	Enabled          bool   `json:"enabled"`
	Provider         string `json:"provider"`
	Endpoint         string `json:"endpoint"`
	AccessKeyID      string `json:"access_key_id"`
	SecretAccessKey  string `json:"secret_access_key"`
	Bucket           string `json:"bucket"`
	Region           string `json:"region"`
	UseSSL           bool   `json:"use_ssl"`
	PublicBaseURL    string `json:"public_base_url"`
	KeyPrefix        string `json:"key_prefix"`
	AutoCreateBucket bool   `json:"auto_create_bucket"`
}

func (k LabnanaKey) IsEnabled() bool {
	return k.Enabled == nil || *k.Enabled
}

func (cfg *Config) Normalize() {
	if cfg.Port <= 0 {
		cfg.Port = DefaultPort
	}
	if strings.TrimSpace(cfg.LogLevel) == "" {
		cfg.LogLevel = DefaultLogLevel
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		cfg.APIKey = "labnana2api"
	}
	cfg.Proxy = strings.TrimSpace(cfg.Proxy)
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
	cfg.NoProxy = NormalizeNoProxyHosts(cfg.NoProxy)

	cfg.Labnana.Normalize()
	if cfg.ObjectStorage == nil {
		cfg.ObjectStorage = defaultObjectStorageConfig()
	}
	NormalizeObjectStorageConfig(cfg.ObjectStorage)

	for i := range cfg.LabnanaKeys {
		cfg.LabnanaKeys[i].Name = strings.TrimSpace(cfg.LabnanaKeys[i].Name)
		cfg.LabnanaKeys[i].Key = strings.TrimSpace(cfg.LabnanaKeys[i].Key)
		if cfg.LabnanaKeys[i].Enabled == nil {
			cfg.LabnanaKeys[i].Enabled = boolPtr(true)
		}
		if cfg.LabnanaKeys[i].Name == "" {
			cfg.LabnanaKeys[i].Name = fmt.Sprintf("key-%d", i+1)
		}
	}
}

func (cfg Config) ActiveLabnanaKeys() []LabnanaKey {
	active := make([]LabnanaKey, 0, len(cfg.LabnanaKeys))
	for _, key := range cfg.LabnanaKeys {
		if strings.TrimSpace(key.Key) == "" || !key.IsEnabled() {
			continue
		}
		active = append(active, key)
	}
	return active
}

func (cfg *LabnanaConfig) Normalize() {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "https://api.labnana.com"
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.Timeout <= 0 {
		cfg.Timeout = 300
	}
	if strings.TrimSpace(cfg.DefaultProvider) == "" {
		cfg.DefaultProvider = DefaultProvider
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		cfg.DefaultModel = DefaultModel
	}
	if strings.TrimSpace(cfg.DefaultImageSize) == "" {
		cfg.DefaultImageSize = DefaultImageSize
	}
	if strings.TrimSpace(cfg.DefaultAspectRatio) == "" {
		cfg.DefaultAspectRatio = "1:1"
	}
}

func NormalizeObjectStorageConfig(cfg *ObjectStorageConfig) {
	if cfg == nil {
		return
	}
	if strings.TrimSpace(cfg.Provider) == "" {
		cfg.Provider = "minio"
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = DefaultObjectStorageRegion
	}
	if strings.TrimSpace(cfg.KeyPrefix) == "" {
		cfg.KeyPrefix = "image/ai/labnana-images"
	}
	endpoint := strings.TrimSpace(cfg.Endpoint)
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		cfg.UseSSL = true
		endpoint = strings.TrimPrefix(endpoint, "https://")
	case strings.HasPrefix(endpoint, "http://"):
		cfg.UseSSL = false
		endpoint = strings.TrimPrefix(endpoint, "http://")
	}
	cfg.Endpoint = strings.TrimRight(endpoint, "/")
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/")
}

func NormalizeNoProxyHosts(hosts []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(hosts)+3)
	for _, raw := range append([]string{"127.0.0.1", "localhost", "::1"}, hosts...) {
		host := strings.ToLower(strings.TrimSpace(raw))
		host = strings.TrimPrefix(host, ".")
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		normalized = append(normalized, host)
	}
	return normalized
}

type Store struct {
	path string
	mu   sync.RWMutex
	cfg  Config
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		cfg := defaultConfig()
		cfg.Normalize()
		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal default config: %w", err)
		}
		if err := os.WriteFile(s.path, data, 0600); err != nil {
			return fmt.Errorf("write default config: %w", err)
		}
		s.cfg = cfg
		return nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	cfg.Normalize()
	s.cfg = cfg
	return nil
}

func (s *Store) Reload() error {
	return s.Load()
}

func (s *Store) Save(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(cfg)
}

func (s *Store) Update(mutator func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := s.cfg
	if err := mutator(&cfg); err != nil {
		return err
	}
	return s.saveLocked(cfg)
}

func (s *Store) Watch(onReload func() error) {
	go func() {
		var lastModTime time.Time
		for {
			time.Sleep(5 * time.Second)
			info, err := os.Stat(s.path)
			if err != nil {
				continue
			}
			modTime := info.ModTime()
			if !lastModTime.IsZero() && modTime.After(lastModTime) {
				_ = onReload()
			}
			lastModTime = modTime
		}
	}()
}

func (s *Store) saveLocked(cfg Config) error {
	cfg.Normalize()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.cfg = cfg
	return nil
}

func defaultConfig() Config {
	return Config{
		APIKey:        "labnana2api",
		PublicBaseURL: "",
		Proxy:         "http://127.0.0.1:19090",
		NoProxy:       []string{"127.0.0.1", "localhost", "::1"},
		Port:          DefaultPort,
		LogFile:       "logs/labnana2api.log",
		LogLevel:      DefaultLogLevel,
		ObjectStorage: defaultObjectStorageConfig(),
		Labnana: LabnanaConfig{
			BaseURL:            "https://api.labnana.com",
			Timeout:            300,
			DefaultProvider:    DefaultProvider,
			DefaultModel:       DefaultModel,
			DefaultImageSize:   DefaultImageSize,
			DefaultAspectRatio: "1:1",
		},
		LabnanaKeys: []LabnanaKey{},
		Note: []string{
			"Default config generated automatically.",
			"Update config.json with your runtime credentials before production use.",
		},
	}
}

func defaultObjectStorageConfig() *ObjectStorageConfig {
	return &ObjectStorageConfig{
		Enabled:          false,
		Provider:         "minio",
		Endpoint:         "",
		AccessKeyID:      "",
		SecretAccessKey:  "",
		Bucket:           "",
		Region:           "cn-east-1",
		UseSSL:           false,
		PublicBaseURL:    "",
		KeyPrefix:        "image/ai/labnana-images",
		AutoCreateBucket: false,
	}
}

func boolPtr(v bool) *bool {
	return &v
}
