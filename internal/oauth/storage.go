package oauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"filippo.io/age"
)

var (
	errKeyNotFound      = errors.New("encryption key not found")
	errDecryptionFailed = errors.New("decryption failed")
	errInvalidToken     = errors.New("invalid token data")
)

const (
	tokenFileName = "oauth_tokens.age"
	keyFileName   = ".oauth_key"
)

// EncryptedStore implements TokenStore with age encryption.
type EncryptedStore struct {
	mu        sync.RWMutex
	tokensDir string
	identity  age.Identity
	recipient age.Recipient
}

// NewEncryptedStore creates a new encrypted token store.
//
// Expected:
//   - dataDir is a valid directory path for storing encrypted tokens.
//
// Returns:
//   - A configured EncryptedStore, or an error if creation fails.
//
// Side effects:
//   - Creates the tokens subdirectory if it doesn't exist.
//   - Generates or loads an encryption key.
func NewEncryptedStore(dataDir string) (*EncryptedStore, error) {
	tokensDir := filepath.Join(dataDir, "tokens")
	if err := os.MkdirAll(tokensDir, 0o700); err != nil {
		return nil, err
	}

	passphrase, err := loadOrCreateKey(tokensDir)
	if err != nil {
		return nil, err
	}

	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, fmt.Errorf("creating scrypt recipient: %w", err)
	}

	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, fmt.Errorf("creating scrypt identity: %w", err)
	}

	return &EncryptedStore{
		tokensDir: tokensDir,
		identity:  identity,
		recipient: recipient,
	}, nil
}

// loadOrCreateKey loads the existing encryption key or creates a new one.
//
// Expected:
//   - tokensDir is a valid directory path.
//
// Returns:
//   - The passphrase for encryption, or an error if creation/loading fails.
//
// Side effects:
//   - Creates the key file if it doesn't exist.
func loadOrCreateKey(tokensDir string) (string, error) {
	keyPath := filepath.Join(tokensDir, keyFileName)

	if data, err := os.ReadFile(keyPath); err == nil && len(data) > 0 {
		return string(data), nil
	}

	passphrase := generatePassphrase()
	if err := os.WriteFile(keyPath, []byte(passphrase), 0o600); err != nil {
		return "", err
	}

	return passphrase, nil
}

// Store saves an encrypted token for the given provider.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//   - token is a non-nil TokenResponse with a non-empty AccessToken.
//
// Returns:
//   - An error if storage fails, nil otherwise.
//
// Side effects:
//   - Writes an encrypted file to the tokens directory.
func (s *EncryptedStore) Store(provider string, token *TokenResponse) error {
	if provider == "" {
		return errors.New("provider cannot be empty")
	}
	if token == nil || token.AccessToken == "" {
		return errInvalidToken
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tokenPath := s.tokenFilePath(provider)

	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("marshalling token: %w", err)
	}

	var buf bytes.Buffer
	encrypted, err := age.Encrypt(&buf, s.recipient)
	if err != nil {
		return fmt.Errorf("creating encryptor: %w", err)
	}

	if _, err := encrypted.Write(data); err != nil {
		return fmt.Errorf("encrypting data: %w", err)
	}

	if err := encrypted.Close(); err != nil {
		return fmt.Errorf("finalizing encryption: %w", err)
	}

	return os.WriteFile(tokenPath, buf.Bytes(), 0o600)
}

// Retrieve loads and decrypts a token for the given provider.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//
// Returns:
//   - The decrypted TokenResponse, or an error if retrieval fails.
//
// Side effects:
//   - Reads the encrypted token file from the tokens directory.
func (s *EncryptedStore) Retrieve(provider string) (*TokenResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokenPath := s.tokenFilePath(provider)

	data, err := os.ReadFile(tokenPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errKeyNotFound
		}
		return nil, err
	}

	decrypted, err := age.Decrypt(bytes.NewReader(data), s.identity)
	if err != nil {
		return nil, errDecryptionFailed
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, decrypted); err != nil {
		return nil, errDecryptionFailed
	}

	var token TokenResponse
	if err := json.Unmarshal(buf.Bytes(), &token); err != nil {
		return nil, errInvalidToken
	}

	return &token, nil
}

// Delete removes the stored token for the given provider.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//
// Returns:
//   - An error if deletion fails, nil otherwise.
//
// Side effects:
//   - Deletes the encrypted token file from the tokens directory.
func (s *EncryptedStore) Delete(provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tokenPath := s.tokenFilePath(provider)
	if err := os.Remove(tokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// HasToken checks if a token exists for the given provider.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//
// Returns:
//   - true if a token file exists for the provider, false otherwise.
//
// Side effects:
//   - None.
func (s *EncryptedStore) HasToken(provider string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tokenPath := s.tokenFilePath(provider)
	_, err := os.Stat(tokenPath)
	return err == nil
}

// tokenFilePath returns the path to the token file for the given provider.
//
// Expected:
//   - provider is a non-empty string identifying the provider.
//
// Returns:
//   - The full path to the encrypted token file.
//
// Side effects:
//   - None.
func (s *EncryptedStore) tokenFilePath(provider string) string {
	return filepath.Join(s.tokensDir, provider+"_"+tokenFileName)
}

// generatePassphrase generates a random passphrase for key encryption.
//
// Returns:
//   - A 32-character random passphrase.
//
// Side effects:
//   - None.
func generatePassphrase() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		b[i] = charset[i*7%len(charset)]
	}
	return string(b)
}
