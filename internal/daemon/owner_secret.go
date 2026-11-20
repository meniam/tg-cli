package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"tg-cli/internal/store"
)

var (
	ErrOwnerPassphraseRequired = errors.New("owner passphrase required")
	ErrOwnerPassphraseWrong    = errors.New("owner passphrase is wrong")
	ErrOwnerPassphraseLocked   = errors.New("owner passphrase temporarily locked")
	ErrOwnerPassphraseExists   = errors.New("owner passphrase already exists")
)

const ownerMemory uint32 = 64 * 1024
const ownerIterations uint32 = 3
const ownerThreads uint8 = 1
const ownerSaltBytes = 16
const ownerHashBytes = 32

// OwnerSecrets serializes passphrase checks and throttles failures in memory.
type OwnerSecrets struct {
	db           *store.DB
	mu           sync.Mutex
	failures     int
	blockedUntil time.Time
	now          func() time.Time
}

// NewOwnerSecrets creates the owner passphrase manager for one daemon.
func NewOwnerSecrets(db *store.DB) *OwnerSecrets { return &OwnerSecrets{db: db, now: time.Now} }

func hashOwnerPassphrase(phrase string) (string, error) {
	salt := make([]byte, ownerSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", errors.New("cannot generate owner secret salt")
	}
	hash := argon2.IDKey([]byte(phrase), salt, ownerIterations, ownerMemory, ownerThreads, ownerHashBytes)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", ownerMemory, ownerIterations, ownerThreads, enc.EncodeToString(salt), enc.EncodeToString(hash)), nil
}

func checkOwnerHash(phrase, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("invalid owner secret hash")
	}
	// Only the exact supported parameters are accepted: corrupt hashes cannot
	// request unbounded memory or CPU from the daemon.
	expected := fmt.Sprintf("m=%d,t=%d,p=%d", ownerMemory, ownerIterations, ownerThreads)
	if parts[3] != expected {
		return false, errors.New("invalid owner secret hash parameters")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != ownerSaltBytes {
		return false, errors.New("invalid owner secret hash")
	}
	hash, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(hash) != ownerHashBytes {
		return false, errors.New("invalid owner secret hash")
	}
	got := argon2.IDKey([]byte(phrase), salt, ownerIterations, ownerMemory, ownerThreads, ownerHashBytes)
	return subtle.ConstantTimeCompare(got, hash) == 1, nil
}

func checkNewOwnerPhrase(phrase string) error {
	if utf8.RuneCountInString(phrase) < 12 || !utf8.ValidString(phrase) {
		return errors.New("owner passphrase must contain at least 12 characters")
	}
	return nil
}

// Init sets the first passphrase, refusing to replace an existing hash.
func (o *OwnerSecrets) Init(ctx context.Context, phrase string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := checkNewOwnerPhrase(phrase); err != nil {
		return err
	}
	_, found, err := o.db.OwnerSecret(ctx)
	if err != nil {
		return errors.New("owner secret unavailable")
	}
	if found {
		return ErrOwnerPassphraseExists
	}
	hash, err := hashOwnerPassphrase(phrase)
	if err != nil {
		return err
	}
	created, err := o.db.CreateOwnerSecret(ctx, hash)
	if err != nil {
		return errors.New("owner secret unavailable")
	}
	if !created {
		return ErrOwnerPassphraseExists
	}
	return nil
}

func (o *OwnerSecrets) verify(ctx context.Context, phrase string) (string, error) {
	if o.now().Before(o.blockedUntil) {
		return "", ErrOwnerPassphraseLocked
	}
	if phrase == "" {
		return "", ErrOwnerPassphraseRequired
	}
	hash, found, err := o.db.OwnerSecret(ctx)
	if err != nil {
		return "", errors.New("owner secret unavailable")
	}
	if !found {
		return "", ErrOwnerPassphraseRequired
	}
	valid, err := checkOwnerHash(phrase, hash)
	if err != nil {
		return "", errors.New("owner secret unavailable")
	}
	if !valid {
		o.failures++
		if o.failures >= 5 {
			delay := time.Minute
			for i := 5; i < o.failures && delay < time.Hour; i++ {
				delay *= 2
			}
			if delay > time.Hour {
				delay = time.Hour
			}
			o.blockedUntil = o.now().Add(delay)
		}
		return "", ErrOwnerPassphraseWrong
	}
	o.failures = 0
	o.blockedUntil = time.Time{}
	return hash, nil
}

// Verify checks a passphrase before a protected operation.
func (o *OwnerSecrets) Verify(ctx context.Context, phrase string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	_, err := o.verify(ctx, phrase)
	return err
}

// Change checks the old passphrase and atomically replaces its hash.
func (o *OwnerSecrets) Change(ctx context.Context, oldPhrase, newPhrase string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	previous, err := o.verify(ctx, oldPhrase)
	if err != nil {
		return err
	}
	if err := checkNewOwnerPhrase(newPhrase); err != nil {
		return err
	}
	next, err := hashOwnerPassphrase(newPhrase)
	if err != nil {
		return err
	}
	changed, err := o.db.ReplaceOwnerSecret(ctx, previous, next)
	if err != nil {
		return errors.New("owner secret unavailable")
	}
	if !changed {
		return errors.New("owner secret changed concurrently")
	}
	return nil
}

// ResetOwnerSecretOffline removes the hash only while both profile and database locks are held.
func ResetOwnerSecretOffline(ctx context.Context, paths Paths) error {
	release, err := acquireLock(paths.Lock)
	if err != nil {
		return err
	}
	defer release()
	releaseDB, err := LockDatabase(paths)
	if err != nil {
		return err
	}
	defer releaseDB()
	if _, err := os.Stat(paths.Database); err != nil {
		return fmt.Errorf("inspect profile database: %w", err)
	}
	if err := CheckDatabaseBinding(paths); err != nil {
		return err
	}
	db, err := store.Open(paths.Database)
	if err != nil {
		return fmt.Errorf("open profile database: %w", err)
	}
	defer db.Close()
	if err := db.DeleteOwnerSecret(ctx); err != nil {
		return errors.New("owner secret unavailable")
	}
	return nil
}
