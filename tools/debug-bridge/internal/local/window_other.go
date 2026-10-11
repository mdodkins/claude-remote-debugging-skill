//go:build !(linux || darwin || freebsd || openbsd || netbsd)

package local

import (
	"context"
	"errors"
)

// Window is unavailable on this platform: -local relies on tmux and a FIFO.
type Window struct{ Pane, Session string }

// Options for Open.
type Options struct{ Session, Name, Dir, Shell string }

var errUnsupported = errors.New("-local needs tmux, which is not available on this platform")

func InsideTmux() bool                                    { return false }
func Open(Options) (*Window, error)                       { return nil, errUnsupported }
func (w *Window) Run(context.Context, func(string)) error { return errUnsupported }
func (w *Window) SendLine(string) error                   { return errUnsupported }
func (w *Window) Close()                                  {}
func (w *Window) AttachHint() string                      { return "" }
func (w *Window) Attach(context.Context) error            { return errUnsupported }
