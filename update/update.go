// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package update

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"

	"github.com/blang/semver"
	"github.com/brimstone/clank/version"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
	"github.com/spf13/cobra"
)

func PublicKey() (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(`
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEgBcgOYso+D8c/qKaGfafV7eyhHKl
3LkKk0uulV/ugSN++tqpBTD45zbHIom0vmDN4YVy8kPhk0nOu1OGkkEQug==
-----END PUBLIC KEY-----`))
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("Failed to parse public key: " + err.Error())
	}

	pubKey, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("PublicKey is not ECDSA")
	}

	return pubKey, nil
}

func Cmd() *cobra.Command {
	var listCmd = &cobra.Command{
		Use:   "update",
		Short: "Update clank",
		Long: `Check github.com/brimstone/clank for the latest version of clank.
Download it if available and replace the current executable.`,
		RunE: Run,
	}

	return listCmd
}

func Run(cmd *cobra.Command, args []string) error {
	v := semver.MustParse(version.Version)
	// Check for updates
	pubkey, err := PublicKey()
	if err != nil {
		return fmt.Errorf("error occurred while extracting public key: %w", err)
	}

	slog.Info("Checking for updates")

	up, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ECDSAValidator{
			PublicKey: pubkey,
		},
		Filters: []string{
			version.Binary,
		},
	})
	if err != nil {
		return fmt.Errorf("unable to create updater component: %w", err)
	}

	latest, err := up.UpdateSelf(v, "brimstone/clank")

	if err != nil {
		return fmt.Errorf("binary update failed: %w", err)
	}

	if latest.Version.Equals(v) {
		// latest version is the same as current version. It means current binary is up to date.
		slog.Info("Current binary is the latest", "version", v)
	} else {
		slog.Info("Successfully updated", "version", latest.Version)
	}

	return nil
}
