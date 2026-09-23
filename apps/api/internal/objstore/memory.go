package objstore

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type memoryObject struct {
	contentType string
	body        []byte
}

// Memory is an in-process Store for tests. Its pre-signed URLs are placeholders no client can use.
type Memory struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{objects: map[string]memoryObject{}}
}

// Keys lists the stored keys in order, for test assertions.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for key := range m.objects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

const memoryURLPrefix = "memory://bucket/"

// KeyOf returns the object key a pre-signed URL from this store points at.
func (m *Memory) KeyOf(req PresignedRequest) string {
	escaped, _, _ := strings.Cut(strings.TrimPrefix(req.URL, memoryURLPrefix), "?")
	key, err := url.PathUnescape(escaped)
	if err != nil {
		return ""
	}
	return key
}

// Upload plays the browser's PUT against a pre-signed request, refusing a body or content type that
// differs from the signature the way S3 does.
func (m *Memory) Upload(req PresignedRequest, contentType string, body []byte) error {
	if contentType != req.Headers["Content-Type"] || strconv.Itoa(len(body)) != req.Headers["Content-Length"] {
		return fmt.Errorf("objstore: upload does not match the signed content type and length")
	}
	return m.Put(context.Background(), m.KeyOf(req), contentType, body)
}

func (m *Memory) PresignPut(_ context.Context, key, contentType string, size int64, ttl time.Duration) (PresignedRequest, error) {
	return PresignedRequest{
		Method: "PUT",
		URL:    memoryURLPrefix + url.PathEscape(key),
		Headers: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": strconv.FormatInt(size, 10),
		},
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

func (m *Memory) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	expires := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return memoryURLPrefix + url.PathEscape(key) + "?expires=" + expires, nil
}

func (m *Memory) Head(_ context.Context, key string) (ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return ObjectInfo{}, ErrNotFound
	}
	return ObjectInfo{Size: int64(len(obj.body)), ContentType: obj.contentType}, nil
}

func (m *Memory) Get(_ context.Context, key string, maxBytes int64) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	if int64(len(obj.body)) > maxBytes {
		return nil, ErrTooLarge
	}
	return slices.Clone(obj.body), nil
}

func (m *Memory) Put(_ context.Context, key, contentType string, body []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{contentType: contentType, body: slices.Clone(body)}
	return nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}
