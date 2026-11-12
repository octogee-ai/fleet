package cli

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
)

const (
	spinnerDelay = 300 * time.Millisecond
	spinnerFrame = 80 * time.Millisecond
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinner shows that the command is waiting, on a terminal's stderr only. It
// appears after a short delay so quick answers do not flicker.
type spinner struct {
	out   io.Writer
	style palette

	mutex sync.Mutex
	stop  chan struct{}
	done  chan struct{}
}

func newSpinner(runtime Runtime, style palette) *spinner {
	if !isTerminal(runtime.Stderr) || runtime.Getenv("TERM") == "dumb" {
		return nil
	}
	return &spinner{out: runtime.Stderr, style: style}
}

func (s *spinner) start(label string) {
	if s == nil {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.stop != nil {
		return
	}
	s.stop, s.done = make(chan struct{}), make(chan struct{})
	go s.spin(label, s.stop, s.done)
}

func (s *spinner) end() {
	if s == nil {
		return
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.stop == nil {
		return
	}
	close(s.stop)
	<-s.done
	s.stop, s.done = nil, nil
}

func (s *spinner) spin(label string, stop, done chan struct{}) {
	defer close(done)
	select {
	case <-stop:
		return
	case <-time.After(spinnerDelay):
	}
	ticker := time.NewTicker(spinnerFrame)
	defer ticker.Stop()
	for frame := 0; ; frame++ {
		fmt.Fprintf(s.out, "\r%s %s", s.style.cyan(spinnerFrames[frame%len(spinnerFrames)]), s.style.dim(label))
		select {
		case <-stop:
			fmt.Fprint(s.out, "\r\x1b[K")
			return
		case <-ticker.C:
		}
	}
}

// waitingDoer runs the spinner while the Admin API has not yet answered.
type waitingDoer struct {
	inner   admin.Doer
	spinner *spinner
}

func (d waitingDoer) Do(request *http.Request) (*http.Response, error) {
	d.spinner.start(waitingLabel(request))
	defer d.spinner.end()
	return d.inner.Do(request)
}

func waitingLabel(request *http.Request) string {
	path := strings.TrimPrefix(request.URL.Path, "/api/admin")
	return fmt.Sprintf("Waiting for the Admin API (%s %s)…", request.Method, path)
}
