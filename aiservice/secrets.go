package aiservice

import "strings"

const maxConfigBytes = 4 << 20

// SecretStore stores credentials independently of Config. Implementations must
// never persist plaintext credentials. Get returns ErrSecretNotFound if absent.
type SecretStore interface {
	Get(Provider) (string, error)
	Set(Provider, string) error
	Delete(Provider) error
}

func validProvider(p Provider) bool {
	if p == Custom || p == Magpie {
		return true
	}
	s := string(p)
	if !strings.HasPrefix(s, "provider-") || len(s) != len("provider-")+32 {
		return false
	}
	for _, c := range strings.TrimPrefix(s, "provider-") {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
