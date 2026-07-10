// Package auth gestisce autenticazione, sessioni e protezione CSRF.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Session rappresenta una sessione utente attiva.
type Session struct {
	Token     string
	Username  string
	IsAdmin   bool
	CSRFToken string
	ExpiresAt time.Time
}

// SessionStore conserva le sessioni in memoria. Per un NAS single-node è
// sufficiente; sopravvive finché il daemon è attivo (logout su riavvio).
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	s := &SessionStore{
		sessions: make(map[string]*Session),
		ttl:      ttl,
	}
	go s.gc()
	return s
}

// Create genera una nuova sessione con token e CSRF token casuali.
func (s *SessionStore) Create(username string, isAdmin bool) *Session {
	sess := &Session{
		Token:     randToken(32),
		Username:  username,
		IsAdmin:   isAdmin,
		CSRFToken: randToken(32),
		ExpiresAt: time.Now().Add(s.ttl),
	}
	s.mu.Lock()
	s.sessions[sess.Token] = sess
	s.mu.Unlock()
	return sess
}

// Get restituisce la sessione valida associata al token, o nil.
func (s *SessionStore) Get(token string) *Session {
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil
	}
	return sess
}

// Delete invalida una sessione (logout).
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *SessionStore) gc() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		now := time.Now()
		s.mu.Lock()
		for k, v := range s.sessions {
			if now.After(v.ExpiresAt) {
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
	}
}

// HashPassword produce un hash bcrypt.
func HashPassword(password string, cost int) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	return string(b), err
}

// CheckPassword verifica una password contro un hash bcrypt.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
