package gallery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Record struct {
	ID             string    `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	Prompt         string    `json:"prompt"`
	Model          string    `json:"model"`
	User           string    `json:"user,omitempty"`
	Size           string    `json:"size,omitempty"`
	AspectRatio    string    `json:"aspect_ratio,omitempty"`
	MimeType       string    `json:"mime_type"`
	URL            string    `json:"url"`
	LocalFilename  string    `json:"local_filename"`
	ObjectKey      string    `json:"object_key,omitempty"`
	RevisedPrompt  string    `json:"revised_prompt,omitempty"`
	ReferenceCount int       `json:"reference_count,omitempty"`
	KeyName        string    `json:"key_name,omitempty"`
	Source         string    `json:"source,omitempty"`
}

type Store struct {
	path     string
	mediaDir string
	mu       sync.RWMutex
	records  []Record
}

func New(path, mediaDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(mediaDir, 0755); err != nil {
		return nil, err
	}

	s := &Store{path: path, mediaDir: mediaDir}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) MediaDir() string {
	return s.mediaDir
}

func (s *Store) List(limit int) []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := append([]Record(nil), s.records...)
	sort.Slice(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	if limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return records
}

func (s *Store) Add(record Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.records = append(s.records, record)
	return s.saveLocked()
}

func (s *Store) SaveMedia(filename string, data []byte) (string, error) {
	fullPath := filepath.Join(s.mediaDir, filename)
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}
	return fullPath, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.records = []Record{}
		return s.saveLocked()
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		s.records = []Record{}
		return nil
	}
	if err := json.Unmarshal(data, &s.records); err != nil {
		return fmt.Errorf("parse gallery: %w", err)
	}
	return nil
}

func (s *Store) saveLocked() error {
	data, err := json.MarshalIndent(s.records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}
