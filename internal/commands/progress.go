package commands

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	progressMinimum = 1 << 20
	progressTick    = 128 << 20
	progressWidth   = 24
)

type progressDisplay struct {
	mu       sync.Mutex
	out      io.Writer
	terminal bool
	active   map[*progressTask]struct{}
	lastDraw time.Time
}

type progressTask struct {
	display *progressDisplay
	label   string
	total   int64
	written int64
	nextLog int64
}

type progressWriter struct {
	dst  io.Writer
	task *progressTask
}

type progressReader struct {
	src  io.Reader
	task *progressTask
}

var archiveProgress = newProgressDisplay(os.Stderr, stderrIsTerminal())

func newProgressDisplay(out io.Writer, terminal bool) *progressDisplay {
	return &progressDisplay{out: out, terminal: terminal, active: map[*progressTask]struct{}{}}
}

func stderrIsTerminal() bool {
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (d *progressDisplay) start(label string, total int64) *progressTask {
	t := &progressTask{display: d, label: label, total: total, nextLog: progressTick}
	d.mu.Lock()
	d.active[t] = struct{}{}
	d.mu.Unlock()
	return t
}

func (t *progressTask) add(n int64) {
	if t == nil || n <= 0 {
		return
	}
	d := t.display
	d.mu.Lock()
	t.written += n
	if d.terminal {
		if t.written >= progressMinimum && time.Since(d.lastDraw) >= 100*time.Millisecond {
			d.drawLocked(t)
		}
	} else {
		for t.written >= t.nextLog {
			_, _ = fmt.Fprintf(d.out, "%s: %s written\n", t.label, formatProgressBytes(t.nextLog))
			t.nextLog += progressTick
		}
	}
	d.mu.Unlock()
}

func (t *progressTask) finish(success bool) {
	if t == nil {
		return
	}
	d := t.display
	d.mu.Lock()
	delete(d.active, t)
	if d.terminal {
		if t.written >= progressMinimum {
			marker, color := "✗", "31"
			if success {
				marker, color = "✓", "32"
			}
			_, _ = fmt.Fprintf(d.out, "\r\x1b[2K\x1b[%sm%s\x1b[0m %s · %s\n", color, marker, t.label, formatProgressBytes(t.written))
			if len(d.active) > 0 {
				d.drawLocked(nil)
			}
		}
	} else if success && t.written >= progressTick && t.written < t.nextLog {
		_, _ = fmt.Fprintf(d.out, "%s: %s complete\n", t.label, formatProgressBytes(t.written))
	}
	d.mu.Unlock()
}

func (d *progressDisplay) drawLocked(preferred *progressTask) {
	if len(d.active) == 0 {
		return
	}
	tasks := make([]*progressTask, 0, len(d.active))
	for task := range d.active {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].label < tasks[j].label })
	if preferred != nil {
		for i, task := range tasks {
			if task == preferred {
				copy(tasks[1:i+1], tasks[0:i])
				tasks[0] = preferred
				break
			}
		}
	}
	var activeBytes, total int64
	knownTotal := true
	for _, task := range tasks {
		activeBytes += task.written
		if task.total <= 0 {
			knownTotal = false
		} else {
			total += task.total
		}
	}
	filled := 0
	if knownTotal && total > 0 {
		filled = int(activeBytes * progressWidth / total)
		if filled > progressWidth {
			filled = progressWidth
		}
	} else {
		filled = int(activeBytes/(16<<20)) % (progressWidth + 1)
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", progressWidth-filled)
	label := tasks[0].label
	if len(tasks) > 1 {
		label = fmt.Sprintf("%s +%d streams", label, len(tasks)-1)
	}
	if knownTotal && total > 0 {
		_, _ = fmt.Fprintf(d.out, "\r\x1b[2K\x1b[36m[%s]\x1b[0m %s · %s / %s", bar, label, formatProgressBytes(activeBytes), formatProgressBytes(total))
	} else {
		_, _ = fmt.Fprintf(d.out, "\r\x1b[2K\x1b[36m[%s]\x1b[0m %s · %s", bar, label, formatProgressBytes(activeBytes))
	}
	d.lastDraw = time.Now()
}

func (w progressWriter) Write(data []byte) (int, error) {
	n, err := w.dst.Write(data)
	w.task.add(int64(n))
	return n, err
}

func (r progressReader) Read(data []byte) (int, error) {
	n, err := r.src.Read(data)
	r.task.add(int64(n))
	return n, err
}

func openProgressFile(path, label string) (*os.File, *progressTask, io.Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, nil, err
	}
	task := archiveProgress.start(label, info.Size())
	return f, task, progressReader{src: f, task: task}, nil
}

func formatProgressBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	for _, name := range units {
		value /= unit
		if value < unit || name == "TiB" {
			return fmt.Sprintf("%.1f %s", value, name)
		}
	}
	return fmt.Sprintf("%d B", n)
}
