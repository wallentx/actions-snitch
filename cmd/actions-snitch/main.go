package main

import (
	"context"
	"math"
	"os"
	"os/signal"
	"syscall"

	"github.com/wallentx/actions-snitch/internal/app"
	"golang.org/x/term"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	interactive := terminal(os.Stdin) && terminal(os.Stdout)
	if len(os.Args) == 2 && os.Args[1] == "-c" {
		interactive = terminal(os.Stdin) && terminal(os.Stderr)
	}
	code := app.Run(ctx, os.Args[1:], app.Runtime{Interactive: interactive})
	cancel()
	os.Exit(code)
}

func terminal(file *os.File) bool {
	fd := file.Fd()
	if fd > uintptr(math.MaxInt) {
		return false
	}
	return term.IsTerminal(int(fd))
}
