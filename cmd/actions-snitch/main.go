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
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = os.Stdin.Close()
		case <-finished:
		}
	}()
	code := app.Run(ctx, os.Args[1:], app.Runtime{Interactive: terminal(os.Stdin) && terminal(os.Stdout)})
	close(finished)
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
