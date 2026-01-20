package cred

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// readAndValidateCredentialFile reads a credential file and validates its permissions.
func readAndValidateCredentialFile(path string) ([]byte, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("invalid file path: %w", err)
	}

	// Check if file exists and is readable
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("credential file not accessible: %w", err)
	}

	// Check file permissions - warn if too permissive
	if info.Mode().Perm()&0o044 != 0 {
		// File is world-readable or group-readable, which is a security issue
		fmt.Fprintf(os.Stderr, "warning: credential file %s has overly permissive permissions (%o). "+
			"Recommend: chmod 600 %s\n", absPath, info.Mode().Perm(), absPath)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credential file: %w", err)
	}

	return data, nil
}

// FileProvider loads credentials from a JSON file.
type FileProvider struct {
	path string
}

// NewFileProvider creates a new file-based credential provider.
func NewFileProvider(path string) (*FileProvider, error) {
	if path == "" {
		return nil, fmt.Errorf("file path is required")
	}
	return &FileProvider{path: path}, nil
}

// Get retrieves credentials from the JSON file.
func (p *FileProvider) Get(context.Context) (*CredentialSet, error) {
	data, err := readAndValidateCredentialFile(p.path)
	if err != nil {
		return nil, err
	}

	creds, err := parseCredentialFile(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credential file: %w", err)
	}

	return creds, nil
}

// parseCredentialFile parses credential data from JSON bytes.
func parseCredentialFile(data []byte) (*CredentialSet, error) {
	var credFile struct {
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		SessionToken    string `json:"session_token,omitempty"`
	}

	if err := json.Unmarshal(data, &credFile); err != nil {
		return nil, fmt.Errorf("invalid JSON format: %w", err)
	}

	if credFile.AccessKeyID == "" {
		return nil, fmt.Errorf("access_key_id is required in credentials file")
	}
	if credFile.SecretAccessKey == "" {
		return nil, fmt.Errorf("secret_access_key is required in credentials file")
	}

	return &CredentialSet{
		AccessKeyID:     credFile.AccessKeyID,
		SecretAccessKey: credFile.SecretAccessKey,
		SessionToken:    credFile.SessionToken,
	}, nil
}
