// photo-organize organizes photos and videos by capture date with
// content-hash naming. It shells out to exiftool for metadata extraction
// and uses Go for all hashing, path building, and file operations.
//
// Usage:
//
//	photo-organize --src DIR --dest DIR [--apply] [--move] [--keep-names] [--log FILE]
//
// Without --apply the tool runs in dry-run mode: it logs what it would do
// but copies or moves nothing.
//
// Output layout:
//
//	<dest>/<YYYY>/<MM>/<YYYY-MM-DD>-<HHMMSS>-<sha1[12]>.<ext>
//	<dest>/unknown/<sha1[12]>.<ext>   (when no date is recoverable)
//
// With --keep-names the original filename is preserved instead of the
// date+hash name. Two different files sharing an original name in the
// same month bucket collide: the second overwrites the first and is
// logged as dup-recopy for human review.
//
// Install exiftool:
//
//	pacman -S perl-image-exiftool      (Arch)
//	apt install libimage-exiftool-perl (Debian/TrueNAS)
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// exiftoolDateFormat is the strftime-style format passed to exiftool's -d flag.
// exiftool uses C/strftime %Y%m%d tokens, not Go's reference-time layout.
const exiftoolDateFormat = "%Y:%m:%d_%H:%M:%S"

// dateLayout is the Go time.Parse layout matching exiftoolDateFormat output.
const dateLayout = "2006:01:02_15:04:05"

// hashLen is the number of hex characters retained from the sha1 digest.
// 12 hex chars = 48 bits of entropy, collision-safe for personal photo libraries.
const hashLen = 12

// Processing status values written to the log.
const (
	statusCopied       = "copied"
	statusNoDate       = "no-date"
	statusSkippedDup   = "skipped-dup"
	statusRecopy       = "recopy"
	statusDupRecopy    = "dup-recopy"
	statusNoDateRecopy = "no-date-recopy"
	statusError        = "error"
)

// dateFallbackChain defines the exiftool tag columns emitted in TSV order.
// The first non-"-" value wins. Order matters: embedded capture dates first,
// filesystem timestamps as last resort.
var dateFallbackChain = []string{
	"DateTimeOriginal",     // EXIF capture date (cameras, phones)
	"CreateDate",           // EXIF/MOV creation date
	"IFD0:ModifyDate",      // TIFF/Photoshop modify date (e.g. scanned prints)
	"XMP:ModifyDate",       // XMP modify date
	"XMP:DateTimeOriginal", // XMP capture date (Lightroom edits)
	"TrackCreateDate",      // video track creation date
	"QuickTime:CreateDate", // QuickTime container creation date (corrected by -api QuickTimeUTC)
	"FileModifyDate",       // filesystem mtime (last resort)
	"FileCreateDate",       // filesystem ctime (last resort)
}

// junkExts are file extensions (lowercase, without dot) to skip entirely.
var junkExts = map[string]bool{
	"ds_store": true,
	"aae":      true,
	"xmp":      true,
	"thm":      true,
}

// junkBases are exact filenames to skip entirely.
// Note: dotfiles are already caught by the HasPrefix check in isJunk,
// so this only needs non-dotfile entries.
var junkBases = map[string]bool{
	"Thumbs.db": true,
	"thumbs.db": true,
}

// config holds the resolved CLI flags.
type config struct {
	src       string
	dest      string
	apply     bool
	move      bool
	quiet     bool
	keepNames bool
	logPath   string
}

// stats tracks the outcome of processing each file. Each status has its
// own counter; no shared increments.
type stats struct {
	copied       int
	noDate       int
	skippedDup   int
	recopy       int
	dupRecopy    int
	noDateRecopy int
	errors       int
}

// fileRecord holds the parsed exiftool output for a single source file.
type fileRecord struct {
	src     string // path as reported by exiftool (Directory/FileName)
	date    string // resolved date in exiftoolDateFormat, or ""
	dateTag string // which tag supplied the date, or "" if none
	ext     string // normalized lowercase extension without dot
}

