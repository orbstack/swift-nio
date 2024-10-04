package spinutil

import (
	"os"
	"os/signal"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fatih/color"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type Spinner struct {
	spinner *spinner.Spinner
	stopCh  chan struct{}
}

func Start(color string, message string) *Spinner {
	spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	spin.Color(color)
	spin.Suffix = " " + message
	spin.Start()

	sp := &Spinner{
		spinner: spin,
		stopCh:  make(chan struct{}),
	}
	go sp.monitorSignals()
	return sp
}

func (s *Spinner) monitorSignals() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, unix.SIGINT, unix.SIGTERM, unix.SIGQUIT)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		s.Stop()
		if term.IsTerminal(int(os.Stderr.Fd())) {
			color.New(color.FgRed).Fprintln(os.Stderr, "Canceled")
		}
		// simulate the signal exit
		signal.Stop(sigCh)
		unix.Kill(os.Getpid(), sig.(unix.Signal))
	case <-s.stopCh:
	}
}

func (s *Spinner) Stop() {
	s.spinner.Stop()
	close(s.stopCh)
}
