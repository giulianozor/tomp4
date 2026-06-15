# tomp4

Batch convert video files to MP4 (H.264/AAC) using ffmpeg.

## Features

- Scans a directory for video files (mp4, avi, mkv, mov, wmv, flv, webm, m4v, ts, mts, m2ts, 3gp, ogv) — optionally recursive with `-r`
- Skips files already in valid MP4/H.264 format
- Preserves compatible audio (AAC, AC3) and subtitle streams
- Re-encodes incompatible audio to AAC
- Real-time progress table with per-file status
- Dry-run mode to preview commands
- Info mode to show codecs and actions without converting
- QSV hardware acceleration support
- Graceful interrupt handling (SIGINT/SIGTERM)

## Requirements

- [ffmpeg](https://ffmpeg.org/) (with ffprobe)
- Go 1.21+ (for building from source)

## Installation

```sh
go install github.com/giulianozor/tomp4@latest
```

Or build locally:

```sh
make build
```

## Usage

```
tomp4 [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-s` | `.` | Source directory path |
| `-o` | `<source>/out` | Output directory path |
| `-k` | `false` | Keep source files after processing |
| `-qsv` | `false` | Enable QSV (Quick Sync Video) encoder |
| `-n` | `false` | Dry run: show ffmpeg commands without converting |
| `-i` | `false` | Show table of sources, codecs, and actions, then exit |
| `-r` | `false` | Recursively scan subdirectories | 
| `-y` | `false` | Skip confirmation prompt |

### Examples

```sh
# Convert all videos in current directory
tomp4

# Scan a specific directory, output to ./out
tomp4 -s /path/to/videos -o /path/to/output

# Preview what would be done
tomp4 -n

# Show codec info for all files
tomp4 -i

# Use QSV hardware encoding, keep originals
tomp4 -qsv -k

# Recursively scan subdirectories
tomp4 -r
```

## How it works

For each video file found:
1. **ffprobe** detects video codec, audio codecs, and duration
2. If the file is already MP4 with H.264 video and compatible audio (AAC/AC3), it is **skipped**
3. Otherwise, **ffmpeg** converts the file:
   - Video: H.264 (`libx264` or `h264_qsv`), or stream-copied if already H.264
   - Audio: AAC, or stream-copied if already AAC/AC3
   - Subtitles: stream-copied
   - Metadata and chapters are preserved
4. On success, the original is removed (unless `-k` is set)