func main() {
	cfg := parseFlags()

	info, err := os.Stat(cfg.src)
	if err != nil || !info.IsDir() {
		fatalf("source directory not found: %s", cfg.src)
	}

	logFile, err := os.Create(cfg.logPath)
	if err != nil {
		fatalf("cannot create log file %s: %v", cfg.logPath, err)
	}
	defer logFile.Close()

	scanner, waitFn, err := startExiftool(cfg.src)
	if err != nil {
		fatalf("exiftool failed: %v", err)
	}

	st := process(cfg, scanner, logFile)
	if err := waitFn(); err != nil {
		fatalf("exiftool exited with error: %v", err)
	}

	printSummary(cfg, st)
}

// parseFlags reads CLI flags and returns a populated config.
// --src and --dest are required; --log defaults to organize.log.tsv.
func parseFlags() config {
	src := flag.String("src", "", "source directory (required)")
	dest := flag.String("dest", "", "destination directory (required)")
	apply := flag.Bool("apply", false, "copy/move files (default: dry-run)")
	move := flag.Bool("move", false, "move instead of copy")
	quiet := flag.Bool("quiet", false, "suppress per-file progress output")
	keepNames := flag.Bool("keep-names", false, "preserve original filenames instead of <date>-<hash>.<ext>")
	logPath := flag.String("log", "organize.log.tsv", "log file path")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "photo-organize: organize photos by capture date\n\n")
		fmt.Fprintf(out, "Usage: %s --src DIR --dest DIR [--apply] [--move] [--keep-names] [--log FILE]\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *src == "" || *dest == "" {
		flag.Usage()
		os.Exit(2)
	}

	return config{
		src:       *src,
		dest:      *dest,
		apply:     *apply,
		move:      *move,
		quiet:     *quiet,
		keepNames: *keepNames,
		logPath:   *logPath,
	}
}

