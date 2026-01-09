package database

import (
	"context"
	"fmt"

	"spotter/ent"
	"spotter/internal/crypto"

	_ "github.com/mattn/go-sqlite3"
)

func NewClient(driver, source string, encryptor *crypto.Encryptor) (*ent.Client, error) {
	client, err := ent.Open(driver, source)
	if err != nil {
		return nil, fmt.Errorf("failed opening connection to %s: %v", driver, err)
	}

	// Register encryption/decryption hooks if encryptor is provided
	if encryptor != nil {
		RegisterEncryptionHooks(client, encryptor)
	}

	// Run the auto migration tool.
	if err := client.Schema.Create(context.Background()); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed creating schema resources: %v", err)
	}

	return client, nil
}
