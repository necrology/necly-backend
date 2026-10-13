package service

import "golang.org/x/crypto/bcrypt"

// BcryptCost is the work factor production hashes new passwords with. The server
// used to run at 4 — bcrypt's minimum — which made an offline attack on a stolen
// users table roughly 56x cheaper than bcrypt's own default (10).
const BcryptCost = 10

// PasswordHasher wraps bcrypt so its cost is configurable and testable.
type PasswordHasher struct {
	cost int
}

func NewPasswordHasher(cost int) PasswordHasher {
	if cost < bcrypt.MinCost {
		cost = bcrypt.DefaultCost
	}
	return PasswordHasher{cost: cost}
}

func (h PasswordHasher) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)
	return string(hash), err
}

func (h PasswordHasher) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// NeedsRehash reports whether a stored hash was produced with a lower cost than
// the current setting. Login uses it to upgrade accounts that predate the cost
// increase, so the old weak hashes disappear as people sign in.
func (h PasswordHasher) NeedsRehash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	return err == nil && cost < h.cost
}