// process streams exiftool output line-by-line, hashing and copying/moving
// each file as its metadata arrives. Every action is logged to logFile.
func process(cfg config, scanner *bufio.Scanner, logFile *os.File) stats {
	w := bufio.NewWriter(logFile)
	defer w.Flush()
	fmt.Fprintln(w, "status\tsrc\tdest\tdate\tdateSourceTag\thash")

	var st stats
	seen := make(map[string]bool) // dest path -> seen this run

	for scanner.Scan() {
		rec, ok := parseExifLine(scanner.Text())
		if !ok {
			continue
		}
		if isJunk(rec.src, rec.ext) {
			continue
		}

		// Hashing is skipped in --keep-names mode: the original filename is
		// used as-is and dedup is purely path/size based.
		var hash string
		if !cfg.keepNames {
			h, err := contentHash(rec.src)
			if err != nil {
				st.errors++
				fmt.Fprintf(w, "%s\t%s\t\t\t\t\t%v\n", statusError, rec.src, err)
				if !cfg.quiet {
					fmt.Fprintf(os.Stderr, "error  %s: %v\n", rec.src, err)
				}
				continue
			}
			hash = h
		}

		dest, baseStatus := planDestination(cfg.dest, rec, hash, cfg.keepNames)

		withinRunDup := seen[dest]
		seen[dest] = true

		recopyNeeded, err := sizesDiffer(rec.src, dest)
		if err != nil {
			st.errors++
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%v\n", statusError, rec.src, dest, rec.date, rec.dateTag, hash, err)
			if !cfg.quiet {
				fmt.Fprintf(os.Stderr, "error  %s -> %s: %v\n", rec.src, dest, err)
			}
			continue
		}

		// Decide the final status from the 7-row table.
		status := baseStatus // "copied" or "no-date"
		switch {
		case withinRunDup && recopyNeeded:
			status = statusDupRecopy
		case withinRunDup && !recopyNeeded:
			status = statusSkippedDup
		case !withinRunDup && recopyNeeded && baseStatus == statusCopied:
			status = statusRecopy
		case !withinRunDup && recopyNeeded && baseStatus == statusNoDate:
			status = statusNoDateRecopy
		}

		// Tally the per-status counter.
		switch status {
		case statusCopied:
			st.copied++
		case statusNoDate:
			st.noDate++
		case statusSkippedDup:
			st.skippedDup++
		case statusRecopy:
			st.recopy++
		case statusDupRecopy:
			st.dupRecopy++
		case statusNoDateRecopy:
			st.noDateRecopy++
		}

		// skipped-dup places nothing; everything else (when --apply) does.
		if status != statusSkippedDup && cfg.apply {
			if err := placeFile(cfg, rec.src, dest); err != nil {
				// Replace the planned status with error and retally.
				undoStatus(&st, status)
				st.errors++
				slog.Error("file operation failed", "src", rec.src, "dest", dest, "err", err)
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%v\n", statusError, rec.src, dest, rec.date, rec.dateTag, hash, err)
				if !cfg.quiet {
					fmt.Fprintf(os.Stderr, "error  %s -> %s: %v\n", rec.src, dest, err)
				}
				continue
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", status, rec.src, dest, rec.date, rec.dateTag, hash)
		if !cfg.quiet {
			prefix := statusPrefix(status, cfg.apply)
			fmt.Fprintf(os.Stderr, "%s  %s -> %s\n", prefix, rec.src, dest)
		}
	}

	if err := scanner.Err(); err != nil {
		fatalf("read exiftool output: %v", err)
	}

	return st
}

// undoStatus decrements the counter for a previously-tallied status when a
// later file-operation failure reclassifies the row as an error.
func undoStatus(st *stats, status string) {
	switch status {
	case statusCopied:
		st.copied--
	case statusNoDate:
		st.noDate--
	case statusSkippedDup:
		st.skippedDup--
	case statusRecopy:
		st.recopy--
	case statusDupRecopy:
		st.dupRecopy--
	case statusNoDateRecopy:
		st.noDateRecopy--
	}
}

// statusPrefix maps a status to the short stderr progress label.
func statusPrefix(status string, apply bool) string {
	if !apply {
		return "dry   "
	}
	switch status {
	case statusCopied:
		return "copy  "
	case statusNoDate:
		return "nodate"
	case statusSkippedDup:
		return "dup   "
	case statusRecopy:
		return "recopy"
	case statusDupRecopy:
		return "duprec"
	case statusNoDateRecopy:
		return "ndrecp"
	default:
		return "error "
	}
}

// planDestination computes the target path and status for a record.
// A valid date yields a path under <dest>/<YYYY>/<MM>/; missing or
// unparseable dates are bucketed under <dest>/unknown/.
//
// When keepNames is set, the original filename is preserved as-is instead of
// the default <date>-<hash>.<ext> name. Two different files sharing an
// original name in the same month bucket collide: the second is logged as
// dup-recopy and overwrites the first (human reviews the log).
func planDestination(dest string, rec fileRecord, hash string, keepNames bool) (path, status string) {
	parsed, err := time.Parse(dateLayout, rec.date)
	if err != nil {
		if rec.date != "" {
			slog.Warn("unparseable date, bucketing as unknown", "src", rec.src, "date", rec.date, "err", err)
		}
		name := hash + "." + rec.ext
		if keepNames {
			name = filepath.Base(rec.src)
		}
		return filepath.Join(dest, "unknown", name), statusNoDate
	}

	var name string
	if keepNames {
		name = filepath.Base(rec.src)
	} else {
		name = fmt.Sprintf(
			"%s-%s.%s",
			parsed.Format("2006-01-02-150405"),
			hash, rec.ext,
		)
	}
	full := filepath.Join(dest, parsed.Format("2006"), parsed.Format("01"), name)
	return full, statusCopied
}

// placeFile creates the target directory and copies or moves the source file.
func placeFile(cfg config, src, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if cfg.move {
		return os.Rename(src, dest)
	}
	return copyNoClobber(src, dest)
}

// startExiftool launches exiftool and returns a scanner over its TSV output
// and a function to wait for its exit. exiftool's -f flag makes missing tags
// print as "-" instead of suppressing the line, so column count is stable.
func startExiftool(src string) (*bufio.Scanner, func() error, error) {
	cmd := exec.Command(
		"exiftool",
		"-q", "-q",
		"-f",
		"-p", exiftoolFormat(),
		"-api", "QuickTimeUTC",
		"-d", exiftoolDateFormat,
		"-charset", "filename=utf8",
		src,
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("pipe exiftool stdout: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start exiftool: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	waitFn := func() error { return cmd.Wait() }

	return scanner, waitFn, nil
}

// parseExifLine parses one TSV line from exiftool's -p output.
// Expected columns: path, 9 date columns, ext (11 total).
// Returns ok=false for lines that don't match the expected format.
func parseExifLine(line string) (fileRecord, bool) {
	cols := strings.Split(line, "\t")
	if len(cols) != len(dateFallbackChain)+2 {
		return fileRecord{}, false
	}

	rec := fileRecord{src: cols[0], ext: cols[len(cols)-1]}
	for i, tag := range dateFallbackChain {
		val := cols[i+1]
		if val == "" || val == "-" {
			continue
		}
		rec.date = val
		rec.dateTag = tag
		break
	}
	return rec, true
}

// exiftoolFormat builds the -p format string: a single TSV line per file
// with the path, each date tag in fallback-chain order, and the extension.
func exiftoolFormat() string {
	var b strings.Builder
	b.WriteString("${Directory}/${FileName}")
	for _, tag := range dateFallbackChain {
		b.WriteString("\t${")
		b.WriteString(tag)
		b.WriteString("}")
	}
	b.WriteString("\t${FileTypeExtension}\n")
	return b.String()
}

// contentHash returns the first hashLen hex characters of the file's sha1
// digest. The hash is content-based: identical files always hash identically,
// enabling deduplication across source directories.
func contentHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))[:hashLen], nil
}

// sizesDiffer reports whether dest exists and has a different size than src.
// Returns false (no recopy needed) when dest does not exist or when sizes match.
// A size mismatch indicates either a truncated leftover from an aborted copy
// or — under --keep-names — a genuinely different file sharing the dest name.
func sizesDiffer(src, dest string) (bool, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false, fmt.Errorf("stat source: %w", err)
	}
	dstInfo, err := os.Stat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // dest missing — fresh placement, no recopy
		}
		return false, fmt.Errorf("stat dest: %w", err)
	}
	return dstInfo.Size() != srcInfo.Size(), nil
}

