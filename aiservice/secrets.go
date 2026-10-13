package aiservice

// SecretStore stores credentials independently of Config. Implementations must
// never persist plaintext credentials. Get returns ErrSecretNotFound if absent.
type SecretStore interface {
	Get(Provider) (string, error)
	Set(Provider, string) error
	Delete(Provider) error
}

func validProvider(p Provider) bool { return p == Custom || p == Magpie }
