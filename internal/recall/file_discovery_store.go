package recall

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// FileDiscoveryStore implements DiscoveryStore with JSONL file persistence.
type FileDiscoveryStore struct {
	filePath string
	file     *os.File
	mutex    sync.RWMutex
	watchers []chan any
}

// NewFileDiscoveryStore creates a new FileDiscoveryStore.
func NewFileDiscoveryStore(filePath string) (*FileDiscoveryStore, error) {
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	return &FileDiscoveryStore{
		filePath: filePath,
		file:     file,
		watchers: make([]chan any, 0),
	}, nil
}

// Publish stores an event as JSON in the file.
func (f *FileDiscoveryStore) Publish(event any) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if f.file == nil {
		return errors.New("file discovery store is closed")
	}

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if _, err := f.file.WriteString(string(data) + "\n"); err != nil {
		return err
	}

	for _, watcher := range f.watchers {
		select {
		case watcher <- event:
		default:
		}
	}

	return nil
}

// Query reads events from the file and returns them in insertion order.
func (f *FileDiscoveryStore) Query(_ any) ([]any, error) {
	f.mutex.RLock()
	defer f.mutex.RUnlock()

	readFile, err := os.Open(f.filePath)
	if err != nil {
		return nil, err
	}
	defer readFile.Close()

	var events []any
	scanner := bufio.NewScanner(readFile)
	for scanner.Scan() {
		var event any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		events = append(events, event)
	}

	return events, scanner.Err()
}

// Watch returns a channel for streaming events.
func (f *FileDiscoveryStore) Watch() (<-chan any, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if f.file == nil {
		return nil, errors.New("file discovery store is closed")
	}

	ch := make(chan any, 100)
	f.watchers = append(f.watchers, ch)
	return ch, nil
}

// Close cleans up resources.
func (f *FileDiscoveryStore) Close() error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if f.file == nil {
		return nil
	}

	err := f.file.Close()
	f.file = nil
	return err
}
