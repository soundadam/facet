// Package secret keeps proxy passwords in the system keychain (macOS
// Keychain, Secret Service on Linux), keyed by profile ID, so profile.toml
// never holds one.
package secret

import (
	"errors"

	"github.com/zalando/go-keyring"
)

const service = "facet"

func account(profileID string) string { return "proxy:" + profileID }

// ProxyPassword returns "" when no password is stored.
func ProxyPassword(profileID string) (string, error) {
	v, err := keyring.Get(service, account(profileID))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return v, err
}

// SetProxyPassword stores password, or deletes the entry when it is "".
func SetProxyPassword(profileID, password string) error {
	if password == "" {
		return DeleteProxyPassword(profileID)
	}
	return keyring.Set(service, account(profileID), password)
}

func DeleteProxyPassword(profileID string) error {
	err := keyring.Delete(service, account(profileID))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
