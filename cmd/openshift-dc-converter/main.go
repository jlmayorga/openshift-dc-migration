package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jlmayorga/openshift-dc-migration/internal/migration"
)

var version = "dev"
var commit = "unknown"
var buildDate = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	info := migration.BuildInfo{Version: version, Commit: commit, BuildDate: buildDate}
	if err := migration.NewCommand(info).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
