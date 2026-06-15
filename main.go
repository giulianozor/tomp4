package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type streamInfo struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
}

type ffprobeOut struct {
	Streams []streamInfo `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

type fileEntry struct {
	Path     string
	Name     string
	Size     int64
	Status   string
	Speed    string
	Time     string
	Progress string
	Duration float64
}

var (
	currentCmd  *exec.Cmd
	cmdMu       sync.Mutex
	interrupted atomic.Bool
)

var (
	videoExts   = map[string]bool{
		".mp4": true, ".avi": true, ".mkv": true, ".mov": true,
		".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
		".ts": true, ".mts": true, ".m2ts": true, ".3gp": true,
		".ogv": true,
	}
	termWidth int
)

func checkTool(name string) {
	if _, err := exec.LookPath(name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %s not found in PATH (install ffmpeg)\n", name)
		os.Exit(1)
	}
}

func main() {
	checkTool("ffmpeg")
	checkTool("ffprobe")

	source := flag.String("s", ".", "source directory path")
	dest := flag.String("o", "", "output directory path (default: <source>/out)")
	keep := flag.Bool("k", false, "keep source files after processing")
	qsv := flag.Bool("qsv", false, "enable QSV encoder if supported")
	dryRun := flag.Bool("n", false, "dry run: show ffmpeg commands without converting")
	info := flag.Bool("i", false, "show table of sources, codecs, and actions, then exit")
	yes := flag.Bool("y", false, "skip confirmation prompt")
	recursive := flag.Bool("r", false, "recursively scan source directory")
	flag.Parse()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalCh
		signal.Stop(signalCh)
		interrupted.Store(true)

		cmdMu.Lock()
		cmd := currentCmd
		cmdMu.Unlock()

		if cmd != nil && cmd.Process != nil {
			cmd.Process.Signal(syscall.SIGTERM)
			go func(toKill *exec.Cmd) {
				time.Sleep(3 * time.Second)
				if toKill.Process != nil {
					toKill.Process.Kill()
				}
			}(cmd)
		}
	}()

	srcDir, err := filepath.Abs(*source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving source path: %v\n", err)
		os.Exit(1)
	}
	if fi, err := os.Stat(srcDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: source directory does not exist: %v\n", err)
		os.Exit(1)
	} else if !fi.IsDir() {
		fmt.Fprintf(os.Stderr, "error: source path is not a directory: %s\n", srcDir)
		os.Exit(1)
	}

	dstDir := *dest
	if dstDir == "" {
		dstDir = filepath.Join(srcDir, "out")
	}
	dstDir, err = filepath.Abs(dstDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving output path: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output directory: %v\n", err)
		os.Exit(1)
	}

	entries := findVideoFiles(srcDir, dstDir, *recursive)
	if len(entries) == 0 {
		fmt.Println("No video files found.")
		return
	}

	for _, e := range entries {
		e.Duration = probeDuration(e.Path)
	}

	nameWidth := calcNameWidth(len(entries))
	acs := computeActions(entries, dstDir, *qsv)

	if *info {
		printInfoTable(acs, nameWidth)
		return
	}

	if *dryRun {
		printDryRun(acs, nameWidth)
		return
	}

	if !*yes {
		var toConvert, alreadyValid, alreadyExist int
		for _, c := range acs {
			if c.action.skip {
				alreadyValid++
			} else if _, err := os.Stat(c.outPath); err == nil {
				alreadyExist++
			} else {
				toConvert++
			}
		}

		fmt.Println()
		printInfoTable(acs, nameWidth)
		fmt.Printf("Source:      %s\n", srcDir)
		fmt.Printf("Destination: %s\n", dstDir)
		fmt.Printf("Keep:        %t\n", *keep)
		fmt.Printf("QSV:         %t\n", *qsv)
		fmt.Printf("Recursive:   %t\n", *recursive)
		fmt.Printf("Total:       %d\n", len(entries))
		if alreadyValid > 0 {
			fmt.Printf("Valid:       %d\n", alreadyValid)
		}
		if alreadyExist > 0 {
			fmt.Printf("Exists:      %d\n", alreadyExist)
		}
		fmt.Printf("To convert:  %d\n", toConvert)

		if toConvert == 0 {
			fmt.Println("Nothing to convert.")
			return
		}

		fmt.Print("Proceed? [Y/n] ")
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			fmt.Println("Aborted.")
			return
		}
		if response != "y" && response != "Y" && response != "" {
			fmt.Println("Aborted.")
			return
		}
		fmt.Println()
	}

	total := len(entries)
	tableLines := 4 + total

	if useANSI {
		printTable(entries, nameWidth)
	}

	for _, c := range acs {
		if interrupted.Load() {
			break
		}
		processFile(c.entry, c.action, c.outPath, entries, *keep, nameWidth, tableLines)
	}

	if !useANSI {
		printTable(entries, nameWidth)
	}
	fmt.Println()
}

const fixedTableOverhead = 51 // fixed col widths (10+8+10+12) + #/File padding (4) + border chars (7)

func calcNameWidth(total int) int {
	numW := digits(total)*2 + 1
	if termWidth == 0 {
		out, err := exec.Command("tput", "cols").Output()
		w := 80
		if err == nil {
			if v, e := strconv.Atoi(strings.TrimSpace(string(out))); e == nil && v > 0 {
				w = v
			}
		}
		termWidth = w
	}
	w := termWidth
	nw := w - fixedTableOverhead - numW
	if nw < 20 {
		nw = 20
	}
	if nw > 60 {
		nw = 60
	}
	return nw
}

func findVideoFiles(srcDir, dstDir string, recursive bool) []*fileEntry {
	var entries []*fileEntry
	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: error accessing %s: %v\n", path, err)
			return nil
		}
		if info.IsDir() {
			if path == dstDir {
				return filepath.SkipDir
			}
			if !recursive && path != srcDir {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if videoExts[ext] {
			entries = append(entries, &fileEntry{
				Path:   path,
				Name:   info.Name(),
				Size:   info.Size(),
				Status: "Waiting",
			})
		}
		return nil
	}
	if err := filepath.Walk(srcDir, walkFn); err != nil {
		fmt.Fprintf(os.Stderr, "warning: error walking source directory: %v\n", err)
	}
	return entries
}

func probeDuration(path string) float64 {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_format", path)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(p.Format.Duration, 64)
	return d
}

func probeCodecs(path string) (string, []string) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return "", nil
	}
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return "", nil
	}
	var vc string
	var acs []string
	for _, s := range p.Streams {
		if s.CodecType == "video" && vc == "" {
			vc = s.CodecName
		}
		if s.CodecType == "audio" {
			acs = append(acs, s.CodecName)
		}
	}
	return vc, acs
}

type action struct {
	skip      bool
	vcodec    string
	acodec    string
	vcodecArg string
	acodecArg string
}

func getAction(path string, qsv bool) action {
	ext := strings.ToLower(filepath.Ext(path))
	vcodec, acodecs := probeCodecs(path)

	allCopyable := true
	for _, ac := range acodecs {
		if ac != "aac" && ac != "ac3" {
			allCopyable = false
			break
		}
	}

	codecStr := strings.Join(acodecs, ",")
	if ext == ".mp4" && vcodec == "h264" && allCopyable {
		return action{skip: true, vcodec: vcodec, acodec: codecStr}
	}

	a := action{vcodec: vcodec, acodec: codecStr}
	if vcodec == "h264" {
		a.vcodecArg = "copy"
	} else if qsv {
		a.vcodecArg = "h264_qsv"
	} else {
		a.vcodecArg = "libx264"
	}

	if allCopyable {
		a.acodecArg = "copy"
	} else {
		a.acodecArg = "aac"
	}

	return a
}

func actionDesc(a action) string {
	if a.skip {
		return "Skip (already valid)"
	}
	return fmt.Sprintf("v:%s a:%s", a.vcodecArg, a.acodecArg)
}

type actionCache struct {
	entry   *fileEntry
	action  action
	outPath string
}

func computeActions(entries []*fileEntry, dstDir string, qsv bool) []actionCache {
	ac := make([]actionCache, len(entries))
	for i, e := range entries {
		base := filepath.Base(e.Path)
		outName := strings.TrimSuffix(base, filepath.Ext(base)) + ".mp4"
		ac[i] = actionCache{
			entry:   e,
			action:  getAction(e.Path, qsv),
			outPath: filepath.Join(dstDir, outName),
		}
	}
	return ac
}

func actionTable(acs []actionCache, nameWidth int, afterRow func(actionCache)) {
	w := nameWidth
	vcw, acw := 10, 10
	for _, c := range acs {
		if len(c.action.vcodec) > vcw {
			vcw = len(c.action.vcodec)
		}
		if len(c.action.acodec) > acw {
			acw = len(c.action.acodec)
		}
	}
	if vcw < 5 {
		vcw = 5
	}
	if acw < 5 {
		acw = 5
	}

	numW := digits(len(acs))*2 + 1

	top := fmt.Sprintf("┌%s┬%s┬%s┬%s┬%s┐",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", w+2),
		strings.Repeat("─", vcw+2),
		strings.Repeat("─", acw+2),
		strings.Repeat("─", 28),
	)
	mid := fmt.Sprintf("├%s┼%s┼%s┼%s┼%s┤",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", w+2),
		strings.Repeat("─", vcw+2),
		strings.Repeat("─", acw+2),
		strings.Repeat("─", 28),
	)
	bot := fmt.Sprintf("└%s┴%s┴%s┴%s┴%s┘",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", w+2),
		strings.Repeat("─", vcw+2),
		strings.Repeat("─", acw+2),
		strings.Repeat("─", 28),
	)

	hdr := fmt.Sprintf("│ %-*s │ %-*s │ %-*s │ %-*s │ %-26s │",
		numW, "#", w, "File", vcw, "Video", acw, "Audio", "Action")

	fmt.Println(top)
	fmt.Println(hdr)
	fmt.Println(mid)

	for i, c := range acs {
		name := c.entry.Name
		if len(name) > w {
			name = name[:w-3] + "..."
		}
		fmt.Printf("│ %-*s │ %-*s │ %-*s │ %-*s │ %-26s │\n",
			numW, fmt.Sprintf("%d/%d", i+1, len(acs)),
			w, name, vcw, c.action.vcodec, acw, c.action.acodec, actionDesc(c.action))
		if afterRow != nil {
			afterRow(c)
		}
	}

	fmt.Println(bot)
}

func printInfoTable(acs []actionCache, nameWidth int) {
	actionTable(acs, nameWidth, nil)
}

func digits(n int) int {
	if n < 10 {
		return 1
	}
	if n < 100 {
		return 2
	}
	if n < 1000 {
		return 3
	}
	if n < 10000 {
		return 4
	}
	return 5
}

func printDryRun(acs []actionCache, nameWidth int) {
	actionTable(acs, nameWidth, func(c actionCache) {
		if !c.action.skip {
			args := []string{"-i", c.entry.Path,
				"-map", "0:v?", "-map", "0:a?", "-map", "0:s?",
				"-c:v", c.action.vcodecArg, "-c:a", c.action.acodecArg,
				"-c:s", "copy",
				"-map_metadata", "0", "-map_chapters", "0",
				"-progress", "pipe:1", "-nostats", "-y", c.outPath}
			fmt.Printf("  ffmpeg %s\n", strings.Join(args, " "))
		} else {
			fmt.Println("  (no conversion needed)")
		}
	})
}

func processFile(e *fileEntry, a action, outPath string, entries []*fileEntry, keep bool, nameWidth, tableLines int) {
	if a.skip {
		e.Status = "Skipped"
		e.Speed = "  --  "
		e.Time = "  --  "
		e.Progress = "  --  "
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		if !keep {
			if err := os.Remove(e.Path); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", e.Path, err)
			}
		}
		return
	}

	if _, err := os.Stat(outPath); err == nil {
		e.Status = "Exists"
		e.Speed = "  --  "
		e.Time = "  --  "
		e.Progress = "  --  "
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}

	if interrupted.Load() {
		e.Status = "Aborted"
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}

	args := []string{"-i", e.Path,
		"-map", "0:v?", "-map", "0:a?", "-map", "0:s?",
		"-c:v", a.vcodecArg, "-c:a", a.acodecArg,
		"-c:s", "copy",
		"-map_metadata", "0", "-map_chapters", "0",
		"-progress", "pipe:1", "-nostats", "-y", outPath}

	cmd := exec.Command("ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.Status = "Failed"
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		e.Status = "Failed"
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}

	cmdMu.Lock()
	currentCmd = cmd
	cmdMu.Unlock()

	if err := cmd.Start(); err != nil {
		cmdMu.Lock()
		currentCmd = nil
		cmdMu.Unlock()
		stdout.Close()
		stderr.Close()
		e.Status = "Failed"
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}

	e.Status = "Encoding"
	e.Speed = "  --  "
	e.Time = "  --  "
	e.Progress = "  0%  "
	updateTable(entries, nameWidth, tableLines)

	startTime := time.Now()
	lastUpdate := time.Now()

	progCh := make(chan map[string]string, 64)
	go func() {
		defer close(progCh)
		scanner := bufio.NewScanner(stdout)
		vals := make(map[string]string)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if idx := strings.IndexByte(line, '='); idx >= 0 {
				key := line[:idx]
				val := line[idx+1:]
				vals[key] = val
				if key == "progress" {
					cp := make(map[string]string, len(vals))
					for k, v := range vals {
						cp[k] = v
					}
					progCh <- cp
					vals = make(map[string]string)
				}
			}
		}
	}()

	go io.Copy(io.Discard, stderr)

	for vals := range progCh {
		if time.Since(lastUpdate) < 200*time.Millisecond {
			continue
		}
		lastUpdate = time.Now()

		outTimeUs, _ := strconv.ParseInt(vals["out_time_us"], 10, 64)
		outSecs := float64(outTimeUs) / 1e6
		elapsed := time.Since(startTime)

		var pct float64
		if e.Duration > 0 {
			pct = outSecs / e.Duration * 100
		} else {
			pct = outSecs
		}
		if pct > 100 {
			pct = 100
		}

		e.Progress = fmt.Sprintf("%3.0f%%", pct)
		e.Time = fmtDuration(outSecs)

		if outSecs > 0 && elapsed.Seconds() > 0 {
			speed := outSecs / elapsed.Seconds()
			switch {
			case speed >= 100:
				e.Speed = fmt.Sprintf("%3.0fx ", speed)
			default:
				e.Speed = fmt.Sprintf("%5.2fx", speed)
			}
		}

		updateTable(entries, nameWidth, tableLines)
	}

	cmd.Wait()

	cmdMu.Lock()
	currentCmd = nil
	cmdMu.Unlock()

	if cmd.ProcessState.Success() {
		e.Status = "Done"
		e.Time = fmtDuration(e.Duration)
		e.Progress = "100%"
		e.Speed = "  --  "
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)

		if !keep {
			if err := os.Remove(e.Path); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove %s: %v\n", e.Path, err)
			}
		}
		return
	}

	if interrupted.Load() {
		if _, err := os.Stat(outPath); err == nil {
			if err := os.Remove(outPath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to remove partial output %s: %v\n", outPath, err)
			}
		}
		e.Status = "Killed"
		e.Progress = " ERR "
		e.Speed = "  --  "
		e.Time = "  --  "
		updateTable(entries, nameWidth, tableLines)
		printFileStatus(e)
		return
	}

	e.Status = "Failed"
	e.Progress = " ERR "
	e.Speed = "  --  "
	e.Time = "  --  "
	updateTable(entries, nameWidth, tableLines)
	printFileStatus(e)
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

var useANSI = isTerminal()

func updateTable(entries []*fileEntry, nameWidth, tableLines int) {
	if !useANSI {
		return
	}
	fmt.Printf("\033[%dA\033[J", tableLines)
	printTable(entries, nameWidth)
}

func printFileStatus(e *fileEntry) {
	if useANSI {
		return
	}
	pct := e.Progress
	if pct == "" {
		pct = "  --  "
	}
	fmt.Printf("%s: %s (%s)\n", e.Name, e.Status, pct)
}

func printTable(entries []*fileEntry, nameWidth int) {
	total := len(entries)

	numW := digits(len(entries))*2 + 1

	top := fmt.Sprintf("┌%s┬%s┬%s┬%s┬%s┬%s┐",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", nameWidth+2),
		strings.Repeat("─", 10),
		strings.Repeat("─", 8),
		strings.Repeat("─", 10),
		strings.Repeat("─", 12),
	)
	mid := fmt.Sprintf("├%s┼%s┼%s┼%s┼%s┼%s┤",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", nameWidth+2),
		strings.Repeat("─", 10),
		strings.Repeat("─", 8),
		strings.Repeat("─", 10),
		strings.Repeat("─", 12),
	)
	bot := fmt.Sprintf("└%s┴%s┴%s┴%s┴%s┴%s┘",
		strings.Repeat("─", numW+2),
		strings.Repeat("─", nameWidth+2),
		strings.Repeat("─", 10),
		strings.Repeat("─", 8),
		strings.Repeat("─", 10),
		strings.Repeat("─", 12),
	)

	hdr := fmt.Sprintf("│ %-*s │ %-*s │ %-8s │ %-6s │ %-8s │ %-10s │",
		numW, "#", nameWidth, "File", "Size", "Speed", "Time", "Status")

	fmt.Println(top)
	fmt.Println(hdr)
	fmt.Println(mid)

	for i, e := range entries {
		name := e.Name
		if len(name) > nameWidth {
			name = name[:nameWidth-3] + "..."
		}
		idx := fmt.Sprintf("%d/%d", i+1, total)
		speed := e.Speed
		if speed == "" {
			speed = "  --  "
		}
		tm := e.Time
		if tm == "" {
			tm = "  --  "
		}
		prog := e.Progress
		if prog == "" {
			prog = "  --  "
		}
		disp := e.Status
		if e.Status == "Encoding" {
			disp = prog
		}

		fmt.Printf("│ %-*s │ %-*s │ %-8s │ %-6s │ %-8s │ %-10s │\n",
			numW, idx, nameWidth, name,
			formatSize(e.Size), speed, tm, disp,
		)
	}

	fmt.Println(bot)
}

func formatSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1024*1024*1024))
	}
}

func fmtDuration(seconds float64) string {
	if seconds <= 0 {
		return "  --  "
	}
	d := time.Duration(math.Round(seconds)) * time.Second
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
