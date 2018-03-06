package twig

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

const (
	Ldate         = log.Ldate
	Ltime         = log.Ltime
	Lmicroseconds = log.Lmicroseconds
	Llongfile     = log.Llongfile
	Lshortfile    = log.Lshortfile
	LUTC          = log.LUTC
	LstdFlags     = Ldate | Ltime
)

type Twig struct {
	info, debug *log.Logger
	fdebug      bool
	flags       int
	sync.Mutex
}

func New(out io.Writer, flags int) *Twig {
	return &Twig{
		info:  log.New(out, "INFO ", flags),
		debug: log.New(out, "DEBUG ", flags),
		flags: flags,
	}
}

func (t *Twig) Info(v ...interface{}) {
	t.info.Output(2, fmt.Sprintln(v...))
}

func (t *Twig) Infof(format string, v ...interface{}) {
	t.info.Output(2, fmt.Sprintf(format, v...))
}

func (t *Twig) Debug(v ...interface{}) {
	t.Lock()
	defer t.Unlock()
	if t.fdebug {
		t.debug.Output(2, fmt.Sprintln(v...))
	}
}

func (t *Twig) Debugf(format string, v ...interface{}) {
	t.Lock()
	defer t.Unlock()
	if t.fdebug {
		t.debug.Output(2, fmt.Sprintf(format, v...))
	}
}

func (t *Twig) SetDebug(b bool) {
	t.Lock()
	defer t.Unlock()
	t.fdebug = b
}

func (t *Twig) SetOutput(w io.Writer) {
	t.info.SetOutput(w)
	t.debug.SetOutput(w)
}

func (t *Twig) Flags() int {
	return t.flags
}

func (t *Twig) SetFlags(flags int) {
	t.info.SetFlags(flags)
	t.debug.SetFlags(flags)
	t.flags = flags
}

var std = New(os.Stderr, LstdFlags)

func SetDebug(b bool) {
	std.SetDebug(b)
}

func Info(v ...interface{}) {
	std.info.Output(2, fmt.Sprintln(v...))
}

func Infof(format string, v ...interface{}) {
	std.info.Output(2, fmt.Sprintf(format, v...))
}

func Debug(v ...interface{}) {
	std.Lock()
	defer std.Unlock()
	if std.fdebug {
		std.debug.Output(2, fmt.Sprintln(v...))
	}
}

func Debugf(format string, v ...interface{}) {
	std.Lock()
	defer std.Unlock()
	if std.fdebug {
		std.debug.Output(2, fmt.Sprintf(format, v...))
	}
}

func SetOutput(w io.Writer) {
	std.SetOutput(w)
}

func Flags() int {
	return std.Flags()
}

func SetFlags(flags int) {
	std.SetFlags(flags)
}
