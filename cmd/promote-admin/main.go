package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"os"
	"time"

	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"

	"github.com/deodes/backend/internal/domain/user"
)

// envOr returns the first non-empty environment value, or fallback.
func envOr(fallback string, keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return fallback
}

// envFlag reports whether any of the given env vars is set to a truthy value.
func envFlag(keys ...string) bool {
	for _, k := range keys {
		switch os.Getenv(k) {
		case "1", "true", "TRUE", "yes", "YES":
			return true
		}
	}
	return false
}

func main() {
	wallet := flag.String("wallet", envOr("", "ADMIN_WALLET"),
		"wallet address of the existing user to promote (env ADMIN_WALLET)")
	email := flag.String("email", envOr("", "ADMIN_EMAIL"),
		"email of the existing user to promote (env ADMIN_EMAIL)")
	force := flag.Bool("force", envFlag("ADMIN_PROMOTE_FORCE"),
		"allow promoting when an admin already exists (env ADMIN_PROMOTE_FORCE=1)")
	dbURL := flag.String("db-url", envOr("", "DEODES_DATABASE_URL", "DATABASE_URL"),
		"postgres connection URL (env DEODES_DATABASE_URL, DEODES_DATABASE_URL or DATABASE_URL)")
	flag.Parse()

	if *dbURL == "" {
		log.Fatal().Msg("missing database URL: pass --db-url or set DEODES_DATABASE_URL / DEODES_DATABASE_URL / DATABASE_URL")
	}

	opts := Options{Wallet: *wallet, Email: *email, Force: *force}
	if err := opts.Validate(); err != nil {
		flag.Usage()
		log.Fatal().Err(err).Msg("invalid options")
	}

	db, err := sql.Open("postgres", *dbURL)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open database connection")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatal().Err(err).Msg("failed to reach database")
	}

	id, alreadyAdmin, err := Promote(ctx, db, opts)
	if err != nil {
		if errors.Is(err, ErrAdminExists) {
			log.Fatal().Err(err).Msg("refused to promote: first admin already exists")
		}
		if errors.Is(err, user.ErrUserNotFound) {
			log.Fatal().Err(err).Msg("target user not found: register or log in first so the account exists")
		}
		log.Fatal().Err(err).Msg("failed to promote user")
	}

	if alreadyAdmin {
		log.Info().Str("userId", id).Msg("user is already an admin; nothing changed")
		return
	}
	// The identifier itself is operational, not secret, and naming it makes
	// the audit trail of who was just privileged unambiguous.
	target := *wallet
	if target == "" {
		target = *email
	}
	log.Info().Str("userId", id).Str("target", target).Msg("promoted user to admin — they must log out and back in for the new role to take effect")
}
