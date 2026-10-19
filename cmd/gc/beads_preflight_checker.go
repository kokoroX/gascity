package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/beads/contract"
	"github.com/gastownhall/gascity/internal/doltauth"
	"github.com/gastownhall/gascity/internal/fsys"
)

var preflightDatabaseProjectIDProbe = func(target contract.DoltConnectionTarget, user, password string) (string, bool, error) {
	var db *sql.DB
	var err error
	if target.Socket != "" {
		db, err = managedDoltOpenDatabaseSocketWithPassword(target.Socket, user, password, target.Database)
	} else {
		db, err = managedDoltOpenDatabaseWithPassword(target.Host, target.Port, user, password, target.Database)
	}
	if err != nil {
		return "", false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return "", false, err
	}
	return readDatabaseProjectID(ctx, db)
}

func newBeadsPreflightChecker(cityPath, provider string) contract.PreflightChecker {
	return contract.PreflightChecker{
		FS:                        fsys.OSFS{},
		Provider:                  provider,
		BDContext:                 preflightBDContextReader(cityPath),
		DatabaseProjectID:         preflightDatabaseProjectIDReader(cityPath),
		DeferIdentityToNativeOpen: preflightIdentityDeferredReader(cityPath),
	}
}

func preflightBDContextReader(cityPath string) func(scope string) (contract.PreflightBDContext, error) {
	return func(scope string) (contract.PreflightBDContext, error) {
		out, err := bdCommandRunnerForCity(cityPath)(scope, "bd", "context", "--json")
		if err != nil {
			return contract.PreflightBDContext{}, err
		}
		var raw struct {
			Backend       string `json:"backend"`
			DoltMode      string `json:"dolt_mode"`
			BDVersion     string `json:"bd_version"`
			SchemaVersion int    `json:"schema_version"`
		}
		if err := json.Unmarshal(out, &raw); err != nil {
			return contract.PreflightBDContext{}, fmt.Errorf("parse bd context --json: %w", err)
		}
		return contract.PreflightBDContext{
			Backend:       raw.Backend,
			DoltMode:      raw.DoltMode,
			BDVersion:     raw.BDVersion,
			SchemaVersion: raw.SchemaVersion,
		}, nil
	}
}

// preflightIdentityDeferredReader reports whether a scope resolves to an
// external Dolt endpoint (e.g. a hosted beads-gateway). The direct root/plaintext
// project_id probe cannot authenticate such endpoints, so when it comes back
// unconfirmed the identity check defers to beadslib's native-open verification
// (which authenticates via the credential command and refuses to connect on a
// _project_id mismatch) instead of degrading the scope off the native store.
func preflightIdentityDeferredReader(cityPath string) func(scope string) bool {
	return func(scope string) bool {
		target, ok, err := preflightScopeDoltTarget(cityPath, scope)
		if err != nil || !ok {
			return false
		}
		return target.External || target.DoltMode == "proxied-server"
	}
}

func preflightScopeDoltTarget(cityPath, scope string) (contract.DoltConnectionTarget, bool, error) {
	target, ok, err := canonicalScopeDoltTarget(cityPath, scope)
	if err != nil || ok {
		return target, ok, err
	}
	bdOwnedDirect, err := scopeIsBdOwnedDirectExternal(cityPath, scope)
	if err != nil || !bdOwnedDirect {
		return contract.DoltConnectionTarget{}, false, err
	}
	target, err = bdOwnedDirectRigTarget(scope)
	if err != nil {
		return contract.DoltConnectionTarget{}, false, err
	}
	return target, true, nil
}

func preflightDatabaseProjectIDReader(cityPath string) func(scope string) (string, bool, error) {
	return func(scope string) (string, bool, error) {
		target, ok, err := preflightScopeDoltTarget(cityPath, scope)
		if err != nil || !ok {
			return "", false, err
		}
		if target.DoltMode == "proxied-server" {
			// Identity is verified by the provider-owned proxied connection;
			// there is no direct SQL endpoint to probe here.
			return "", false, nil
		}
		authScope := doltauth.AuthScopeRoot(cityPath, scope, target)
		auth := doltauth.ResolveScopedFromEnv(authScope, target.User, map[string]string{})
		return preflightDatabaseProjectIDProbe(target, auth.User, auth.Password)
	}
}