// copyNoClobber copies src to dst. If dst already exists with the same size
// (complete file from a prior run), it is silently skipped. A size mismatch
// indicates a truncated file from an aborted copy — the dest is removed and
// re-copied. The initial open uses O_EXCL for atomicity on fresh files.
func copyNoClobber(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer in.Close()
	srcInfo, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create dest: %w", err)
		}
		// dest exists — check if it's a complete copy or a truncated leftover
		dstInfo, err := os.Stat(dst)
		if err != nil {
			return fmt.Errorf("stat dest: %w", err)
		}
		if dstInfo.Size() == srcInfo.Size() {
			return nil // complete file from a prior run, skip
		}
		// truncated file from an aborted copy — remove and re-copy
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove truncated dest: %w", err)
		}
		out, err = os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("recreate dest: %w", err)
		}
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	return nil
}

// isJunk returns true for dotfiles, sidecar files, and OS metadata to skip.
func isJunk(src, ext string) bool {
	base := filepath.Base(src)
	if strings.HasPrefix(base, ".") {
		return true
	}
	return junkBases[base] || junkExts[strings.ToLower(ext)]
}

// printSummary writes the processing summary to stdout.
func printSummary(cfg config, st stats) {
	mode := "dry-run"
	if cfg.apply {
		mode = "apply"
	}
	op := "copy"
	if cfg.move {
		op = "move"
	}
	fmt.Printf("\n=== summary (%s, %s) ===\n", mode, op)
	fmt.Printf("copied:        %d\n", st.copied)
	fmt.Printf("no-date:       %d\n", st.noDate)
	fmt.Printf("skipped-dup:   %d\n", st.skippedDup)
	fmt.Printf("recopy:        %d\n", st.recopy)
	fmt.Printf("dup-recopy:    %d\n", st.dupRecopy)
	fmt.Printf("no-date-recopy:%d\n", st.noDateRecopy)
	fmt.Printf("errors:        %d\n", st.errors)
	fmt.Printf("log:           %s\n", cfg.logPath)
}

// fatalf prints a formatted error to stderr and exits with code 1.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
