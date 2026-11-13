package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"panel/internal/secret"
)

// UseSecrets sets the key that encrypts app environments at rest, then encrypts every
// environment still stored as plain JSON (written before encryption existed, or by a
// store without a key). It runs once at startup, before the store serves requests.
func (s *Store) UseSecrets(ctx context.Context, box *secret.Box) (int, error) {
	if box == nil {
		return 0, errors.New("store: no secret key")
	}
	s.box = box
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, environment FROM apps WHERE environment NOT LIKE 'v1:%'`)
	if err != nil {
		return 0, err
	}
	plain := map[string]string{}
	for rows.Next() {
		var id, env string
		if err := rows.Scan(&id, &env); err != nil {
			rows.Close()
			return 0, err
		}
		plain[id] = env
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for id, env := range plain {
		vars, err := s.openEnvironment(env)
		if err != nil {
			return 0, fmt.Errorf("app %s environment: %w", id, err)
		}
		sealed, err := s.sealEnvironment(vars)
		if err != nil {
			return 0, err
		}
		// updated_at is left alone: the app did not change, only how it is stored.
		if _, err := tx.ExecContext(ctx, `UPDATE apps SET environment = ? WHERE id = ?`, sealed, id); err != nil {
			return 0, err
		}
	}
	return len(plain), tx.Commit()
}

// sealEnvironment is how an environment is stored: JSON encrypted with the store's key.
// Without a key (tests only) it stays plain JSON.
func (s *Store) sealEnvironment(env map[string]string) (string, error) {
	if env == nil {
		env = map[string]string{}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	if s.box == nil {
		return string(raw), nil
	}
	return s.box.Encrypt(raw)
}

// openEnvironment reads a stored environment, encrypted or (from before encryption) plain.
func (s *Store) openEnvironment(stored string) (map[string]string, error) {
	raw := []byte(stored)
	if secret.IsSealed(stored) {
		if s.box == nil {
			return nil, errors.New("encrypted, and the store has no secret key")
		}
		var err error
		if raw, err = s.box.Decrypt(stored); err != nil {
			return nil, err
		}
	}
	env := map[string]string{}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env == nil { // "null"
		env = map[string]string{}
	}
	return env, nil
}
