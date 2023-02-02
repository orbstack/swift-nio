package spinutil

import (
	"time"

	"github.com/briandowns/spinner"
)

type Spinner struct {
	*spinner.Spinner
}

func Start(color string, message string) *Spinner {
	spin := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	spin.Color(color)
	spin.Suffix = " " + message
	spin.Start()

	return &Spinner{spin}
}

func (s *Spinner) Stop() {
	s.Spinner.Stop()
}
