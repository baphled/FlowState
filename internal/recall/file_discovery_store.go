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
//
// Expected:
//   - filePath identifies the JSONL file to open.
//
// Returns:
//   - A file-backed discovery store.
//   - An error when the file cannot be opened.
//
// Side effects:
//   - Opens or creates the backing file.
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
//
// Expected:
//   - event is JSON-marshalable.
//
// Returns:
//   - An error when marshalling or writing fails.
//
// Side effects:
//   - Appends a JSON line to the backing file.
//   - Notifies any registered watchers.
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
//
// Expected:
//   - The store is open.
//
// Returns:
//   - Events in insertion order.
//   - An error when the file cannot be read.
//
// Side effects:
//   - Reads the backing file.
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
//
// Expected:
//   - The store is open.
//
// Returns:
//   - A channel that receives published events.
//   - An error when the store is closed.
//
// Side effects:
//   - Registers a watcher channel.
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
//
// Expected:
//   - The store may already be closed.
//
// Returns:
//   - An error when closing the backing file fails.
//
// Side effects:
//   - Closes the backing file and marks the store closed.
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
