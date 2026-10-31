package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"filippo.io/age"
)

type FileTokenStorage struct {
	storageDir string
	identity   *age.X25519Identity
}

func NewFileTokenStorage(storageDir string) (*FileTokenStorage, error) {
	if err := os.MkdirAll(storageDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	identityPath := filepath.Join(storageDir, ".key")
	identity, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load identity: %w", err)
	}

	return &FileTokenStorage{
		storageDir: storageDir,
		identity:   identity,
	}, nil
}

func loadOrCreateIdentity(path string) (*age.X25519Identity, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		identity, err := age.GenerateX25519Identity()
		if err != nil {
			return nil, fmt.Errorf("failed to generate identity: %w", err)
		}

		if err := os.WriteFile(path, []byte(identity.String()), 0600); err != nil {
			return nil, fmt.Errorf("failed to save identity: %w", err)
		}

		return identity, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read identity: %w", err)
	}

	identity, err := age.ParseX25519Identity(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse identity: %w", err)
	}

	return identity, nil
}

func (f *FileTokenStorage) Save(providerName string, token *Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return fmt.Errorf("failed to marshal token: %w", err)
	}

	recipient := f.identity.Recipient()
	encryptedPath := f.tokenPath(providerName)

	outFile, err := os.OpenFile(encryptedPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer outFile.Close()

	w, err := age.Encrypt(outFile, recipient)
	if err != nil {
		return fmt.Errorf("failed to create encryptor: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write encrypted token: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("failed to close encryptor: %w", err)
	}

	return nil
}

func (f *FileTokenStorage) Load(providerName string) (*Token, error) {
	encryptedPath := f.tokenPath(providerName)

	inFile, err := os.Open(encryptedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("token not found")
		}
		return nil, fmt.Errorf("failed to open token file: %w", err)
	}
	defer inFile.Close()

	r, err := age.Decrypt(inFile, f.identity)
	if err != nil {
		backupPath := encryptedPath + ".corrupted"
		os.Rename(encryptedPath, backupPath)
		return nil, fmt.Errorf("token file corrupted (backed up to %s)", backupPath)
	}

	var token Token
	if err := json.NewDecoder(r).Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode token: %w", err)
	}

	return &token, nil
}

func (f *FileTokenStorage) Delete(providerName string) error {
	encryptedPath := f.tokenPath(providerName)
	if err := os.Remove(encryptedPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	return nil
}

func (f *FileTokenStorage) Exists(providerName string) bool {
	_, err := os.Stat(f.tokenPath(providerName))
	return err == nil
}

func (f *FileTokenStorage) tokenPath(providerName string) string {
	return filepath.Join(f.storageDir, fmt.Sprintf("%s.enc", providerName))
}
