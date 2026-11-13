package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"panel/internal/secret"
)

func hookHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SetAppDeployHook turns the app's deploy webhook on with token (a new token replaces
// the old one, which stops working), or off when token is empty.
func (s *Store) SetAppDeployHook(ctx context.Context, appID, token string) error {
	if token == "" {
		return affected(s.db.ExecContext(ctx,
			`UPDATE apps SET deploy_hook_hash = NULL, deploy_hook = NULL, updated_at = ? WHERE id = ?`, now(), appID))
	}
	sealed := token
	if s.box != nil {
		var err error
		if sealed, err = s.box.Encrypt([]byte(token)); err != nil {
			return err
		}
	}
	return affected(s.db.ExecContext(ctx,
		`UPDATE apps SET deploy_hook_hash = ?, deploy_hook = ?, updated_at = ? WHERE id = ?`, hookHash(token), sealed, now(), appID))
}

// AppDeployHook returns the app's webhook token; "" when the webhook is off.
func (s *Store) AppDeployHook(ctx context.Context, appID string) (string, error) {
	var stored sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT deploy_hook FROM apps WHERE id = ?`, appID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil || !stored.Valid {
		return "", err
	}
	if !secret.IsSealed(stored.String) {
		return stored.String, nil
	}
	if s.box == nil {
		return "", errors.New("deploy hook is encrypted, and the store has no secret key")
	}
	plain, err := s.box.Decrypt(stored.String)
	return string(plain), err
}

// AppByDeployHook finds the app whose webhook token is token; ErrNotFound for an unknown
// token or an app with its webhook off.
func (s *Store) AppByDeployHook(ctx context.Context, token string) (App, error) {
	if token == "" {
		return App{}, ErrNotFound
	}
	return s.scanApp(s.db.QueryRowContext(ctx, `SELECT `+appCols+` FROM apps WHERE deploy_hook_hash = ?`, hookHash(token)))
}

// SetAppImage changes an image app's image (a webhook deploying a new tag).
func (s *Store) SetAppImage(ctx context.Context, appID, image string) error {
	return affected(s.db.ExecContext(ctx,
		`UPDATE apps SET image = ?, updated_at = ? WHERE id = ? AND source_type = ?`, image, now(), appID, SourceImage))
}
