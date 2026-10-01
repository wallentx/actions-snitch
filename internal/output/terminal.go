package output

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/xo/terminfo"
)

type Style struct {
	colors                 [9]string
	bold, reset            string
	HideCursor, ShowCursor string
}

func TerminalStyle(name string) Style {
	info, err := terminfo.Load(name)
	if err != nil {
		return Style{}
	}
	s := Style{HideCursor: string(info.Strings[terminfo.CursorInvisible]), ShowCursor: string(info.Strings[terminfo.CursorNormal])}
	if info.Nums[terminfo.MaxColors] <= 0 {
		return s
	}
	s.bold = string(info.Strings[terminfo.EnterBoldMode])
	s.reset = string(info.Strings[terminfo.ExitAttributeMode])
	for i := range s.colors {
		s.colors[i] = terminfo.Printf(info.Strings[terminfo.SetAForeground], i)
	}
	return s
}

func (s Style) Wrap(class, text string) string {
	color := ""
	switch class {
	case "header":
		color = s.bold + s.colors[5]
	case "success":
		color = s.colors[2]
	case "warning":
		color = s.colors[3]
	case "error":
		color = s.colors[1]
	case "info":
		color = s.colors[7]
	case "help", "status":
		color = s.colors[8]
	case "code":
		color = s.colors[4]
	case "section":
		color = s.colors[6]
	}
	if color == "" {
		return text
	}
	return color + text + s.reset
}

func (s Style) Usage(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		class := "success"
		switch i {
		case 0:
			class = "header"
		case 1:
			class = "help"
		}
		lines[i] = s.Wrap(class, line)
	}
	return strings.Join(lines, "\n")
}

// StartSpinner owns its writer until stop returns. The caller buffers findings
// during animation and restores normal output only after joining the goroutine.
func StartSpinner(ctx context.Context, w io.Writer, style Style, trueColor bool) func() {
	if style.HideCursor == "" {
		return func() {}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.WriteString(w, style.HideCursor)
		defer func() { _, _ = io.WriteString(w, "\x1b[2K\r"+style.ShowCursor+"\r") }()
		ticker := time.NewTicker(40 * time.Millisecond)
		defer ticker.Stop()
		frame := 0
		for {
			if _, err := io.WriteString(w, spinnerFrame(frame, trueColor)+"\r"); err != nil {
				return
			}
			frame++
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }
}

func spinnerFrame(frame int, trueColor bool) string {
	letters := []rune("    𝙻𝙾𝙰𝙳𝙸𝙽𝙶...    ")
	greens := []int{46, 40, 34, 28, 22, 16}
	var b strings.Builder
	for slot := 0; slot < 17; slot++ {
		age := (frame - slot) % 17
		if age < 0 {
			age += 17
		}
		if age >= 11 || frame-age < 0 {
			b.WriteByte(' ')
			continue
		}
		char := letters[(frame-age)%len(letters)]
		if trueColor {
			_, _ = fmt.Fprintf(&b, "\x1b[38;2;0;%d;0m%c\x1b[0m", 255-255*age/10, char)
		} else {
			_, _ = fmt.Fprintf(&b, "\x1b[38;5;%dm%c\x1b[0m", greens[age*(len(greens)-1)/10], char)
		}
	}
	return b.String()
}
