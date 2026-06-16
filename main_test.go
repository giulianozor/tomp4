package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDigits(t *testing.T) {
	tests := []struct {
		n    int
		want int
	}{
		{0, 1},
		{1, 1},
		{9, 1},
		{10, 2},
		{99, 2},
		{100, 3},
		{999, 3},
		{1000, 4},
		{9999, 4},
		{10000, 5},
	}
	for _, tt := range tests {
		got := digits(tt.n)
		if got != tt.want {
			t.Errorf("digits(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestActionDesc(t *testing.T) {
	tests := []struct {
		name string
		a    action
		want string
	}{
		{"skip", action{skip: true}, "Skip (already valid)"},
		{"copy both", action{vcodecArg: "copy", acodecArg: "copy"}, "v:copy a:copy"},
		{"encode both", action{vcodecArg: "libx264", acodecArg: "aac"}, "v:libx264 a:aac"},
		{"qsv", action{vcodecArg: "h264_qsv", acodecArg: "copy"}, "v:h264_qsv a:copy"},
		{"empty", action{}, "v: a:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionDesc(tt.a)
			if got != tt.want {
				t.Errorf("actionDesc(%+v) = %q, want %q", tt.a, got, tt.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1610612736, "1.5 GB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.n)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		sec  float64
		want string
	}{
		{0, "  --  "},
		{-1, "  --  "},
		{30, "00:30"},
		{90, "01:30"},
		{3600, "01:00:00"},
		{3661, "01:01:01"},
		{86400, "24:00:00"},
		{1.999, "00:02"},
		{59.4, "00:59"},
		{59.5, "01:00"},
	}
	for _, tt := range tests {
		got := fmtDuration(tt.sec)
		if got != tt.want {
			t.Errorf("fmtDuration(%f) = %q, want %q", tt.sec, got, tt.want)
		}
	}
}

func TestFindVideoFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(srcDir, "out")

	files := []string{
		"test.mp4",
		"test.avi",
		"test.mkv",
		"test.mov",
		"test.txt",
		"test.jpg",
		"nested/video.mkv",
		"nested/deep/clip.ts",
	}
	for _, f := range files {
		path := filepath.Join(srcDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// zero-byte video file should be skipped
	emptyPath := filepath.Join(srcDir, "empty.mp4")
	if err := os.WriteFile(emptyPath, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	// also create a file inside the dst dir that should be excluded
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "excluded.mp4"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := findVideoFiles(srcDir, dstDir, true)

	got := make(map[string]bool)
	for _, e := range entries {
		got[e.Name] = true
	}

	expected := []string{"test.mp4", "test.avi", "test.mkv", "test.mov", "video.mkv", "clip.ts"}
	for _, name := range expected {
		if !got[name] {
			t.Errorf("expected %q in results, but not found", name)
		}
	}

	if got["test.txt"] {
		t.Error("test.txt should not be in results")
	}
	if got["test.jpg"] {
		t.Error("test.jpg should not be in results")
	}
	if got["excluded.mp4"] {
		t.Error("excluded.mp4 from dst dir should not be in results")
	}
	if got["empty.mp4"] {
		t.Error("empty.mp4 (0-byte) should not be in results")
	}

	for _, e := range entries {
		if e.Size == 0 {
			t.Errorf("entry %q has size 0, should have been skipped", e.Name)
		}
	}

	if len(entries) != len(expected) {
		t.Errorf("got %d entries, want %d", len(entries), len(expected))
	}
}

func TestFindVideoFilesNoVideoFiles(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(srcDir, "out")

	if err := os.WriteFile(filepath.Join(srcDir, "readme.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := findVideoFiles(srcDir, dstDir, false)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestFindVideoFilesExcludesDstDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(srcDir, "out")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "movie.mkv"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "movie.mp4"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := findVideoFiles(srcDir, dstDir, true)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "movie.mkv" {
		t.Errorf("expected movie.mkv, got %s", entries[0].Name)
	}
}

func TestFindVideoFilesNonRecursive(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(srcDir, "out")

	files := []string{
		"top.mp4",
		"nested/video.mkv",
	}
	for _, f := range files {
		path := filepath.Join(srcDir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fake"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	entries := findVideoFiles(srcDir, dstDir, false)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (only top-level), got %d", len(entries))
	}
	if entries[0].Name != "top.mp4" {
		t.Errorf("expected top.mp4, got %s", entries[0].Name)
	}
}

func TestFindVideoFilesNonRecursiveExcludesDstDir(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := filepath.Join(srcDir, "out")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(srcDir, "movie.mkv"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "movie.mp4"), []byte("fake"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := findVideoFiles(srcDir, dstDir, false)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "movie.mkv" {
		t.Errorf("expected movie.mkv, got %s", entries[0].Name)
	}
}

func TestVideoExts(t *testing.T) {
	expectedExts := []string{".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".ts", ".mts", ".m2ts", ".3gp", ".ogv"}
	for _, ext := range expectedExts {
		if !videoExts[ext] {
			t.Errorf("expected %q to be in videoExts", ext)
		}
	}

	nonVideoExts := []string{".txt", ".jpg", ".mp3", ".pdf", ".go", ""}
	for _, ext := range nonVideoExts {
		if videoExts[ext] {
			t.Errorf("did not expect %q to be in videoExts", ext)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	result := isTerminal()
	if result {
		t.Log("running in a terminal")
	} else {
		t.Log("not running in a terminal")
	}
}

func TestUseANSI(t *testing.T) {
	if useANSI {
		t.Log("ANSI escape codes enabled")
	} else {
		t.Log("ANSI escape codes disabled")
	}
}
