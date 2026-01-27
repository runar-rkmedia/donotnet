// Package cache provides a bbolt-backed cache for storing test/build results.
package cache

import (
	"encoding/binary"
	"os"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const bucketName = "cache"

// DB wraps a bbolt database for caching test/build results.
type DB struct {
	db *bolt.DB
}

// Open opens or creates a cache database at the given path.
func Open(path string) (*DB, error) {
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, err
	}

	// Ensure bucket exists
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

// Close closes the cache database.
func (c *DB) Close() error {
	return c.db.Close()
}

// Path returns the path to the database file.
func (c *DB) Path() string {
	return c.db.Path()
}

// Entry represents a cache entry value.
type Entry struct {
	LastRun   int64  // Unix timestamp of last run
	CreatedAt int64  // Unix timestamp when first created
	Success   bool   // Whether the last run succeeded
	Output    []byte // Captured stdout from the run
	Args      string // The args used for this run (e.g., "test --no-build")
}

// Result contains the result of a cache lookup.
type Result struct {
	Time    time.Time
	Success bool
	Output  []byte
}

// MakeKey constructs the cache key from components.
// contentHash is a hash of all source files affecting the project.
func MakeKey(contentHash, argsHash, projectPath string) string {
	return contentHash + ":" + argsHash + ":" + projectPath
}

// ParseKey parses a cache key into its components.
func ParseKey(key string) (contentHash, argsHash, projectPath string) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// encodeEntry encodes a cache entry to bytes.
// Format: [LastRun:8][CreatedAt:8][OutputLen:4][Output:OutputLen][Success:1][ArgsLen:4][Args:ArgsLen]
func encodeEntry(e Entry) []byte {
	outputLen := len(e.Output)
	argsLen := len(e.Args)
	buf := make([]byte, 25+outputLen+argsLen)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(e.LastRun))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(e.CreatedAt))
	binary.LittleEndian.PutUint32(buf[16:20], uint32(outputLen))
	if outputLen > 0 {
		copy(buf[20:20+outputLen], e.Output)
	}
	pos := 20 + outputLen
	if e.Success {
		buf[pos] = 1
	} else {
		buf[pos] = 0
	}
	pos++
	binary.LittleEndian.PutUint32(buf[pos:pos+4], uint32(argsLen))
	if argsLen > 0 {
		copy(buf[pos+4:], e.Args)
	}
	return buf
}

// decodeEntry decodes a cache entry from bytes.
func decodeEntry(data []byte) Entry {
	if len(data) < 16 {
		return Entry{}
	}
	entry := Entry{
		LastRun:   int64(binary.LittleEndian.Uint64(data[0:8])),
		CreatedAt: int64(binary.LittleEndian.Uint64(data[8:16])),
		Success:   true, // Default for backward compatibility
	}
	// Handle old format (16 bytes) and new format (20+ bytes)
	if len(data) >= 20 {
		outputLen := binary.LittleEndian.Uint32(data[16:20])
		if len(data) >= 20+int(outputLen) {
			// Must copy - bbolt's buffer is only valid during transaction
			entry.Output = make([]byte, outputLen)
			copy(entry.Output, data[20:20+outputLen])
			pos := 20 + int(outputLen)
			// Check for success byte (new format)
			if len(data) >= pos+1 {
				entry.Success = data[pos] == 1
				pos++
				// Check for args (newest format)
				if len(data) >= pos+4 {
					argsLen := binary.LittleEndian.Uint32(data[pos : pos+4])
					if len(data) >= pos+4+int(argsLen) {
						entry.Args = string(data[pos+4 : pos+4+int(argsLen)])
					}
				}
			}
		}
	}
	return entry
}

// Lookup checks if a cache entry exists and returns the result.
// Only returns successful entries (for cache-hit purposes).
func (c *DB) Lookup(key string) *Result {
	var result *Result
	c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		entry := decodeEntry(data)
		// Only return successful entries for cache-hit purposes
		if !entry.Success {
			return nil
		}
		result = &Result{
			Time:    time.Unix(entry.LastRun, 0),
			Success: entry.Success,
			Output:  entry.Output,
		}
		return nil
	})
	return result
}

