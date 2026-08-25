package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/goccy/go-json"
	"go.etcd.io/bbolt"
)

const bookmarkBucketName = "command_bookmarks"

// Bookmark represents a saved command with a label/tag
type Bookmark struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Label     string    `json:"label"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

// AddBookmark adds a new command bookmark
func (s *Storage) AddBookmark(ctx context.Context, command, label, notes string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command cannot be empty")
	}

	now := time.Now()
	id := fmt.Sprintf("%020d", now.UnixNano())

	bookmark := Bookmark{
		ID:        id,
		Command:   command,
		Label:     label,
		Notes:     notes,
		CreatedAt: now,
	}

	data, err := json.Marshal(bookmark)
	if err != nil {
		return fmt.Errorf("failed to marshal bookmark: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte(bookmarkBucketName))
		if err != nil {
			return err
		}
		return bucket.Put([]byte(id), data)
	})
}

// GetBookmarks retrieves all bookmarks
func (s *Storage) GetBookmarks(ctx context.Context) ([]Bookmark, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("storage not initialized")
	}

	var entries []Bookmark

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, bookmarkBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}

		c := bucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var entry Bookmark
			if err := json.Unmarshal(v, &entry); err == nil {
				entries = append(entries, entry)
			}
		}
		return nil
	})

	return entries, err
}

// SearchBookmarks searches bookmarks by label, command or notes
func (s *Storage) SearchBookmarks(ctx context.Context, query string) ([]Bookmark, error) {
	allEntries, err := s.GetBookmarks(ctx)
	if err != nil {
		return nil, err
	}

	if query == "" {
		return allEntries, nil
	}

	queryLower := strings.ToLower(query)
	var results []Bookmark

	for _, entry := range allEntries {
		if strings.Contains(strings.ToLower(entry.Command), queryLower) ||
			strings.Contains(strings.ToLower(entry.Label), queryLower) ||
			strings.Contains(strings.ToLower(entry.Notes), queryLower) {
			results = append(results, entry)
		}
	}

	return results, nil
}

// DeleteBookmark deletes a bookmark by its ID
func (s *Storage) DeleteBookmark(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("storage not initialized")
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket, err := requireBucket(tx, bookmarkBucketName)
		if err != nil {
			return err
		}
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(id))
	})
}
