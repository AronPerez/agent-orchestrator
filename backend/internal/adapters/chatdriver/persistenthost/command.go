package persistenthost

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
)

// ErrCommandUsage distinguishes invalid arguments from runtime failures.
var ErrCommandUsage = errors.New("chat-host requires <session> <data-dir> <workdir> [acp|unreal <fingerprint>] -- <provider> [args...]")

// RunCommand serves both the packaged host executable and the CLI fallback.
// A shutdown signal cancels the host's context so Run takes its orderly path:
// it stops the provider tree and clears the launch records. Without this the
// default signal action would end the host with its records still fencing the
// session until the next reboot.
func RunCommand(ctx context.Context, args []string) error {
	cfg, err := commandConfig(args)
	if err != nil {
		return err
	}
	cfg.Env = os.Environ()
	ctx, stop := signal.NotifyContext(ctx, hostShutdownSignals...)
	defer stop()
	// Only the first signal starts orderly teardown. Restore default handling
	// then, so a further signal still ends a host whose teardown is stuck (for
	// example on a child that holds the provider's stderr). That abrupt exit
	// skips the deferred clear, so the session stays fenced.
	context.AfterFunc(ctx, stop)
	return Run(ctx, cfg)
}

func commandConfig(args []string) (Config, error) {
	protocol := ProtocolRaw
	fingerprint := ""
	separator := 3
	if len(args) > 3 && (args[3] == string(ProtocolACP) || args[3] == string(ProtocolUnreal)) {
		protocol = Protocol(args[3])
		if len(args) > 4 {
			fingerprint = strings.TrimSpace(args[4])
		}
		separator = 5
	}
	if len(args) < separator+2 || args[separator] != "--" || (protocol != ProtocolRaw && fingerprint == "") {
		return Config{}, ErrCommandUsage
	}
	return Config{
		SessionID: strings.TrimSpace(args[0]), DataDir: args[1], Workdir: args[2],
		Argv: args[separator+1:], Protocol: protocol, OwnershipFingerprint: fingerprint,
	}, nil
}
