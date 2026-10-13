//go:build !darwin

package aiservice

// KeychainStore is available only on macOS; other platforms fail closed.
type KeychainStore struct{}

func NewKeychainStore() *KeychainStore              { return &KeychainStore{} }
func (*KeychainStore) Get(Provider) (string, error) { return "", ErrSecretStore }
func (*KeychainStore) Set(Provider, string) error   { return ErrSecretStore }
func (*KeychainStore) Delete(Provider) error        { return ErrSecretStore }
