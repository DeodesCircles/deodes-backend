// Command promote-admin is the documented, env-gated bootstrap path to the
// first admin account.
//
// New deployments start with zero admins (every registration path defaults to
// role "user" and AdminMiddleware rejects anything that is not role "admin"),
// so without this tool there would be no legitimate way to obtain an admin.
// It promotes exactly one existing, non-deleted user to role "admin":
//
//	ADMIN_WALLET=0xabc... go run ./cmd/promote-admin
//	ADMIN_EMAIL=owner@example.com go run ./cmd/promote-admin --force
//
// Safety properties, enforced by Promote and covered by promote_test.go:
//   - The target user must already exist; the tool never creates accounts.
//   - When at least one admin already exists the tool refuses to promote
//     another unless --force (or ADMIN_PROMOTE_FORCE=1) is given, so a stray
//     invocation cannot silently multiply admins.
//   - Promoting a user that is already an admin is a no-op success.
//   - Soft-deleted users are never matched, so a deleted account cannot be
//     re-admitted through this path (use the admin restore endpoint instead).
//
// The promoted user must log out and log back in afterwards: the role claim
// is snapshotted into the JWT at session creation, so pre-existing sessions
// keep their old role until the next login.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/deodes/backend/internal/domain/user"
)

// ErrAdminExists is returned when the users table already holds an admin and
// the caller did not pass Force. It is a sentinel so main can report the
// refusal distinctly from operational failures.
var ErrAdminExists = errors.New("an admin already exists")

// Options selects the single user to promote. Exactly one of Wallet or Email
// must be set; see Validate.
type Options struct {
	// Wallet is matched verbatim against users.wallet_address (0x addresses
	// are case-sensitive, so no normalization is applied).
	Wallet string
	// Email is matched against the canonical hashed representation used by
	// every other lookup (user.HashEmail), after trimming whitespace.
	Email string
	// Force permits promoting an additional admin when one already exists.
	Force bool
}

// Validate reports whether exactly one identifier was supplied.
func (o Options) Validate() error {
	wallet := strings.TrimSpace(o.Wallet)
	email := strings.TrimSpace(o.Email)
	if wallet != "" && email != "" {
		return errors.New("supply exactly one of --wallet/ADMIN_WALLET or --email/ADMIN_EMAIL, not both")
	}
	if wallet == "" && email == "" {
		return errors.New("supply --wallet/ADMIN_WALLET or --email/ADMIN_EMAIL to select the user to promote")
	}
	return nil
}

// identifier returns the normalized lookup the promotion runs against.
func (o Options) identifier() (wallet, email string) {
	return strings.TrimSpace(o.Wallet), strings.TrimSpace(o.Email)
}

// Promote sets the selected user's role to "admin". It returns the promoted
// user's id; alreadyAdmin is true when the user already held the admin role
// and nothing was changed.
func Promote(ctx context.Context, db *sql.DB, opts Options) (userID string, alreadyAdmin bool, err error) {
	if err := opts.Validate(); err != nil {
		return "", false, err
	}
	wallet, email := opts.identifier()

	var id, role string
	if wallet != "" {
		id, role, err = findByWallet(ctx, db, wallet)
	} else {
		id, role, err = findByEmail(ctx, db, email)
	}
	if err != nil {
		return "", false, err
	}

	if role == string(user.RoleAdmin) {
		return id, true, nil
	}

	if !opts.Force {
		count, err := countAdmins(ctx, db)
		if err != nil {
			return "", false, fmt.Errorf("checking for existing admins: %w", err)
		}
		if count > 0 {
			return "", false, fmt.Errorf("%w: %d admin(s) already present; re-run with --force to promote another", ErrAdminExists, count)
		}
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE users SET role = $1, updated_at = NOW() WHERE id = $2 AND deleted_at IS NULL`,
		string(user.RoleAdmin), id); err != nil {
		return "", false, fmt.Errorf("promoting user to admin: %w", err)
	}
	return id, false, nil
}

// findByWallet resolves a live user id and role by exact wallet match.
func findByWallet(ctx context.Context, db *sql.DB, wallet string) (string, string, error) {
	var id, role string
	err := db.QueryRowContext(ctx,
		`SELECT id, role FROM users WHERE wallet_address = $1 AND deleted_at IS NULL`,
		wallet).Scan(&id, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("no active user with wallet %q: %w", wallet, user.ErrUserNotFound)
	}
	if err != nil {
		return "", "", fmt.Errorf("looking up user by wallet: %w", err)
	}
	return id, role, nil
}

// emailCandidates lists the hashed-email lookup keys for a supplied address.
// The as-given form is always tried first to match the canonical
// registration/login behavior; a lowercased fallback covers addresses
// registered with different casing without changing what is stored.
func emailCandidates(email string) []string {
	candidates := []string{user.HashEmail(email)}
	if lowered := strings.ToLower(email); lowered != email {
		candidates = append(candidates, user.HashEmail(lowered))
	}
	return candidates
}

// findByEmail resolves a live user id and role by hashed-email match.
func findByEmail(ctx context.Context, db *sql.DB, email string) (string, string, error) {
	for _, hashed := range emailCandidates(email) {
		var id, role string
		err := db.QueryRowContext(ctx,
			`SELECT id, role FROM users WHERE email = $1 AND deleted_at IS NULL`,
			hashed).Scan(&id, &role)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("looking up user by email: %w", err)
		}
		return id, role, nil
	}
	return "", "", fmt.Errorf("no active user with that email: %w", user.ErrUserNotFound)
}

// countAdmins reports how many live users currently hold the admin role.
func countAdmins(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = $1 AND deleted_at IS NULL`,
		string(user.RoleAdmin)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
