package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

const (
	// progressRedrawInterval is the time between 2 redraws of the progress line in a terminal.
	progressRedrawInterval = 250 * time.Millisecond
	// progressLineInterval is the time between 2 progress lines in a file or a pipe, so that a log gets few lines.
	progressLineInterval = 10 * time.Second
	// progressBarWidth is the count of the cells of the bar. The full line must stay shorter than 80 characters,
	// because a terminal puts a longer line on 2 rows, and then a redraw cannot replace it.
	progressBarWidth = 18
	// progressEstimateDelay is the time before the progress shows the speed and the time to the end.
	// The first data of a download comes in bursts, so an earlier estimate jumps and can show hours.
	progressEstimateDelay = 2 * time.Second
	// maxShownTimeLeft is the largest time to the end that the progress shows. A larger value tells nothing more.
	maxShownTimeLeft = 99*time.Hour + 59*time.Minute
	// clearLineEnd removes the old text after the cursor, because a new progress line can be shorter.
	clearLineEnd = "\x1b[K"
)

// progressOutput is 1 place for the progress of a download. A terminal gets 1 line that changes in place.
// A file or a pipe gets a new line every progressLineInterval.
type progressOutput struct {
	writer     io.Writer
	lang       language
	isTerminal bool
}

// downloadProgress shows the received size, the speed and the time to the end of a download.
// Only the goroutine of the download uses it.
type downloadProgress struct {
	outputs  []progressOutput
	now      func() time.Time
	start    time.Time
	lastDraw time.Time
	lastLine time.Time
	received int64
	// total is the size from Content-Length. It is -1 if the server did not send the size.
	total int64
	// isDrawn tells if a terminal shows a progress line that the final line must replace.
	isDrawn bool
}

func newDownloadProgress(now func() time.Time, outputs ...progressOutput) *downloadProgress {
	return &downloadProgress{outputs: outputs, now: now, total: -1}
}

// isTerminalWriter tells if the writer is a terminal. Only a terminal can show a line that changes in place.
func isTerminalWriter(writer io.Writer) bool {
	file, isFile := writer.(*os.File)
	return isFile && isatty.IsTerminal(file.Fd())
}

// begin starts the count. total is -1 if the size is not known.
func (p *downloadProgress) begin(total int64) {
	p.start = p.now()
	p.lastDraw, p.lastLine = p.start, p.start
	p.received, p.total, p.isDrawn = 0, total, false
}

// add counts the received bytes. It redraws the terminal line and writes the other lines when their interval is over.
func (p *downloadProgress) add(count int) {
	p.received += int64(count)
	now := p.now()
	isDraw := now.Sub(p.lastDraw) >= progressRedrawInterval
	isLine := now.Sub(p.lastLine) >= progressLineInterval
	if isDraw {
		p.lastDraw = now
	}
	if isLine {
		p.lastLine = now
	}
	for _, output := range p.outputs {
		switch {
		case output.isTerminal && isDraw:
			fmt.Fprint(output.writer, "\r"+renderBar(p.received, p.total)+p.describe(output.lang, now)+clearLineEnd)
			p.isDrawn = true
		case !output.isTerminal && isLine:
			fmt.Fprintln(output.writer, p.describe(output.lang, now))
		}
	}
}

// end writes the final line with the size and the time of the download. After an error, the terminal keeps
// the last progress line, and the error comes on the next line.
func (p *downloadProgress) end(err error) {
	elapsed := p.now().Sub(p.start)
	for _, output := range p.outputs {
		if output.isTerminal && p.isDrawn {
			if err != nil {
				fmt.Fprintln(output.writer)
				continue
			}
			fmt.Fprint(output.writer, "\r"+clearLineEnd)
		}
		if err == nil {
			fmt.Fprintln(output.writer, textIn(output.lang, msgUpdateDownloaded, formatSize(output.lang, p.received), formatDuration(output.lang, elapsed)))
		}
	}
}

// describe returns the received size, the percent, the speed and the time to the end.
// Without the total size, it returns only the received size and the speed.
func (p *downloadProgress) describe(lang language, now time.Time) string {
	received := formatSize(lang, p.received)
	elapsed := now.Sub(p.start)
	speed := 0.0
	if elapsed > 0 {
		speed = float64(p.received) / elapsed.Seconds()
	}
	rate := textIn(lang, msgSpeed, formatSize(lang, int64(speed)))
	if p.total <= 0 {
		return received + " · " + rate
	}
	percent := min(int(p.received*100/p.total), 100)
	if speed <= 0 || elapsed < progressEstimateDelay {
		return textIn(lang, msgDownloadProgressStart, received, formatSize(lang, p.total), percent)
	}
	left := time.Duration(float64(max(p.total-p.received, 0)) / speed * float64(time.Second))
	return textIn(lang, msgDownloadProgress, received, formatSize(lang, p.total), percent, rate, formatDuration(lang, min(left, maxShownTimeLeft)))
}

// renderBar returns the bar of the percent and a space. Without the total size, it returns nothing.
func renderBar(received, total int64) string {
	if total <= 0 {
		return ""
	}
	filled := min(int(received*progressBarWidth/total), progressBarWidth)
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", progressBarWidth-filled) + "] "
}

// formatSize returns a size in KB below 1 MB and in MB with 1 decimal from 1 MB. The units are decimal, the same as in Finder.
// Russian uses a decimal comma.
func formatSize(lang language, size int64) string {
	if size < 1_000_000 {
		return textIn(lang, msgSizeKB, size/1000)
	}
	value := strconv.FormatFloat(float64(size)/1_000_000, 'f', 1, 64)
	if lang == languageRussian {
		value = strings.Replace(value, ".", ",", 1)
	}
	return textIn(lang, msgSizeMB, value)
}

// formatDuration returns a time in seconds below 1 minute, in minutes and seconds below 1 hour, and in hours and minutes after.
func formatDuration(lang language, duration time.Duration) string {
	seconds := max(int(duration.Round(time.Second)/time.Second), 0)
	switch {
	case seconds < 60:
		return textIn(lang, msgDurationSeconds, seconds)
	case seconds < 3600:
		return textIn(lang, msgDurationMinutes, seconds/60, seconds%60)
	}
	return textIn(lang, msgDurationHours, seconds/3600, seconds%3600/60)
}

// updateLog adds lines with the time to update.log. The automatic update writes the same file.
// A nil updateLog writes nothing, so an update without the file still works.
type updateLog struct {
	file *os.File
	now  func() time.Time
	// isLineStart tells if the next byte starts a new line, which gets the time.
	isLineStart bool
}

// openUpdateLog opens the log for new lines at its end. An error gives nil, because the update must not stop for the log.
func openUpdateLog(path string, now func() time.Time) *updateLog {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return &updateLog{file: file, now: now, isLineStart: true}
}

// Write puts the time in front of each line. The progress of the download writes its lines through it.
func (l *updateLog) Write(data []byte) (int, error) {
	var buffer strings.Builder
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}
		if l.isLineStart {
			buffer.WriteString(l.now().Format(debugTimeFormat) + " ")
		}
		buffer.WriteString(line)
		l.isLineStart = strings.HasSuffix(line, "\n")
	}
	if _, err := l.file.WriteString(buffer.String()); err != nil {
		return 0, err
	}
	return len(data), nil
}

// writeLine adds 1 line. The log is always in English, so the callers give the English text.
func (l *updateLog) writeLine(line string) {
	if l != nil {
		fmt.Fprintln(l, line)
	}
}

func (l *updateLog) Close() {
	if l != nil {
		_ = l.file.Close()
	}
}