// LookupAny checks if a cache entry exists (success or failure) and returns the result.
func (c *DB) LookupAny(key string) *Result {
	var result *Result
	c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		data := b.Get([]byte(key))
		if data == nil {
			return nil
		}
		entry := decodeEntry(data)
		result = &Result{
			Time:    time.Unix(entry.LastRun, 0),
			Success: entry.Success,
			Output:  entry.Output,
		}
		return nil
	})
	return result
}

// Mark records a test/build result for the given key.
func (c *DB) Mark(key string, t time.Time, success bool, output []byte, args string) error {
	return c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}

		// Check if entry exists (to preserve CreatedAt)
		existing := b.Get([]byte(key))
		entry := Entry{
			LastRun:   t.Unix(),
			CreatedAt: t.Unix(),
			Success:   success,
			Output:    output,
			Args:      args,
		}
		if existing != nil {
			old := decodeEntry(existing)
			entry.CreatedAt = old.CreatedAt
		}

		return b.Put([]byte(key), encodeEntry(entry))
	})
}

// Stats contains cache statistics.
type Stats struct {
	TotalEntries int
	OldestEntry  time.Time
	NewestEntry  time.Time
	DBSize       int64
}

// GetStats returns cache statistics.
func (c *DB) GetStats() Stats {
	var stats Stats
	c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}

		cur := b.Cursor()
		for k, v := cur.First(); k != nil; k, v = cur.Next() {
			stats.TotalEntries++
			entry := decodeEntry(v)
			t := time.Unix(entry.LastRun, 0)
			if stats.OldestEntry.IsZero() || t.Before(stats.OldestEntry) {
				stats.OldestEntry = t
			}
			if stats.NewestEntry.IsZero() || t.After(stats.NewestEntry) {
				stats.NewestEntry = t
			}
		}
		return nil
	})

	// Get database file size
	if info, err := os.Stat(c.db.Path()); err == nil {
		stats.DBSize = info.Size()
	}

	return stats
}

// DeleteOldEntries removes cache entries older than maxAge.
func (c *DB) DeleteOldEntries(maxAge time.Duration) (deleted int, err error) {
	cutoff := time.Now().Add(-maxAge).Unix()

	err = c.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}

		var keysToDelete [][]byte
		cur := b.Cursor()
		for k, v := cur.First(); k != nil; k, v = cur.Next() {
			entry := decodeEntry(v)
			if entry.LastRun < cutoff {
				keysToDelete = append(keysToDelete, append([]byte{}, k...))
			}
		}

		for _, k := range keysToDelete {
			if err := b.Delete(k); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return
}

// FailedEntry contains info about a failed cache entry.
type FailedEntry struct {
	ProjectPath string
	Output      []byte
}

// GetFailed scans the cache for failed entries matching the given argsHash.
// Returns entries for projects that failed in their most recent run.
func (c *DB) GetFailed(argsHash string) []FailedEntry {
	// Track most recent entry per project (by timestamp)
	type projectEntry struct {
		entry     FailedEntry
		timestamp int64
		success   bool
	}
	mostRecent := make(map[string]projectEntry)

	c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		cur := b.Cursor()
		for k, v := cur.First(); k != nil; k, v = cur.Next() {
			// Parse key: contentHash:argsHash:projectPath
			_, keyArgsHash, projectPath := ParseKey(string(k))
			if keyArgsHash != argsHash || projectPath == "" {
				continue
			}
			entry := decodeEntry(v)
			prev, exists := mostRecent[projectPath]
			if !exists || entry.LastRun > prev.timestamp {
				mostRecent[projectPath] = projectEntry{
					entry: FailedEntry{
						ProjectPath: projectPath,
						Output:      entry.Output,
					},
					timestamp: entry.LastRun,
					success:   entry.Success,
				}
			}
		}
		return nil
	})

	// Return only projects whose most recent entry was a failure
	var failed []FailedEntry
	for _, pe := range mostRecent {
		if !pe.success {
			failed = append(failed, pe.entry)
		}
	}
	return failed
}

// View provides read-only access to iterate over cache entries.
// The callback receives each key-value pair.
func (c *DB) View(fn func(key string, entry Entry) error) error {
	return c.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName))
		if b == nil {
			return nil
		}
		cur := b.Cursor()
		for k, v := cur.First(); k != nil; k, v = cur.Next() {
			entry := decodeEntry(v)
			if err := fn(string(k), entry); err != nil {
				return err
			}
		}
		return nil
	})
}
