package aiservice

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Cache failures are always misses. No credential or source text is persisted.
const cacheEntryLimit = 4 << 20
const cacheTotalLimit = 32 << 20
const cacheCountLimit = 256

var diskMu sync.Mutex

type diskEntry struct {
	Version  int    `json:"version"`
	Identity string `json:"identity"`
	Value    string `json:"value"`
	Checksum string `json:"checksum"`
}

func defaultCacheDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "GoRss", "ai-cache")
}
func atomicCacheWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cache-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func safeCacheRead(path string, max int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > max {
		return nil, ErrProtocol
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}
func cacheSalt(dir string) []byte {
	if dir == "" {
		return nil
	}
	diskMu.Lock()
	defer diskMu.Unlock()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil
	}
	path := filepath.Join(dir, "salt")
	if b, err := safeCacheRead(path, 32); err == nil && len(b) == 32 {
		return b
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil
	}
	if atomicCacheWrite(path, b) != nil {
		return nil
	}
	return b
}
func (s *Service) cacheIdentity(x session, kind, content, system string, max int) [32]byte {
	mac := hmac.New(sha256.New, s.salt)
	mac.Write([]byte(x.key))
	data, _ := json.Marshal([]any{PromptVersion, x.provider, x.config.BaseURL, x.config.Model, x.config.Language, kind, system, max, sha256.Sum256([]byte(content)), mac.Sum(nil), x.credential})
	return sha256.Sum256(data)
}
func checksum(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (s *Service) diskGet(key [32]byte) (string, bool) {
	if len(s.salt) == 0 {
		return "", false
	}
	diskMu.Lock()
	defer diskMu.Unlock()
	id := hex.EncodeToString(key[:])
	path := filepath.Join(s.cacheDir, id+".json")
	b, err := safeCacheRead(path, cacheEntryLimit)
	if err != nil {
		return "", false
	}
	var v diskEntry
	if json.Unmarshal(b, &v) != nil || v.Version != 1 || v.Identity != id || v.Checksum != checksum(v.Value) {
		return "", false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return v.Value, true
}
func (s *Service) diskPut(key [32]byte, value string) {
	if len(s.salt) == 0 {
		return
	}
	diskMu.Lock()
	defer diskMu.Unlock()
	id := hex.EncodeToString(key[:])
	b, err := json.Marshal(diskEntry{1, id, value, checksum(value)})
	if err != nil || len(b) > cacheEntryLimit {
		return
	}
	if atomicCacheWrite(filepath.Join(s.cacheDir, id+".json"), b) != nil {
		return
	}
	files, _ := os.ReadDir(s.cacheDir)
	type candidate struct {
		name     string
		size     int64
		modified time.Time
	}
	var all []candidate
	var size int64
	for _, f := range files {
		if filepath.Ext(f.Name()) != ".json" {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		all = append(all, candidate{f.Name(), info.Size(), info.ModTime()})
		size += info.Size()
	}
	sort.Slice(all, func(i, j int) bool { return all[i].modified.Before(all[j].modified) })
	for len(all) > cacheCountLimit || size > cacheTotalLimit {
		old := all[0]
		all = all[1:]
		size -= old.size
		_ = os.Remove(filepath.Join(s.cacheDir, old.name))
	}
}

// NewWithCacheDir permits isolated caches in tests. An empty path disables disk caching.
func NewWithCacheDir(c Config, store SecretStore, dir string) (*Service, error) {
	s, err := newService(c, store)
	if err != nil {
		return nil, err
	}
	s.cacheDir = dir
	s.salt = cacheSalt(dir)
	return s, nil
}

// ForceRefresh bypasses derived caches for an explicit regeneration.
func (s *Service) ForceRefresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forceRefresh = true
	clear(s.cache)
}

// A random epoch changes whenever an observed credential changes, including
// switching back to a previously used key. Only a salted MAC is stored.
func (s *Service) credentialEpoch(p Provider, key string) string {
	if len(s.salt) == 0 {
		return ""
	}
	diskMu.Lock()
	defer diskMu.Unlock()
	mac := hmac.New(sha256.New, s.salt)
	mac.Write([]byte(key))
	digest := hex.EncodeToString(mac.Sum(nil))
	path := filepath.Join(s.cacheDir, string(p)+".state")
	var state struct {
		MAC   string `json:"mac"`
		Epoch string `json:"epoch"`
	}
	if b, err := safeCacheRead(path, 1024); err == nil && json.Unmarshal(b, &state) == nil && state.MAC == digest && len(state.Epoch) == 64 {
		return state.Epoch
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return ""
	}
	state.MAC = digest
	state.Epoch = hex.EncodeToString(random)
	b, _ := json.Marshal(state)
	if atomicCacheWrite(path, b) != nil {
		return ""
	}
	return state.Epoch
}

func (s *Service) invalidateDiskCredentials() {
	if s.cacheDir == "" {
		return
	}
	diskMu.Lock()
	defer diskMu.Unlock()
	for _, p := range []Provider{Custom, Magpie} {
		_ = os.Remove(filepath.Join(s.cacheDir, string(p)+".state"))
	}
}
