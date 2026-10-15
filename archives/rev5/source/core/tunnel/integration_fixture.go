//go:build itomtest

package tunnel

import (
	"context"
	"itom.local/core/servercfg"
)

// ServeIntegrationFixture exposes production supervision to cross-module tests.
// This symbol, its injected mail dialer and its private TCP test targets are
// absent from every normal server and Android build. Never set itomtest in a
// production build.
func ServeIntegrationFixture(ctx context.Context, cfg servercfg.Config, dir string, dial Dial, log func(string)) error {
	return serveWithOptions(ctx, cfg, dir, log, serverOptions{dial: dial, allowPrivate: true, diagnostics: cfg.DiagnosticsMode != "off"})
}
