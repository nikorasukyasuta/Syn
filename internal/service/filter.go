package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"syn/internal/domain"
)

// WordFilterService manages server-wide and channel-specific word filter lists,
// in-memory caching, case-insensitive evaluation, and moderation warnings.
type WordFilterService struct {
	warningRepo  domain.WarningRepository
	staffRoleIDs []string
	logger       *slog.Logger

	mu           sync.RWMutex
	globalWords  []string
	channelWords map[string][]string // channel_id -> list of words
}

// NewWordFilterService initializes a new WordFilterService.
func NewWordFilterService(
	warningRepo domain.WarningRepository,
	staffRoleIDs []string,
	logger *slog.Logger,
) *WordFilterService {
	return &WordFilterService{
		warningRepo:  warningRepo,
		staffRoleIDs: staffRoleIDs,
		logger:       logger,
		channelWords: make(map[string][]string),
	}
}

// LoadAll scans the filters directory, loading global.json and any channel-specific filter files.
func (s *WordFilterService) LoadAll(dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dir == "" {
		dir = "./config/filters"
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		s.logger.Warn("could not create filters directory", "dir", dir, "error", err)
	}

	// 1. Load global filter
	globalPath := filepath.Join(dir, "global.json")
	if words, err := readFilterFile(globalPath); err == nil {
		s.globalWords = words
		s.logger.Info("loaded global word filter", "words_count", len(words), "file", globalPath)
	} else if errors.Is(err, os.ErrNotExist) {
		s.logger.Debug("no global.json filter file found, starting with empty global filter", "file", globalPath)
	} else {
		s.logger.Error("error reading global filter file", "file", globalPath, "error", err)
	}

	// 2. Discover channel-specific filter files (<channel_id>.json)
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.logger.Warn("could not read filters directory", "dir", dir, "error", err)
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "global.json" {
			continue
		}
		channelID := strings.TrimSuffix(entry.Name(), ".json")
		filePath := filepath.Join(dir, entry.Name())
		if words, err := readFilterFile(filePath); err == nil {
			s.channelWords[channelID] = words
			s.logger.Info("loaded channel word filter", "channel_id", channelID, "words_count", len(words), "file", filePath)
		} else {
			s.logger.Error("error reading channel filter file", "file", filePath, "error", err)
		}
	}

	return nil
}

// ReloadGlobal reloads global.json from disk into cache ONLY if the file is found.
// Returns (found, count, error). If not found, existing cache is untouched.
func (s *WordFilterService) ReloadGlobal(dir string) (bool, int, error) {
	if dir == "" {
		dir = "./config/filters"
	}
	globalPath := filepath.Join(dir, "global.json")
	if _, err := os.Stat(globalPath); os.IsNotExist(err) {
		s.logger.Warn("reload requested for global filter, but file not found; cache preserved", "path", globalPath)
		return false, 0, nil
	}

	words, err := readFilterFile(globalPath)
	if err != nil {
		return false, 0, fmt.Errorf("reading global filter file: %w", err)
	}

	s.mu.Lock()
	s.globalWords = words
	s.mu.Unlock()

	s.logger.Info("successfully reloaded global word filter", "words_count", len(words), "path", globalPath)
	return true, len(words), nil
}

// ReloadChannel reloads <channel_id>.json from disk into cache ONLY if the file is found.
// Returns (found, count, error). If not found, existing cache is untouched.
func (s *WordFilterService) ReloadChannel(dir string, channelID string) (bool, int, error) {
	if dir == "" {
		dir = "./config/filters"
	}
	channelPath := filepath.Join(dir, channelID+".json")
	// Also check fallback in channels/ subdirectory
	if _, err := os.Stat(channelPath); os.IsNotExist(err) {
		subPath := filepath.Join(dir, "channels", channelID+".json")
		if _, subErr := os.Stat(subPath); os.IsNotExist(subErr) {
			s.logger.Warn("reload requested for channel filter, but file not found; cache preserved",
				"channel_id", channelID,
				"path", channelPath,
			)
			return false, 0, nil
		}
		channelPath = subPath
	}

	words, err := readFilterFile(channelPath)
	if err != nil {
		return false, 0, fmt.Errorf("reading channel filter file: %w", err)
	}

	s.mu.Lock()
	s.channelWords[channelID] = words
	s.mu.Unlock()

	s.logger.Info("successfully reloaded channel word filter",
		"channel_id", channelID,
		"words_count", len(words),
		"path", channelPath,
	)
	return true, len(words), nil
}

// CheckMessage tests message text against global and channel-specific filters (case-insensitively).
// If a filtered word is matched, returns (true, match).
func (s *WordFilterService) CheckMessage(channelID, content string) (bool, *domain.FilterMatch) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Check server-wide global filter
	for _, word := range s.globalWords {
		if matchesFilter(trimmed, word) {
			return true, &domain.FilterMatch{
				Word:      word,
				Scope:     domain.FilterScopeGlobal,
				ChannelID: channelID,
			}
		}
	}

	// 2. Check channel-specific filter
	if words, exists := s.channelWords[channelID]; exists {
		for _, word := range words {
			if matchesFilter(trimmed, word) {
				return true, &domain.FilterMatch{
					Word:      word,
					Scope:     domain.FilterScopeChannel,
					ChannelID: channelID,
				}
			}
		}
	}

	return false, nil
}

// GetGlobalWords returns a copy of currently cached global words.
func (s *WordFilterService) GetGlobalWords() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]string, len(s.globalWords))
	copy(res, s.globalWords)
	return res
}

// GetChannelWords returns a copy of currently cached words for a specific channel.
func (s *WordFilterService) GetChannelWords(channelID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	words, ok := s.channelWords[channelID]
	if !ok {
		return nil
	}
	res := make([]string, len(words))
	copy(res, words)
	return res
}

// SetGlobalWords directly sets the global filter cache (useful for testing or direct sync).
func (s *WordFilterService) SetGlobalWords(words []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.globalWords = normalizeWordList(words)
}

// SetChannelWords directly sets a channel filter cache (useful for testing or direct sync).
func (s *WordFilterService) SetChannelWords(channelID string, words []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelWords[channelID] = normalizeWordList(words)
}

// RecordWarning persists a formal moderation warning against a user.
func (s *WordFilterService) RecordWarning(ctx context.Context, w *domain.UserWarning) error {
	if s.warningRepo == nil {
		return errors.New("warning repository not configured")
	}
	return s.warningRepo.SaveWarning(ctx, w)
}

// GetWarnings retrieves historical warnings recorded against a user.
func (s *WordFilterService) GetWarnings(ctx context.Context, guildID, userID string) ([]*domain.UserWarning, error) {
	if s.warningRepo == nil {
		return nil, errors.New("warning repository not configured")
	}
	return s.warningRepo.GetWarningsByUserID(ctx, guildID, userID)
}

// GetWarningCount returns the total count of warnings issued to a user.
func (s *WordFilterService) GetWarningCount(ctx context.Context, guildID, userID string) (int, error) {
	if s.warningRepo == nil {
		return 0, errors.New("warning repository not configured")
	}
	return s.warningRepo.GetWarningCountByUserID(ctx, guildID, userID)
}

// SaveViolation persists a pending filter violation for ticket review.
func (s *WordFilterService) SaveViolation(ctx context.Context, v *domain.FilterViolation) error {
	if s.warningRepo == nil {
		return errors.New("warning repository not configured")
	}
	return s.warningRepo.SaveViolation(ctx, v)
}

// GetViolation retrieves the filter violation associated with a ticket channel.
func (s *WordFilterService) GetViolation(ctx context.Context, guildID, channelID string) (*domain.FilterViolation, error) {
	if s.warningRepo == nil {
		return nil, errors.New("warning repository not configured")
	}
	return s.warningRepo.GetViolationByChannelID(ctx, guildID, channelID)
}

// IsStaff checks if the caller holds a configured staff or helper role.
func (s *WordFilterService) IsStaff(callerRoles []string) bool {
	if len(s.staffRoleIDs) == 0 {
		return false
	}
	staffMap := make(map[string]struct{}, len(s.staffRoleIDs))
	for _, id := range s.staffRoleIDs {
		staffMap[id] = struct{}{}
	}
	for _, r := range callerRoles {
		if _, ok := staffMap[r]; ok {
			return true
		}
	}
	return false
}

// readFilterFile parses a JSON file supporting either a raw string array ["word1", "word2"]
// or an object format {"words": ["word1", "word2"]}.
func readFilterFile(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// 1. Try parsing as []string
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		return normalizeWordList(list), nil
	}

	// 2. Try parsing as {"words": []string}
	var obj struct {
		Words []string `json:"words"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		return normalizeWordList(obj.Words), nil
	}

	return nil, fmt.Errorf("invalid json format in %s: expected string array or object with 'words' key", filePath)
}

func normalizeWordList(words []string) []string {
	var normalized []string
	seen := make(map[string]struct{})
	for _, w := range words {
		trimmed := strings.ToLower(strings.TrimSpace(w))
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; !exists {
			seen[trimmed] = struct{}{}
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

// matchesFilter performs case-insensitive word or phrase matching against text.
func matchesFilter(text, filterWord string) bool {
	lowerText := strings.ToLower(text)
	lowerWord := strings.ToLower(strings.TrimSpace(filterWord))
	if lowerWord == "" {
		return false
	}

	// If the filter entry is a multi-word phrase, use phrase substring matching
	if strings.Contains(lowerWord, " ") {
		return strings.Contains(lowerText, lowerWord)
	}

	// For single words, enforce word boundary matching to avoid false positives
	pattern := fmt.Sprintf(`(?i)(?:^|[^\p{L}\p{N}_])%s(?:$|[^\p{L}\p{N}_])`, regexp.QuoteMeta(lowerWord))
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Fallback to substring matching if regex compilation fails
		return strings.Contains(lowerText, lowerWord)
	}
	return re.MatchString(lowerText)
}

