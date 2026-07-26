package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExifLine(t *testing.T) {
	// Column order matches dateFallbackChain (11 tags) + path + ext = 13 cols:
	//   path \t SubSecDateTimeOriginal \t DateTimeOriginal \t CreateDate
	//   \t IFD0:ModifyDate \t XMP:DateTimeOriginal \t XMP:CreateDate
	//   \t XMP:ModifyDate \t TrackCreateDate \t QuickTime:CreateDate
	//   \t FileModifyDate \t FileCreateDate \t ext
	tests := []struct {
		name string
		line string
		want fileRecord
		ok   bool
	}{
		{
			name: "DateTimeOriginal wins",
			line: "photos/IMG_001.jpg\t-\t2020:05:23_14:23:01\t-\t-\t-\t-\t-\t-\t-\t-\t-\tjpg",
			want: fileRecord{src: "photos/IMG_001.jpg", date: "2020:05:23_14:23:01", dateTag: "DateTimeOriginal", ext: "jpg"},
			ok:   true,
		},
		{
			name: "SubSecDateTimeOriginal wins (burst photo)",
			line: "photos/burst.heic\t2024:03:15_14:23:01.123\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\theic",
			want: fileRecord{src: "photos/burst.heic", date: "2024:03:15_14:23:01.123", dateTag: "SubSecDateTimeOriginal", ext: "heic"},
			ok:   true,
		},
		{
			name: "fallback to IFD0:ModifyDate (fourth column)",
			line: "photos/scan.tif\t-\t-\t-\t2003:11:05_08:52:31\t-\t-\t-\t-\t-\t-\t-\ttif",
			want: fileRecord{src: "photos/scan.tif", date: "2003:11:05_08:52:31", dateTag: "IFD0:ModifyDate", ext: "tif"},
			ok:   true,
		},
		{
			name: "XMP:DateTimeOriginal beats XMP:ModifyDate",
			line: "photos/xmp.jpg\t-\t-\t-\t-\t2020:05:23_14:23:01\t-\t2020:05:24_10:00:00\t-\t-\t-\t-\tjpg",
			want: fileRecord{src: "photos/xmp.jpg", date: "2020:05:23_14:23:01", dateTag: "XMP:DateTimeOriginal", ext: "jpg"},
			ok:   true,
		},
		{
			name: "XMP:CreateDate fallback (WhatsApp, EXIF stripped)",
			line: "whatsapp/IMG-2022.jpg\t-\t-\t-\t-\t-\t2022:08:01_10:15:00\t-\t-\t-\t-\t-\tjpg",
			want: fileRecord{src: "whatsapp/IMG-2022.jpg", date: "2022:08:01_10:15:00", dateTag: "XMP:CreateDate", ext: "jpg"},
			ok:   true,
		},
		{
			name: "fallback to FileModifyDate (tenth column)",
			line: "photos/nodate.jpg\t-\t-\t-\t-\t-\t-\t-\t-\t-\t2020:05:23_04:01:00\t-\tjpg",
			want: fileRecord{src: "photos/nodate.jpg", date: "2020:05:23_04:01:00", dateTag: "FileModifyDate", ext: "jpg"},
			ok:   true,
		},
		{
			name: "all dates missing",
			line: "photos/unknown.bin\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\t-\tbin",
			want: fileRecord{src: "photos/unknown.bin", date: "", dateTag: "", ext: "bin"},
			ok:   true,
		},
		{
			name: "QuickTime:CreateDate for video",
			line: "video/clip.mov\t-\t-\t2014:12:31_23:09:08\t-\t-\t-\t-\t2014:12:31_23:09:08\t2014:12:31_23:09:08\t-\t-\tmov",
			want: fileRecord{src: "video/clip.mov", date: "2014:12:31_23:09:08", dateTag: "CreateDate", ext: "mov"},
			ok:   true,
		},
		{
			name: "too few columns",
			line: "path\t2020:05:23_14:23:01\tjpg",
			ok:   false,
		},
		{
			name: "empty line",
			line: "",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, ok := parseExifLine(tt.line)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if rec != tt.want {
				t.Errorf("record = %+v, want %+v", rec, tt.want)
			}
		})
	}
}

func TestPlanDestination(t *testing.T) {
	tests := []struct {
		name       string
		dest       string
		rec        fileRecord
		hash       string
		keepNames  bool
		wantPath   string
		wantStatus string
	}{
		{
			name:       "valid date",
			dest:       "/output",
			rec:        fileRecord{src: "photos/IMG_001.jpg", date: "2020:05:23_14:23:01", dateTag: "DateTimeOriginal", ext: "jpg"},
			hash:       "a3f9c2e8b1d4",
			wantPath:   "/output/2020/05/2020-05-23-142301-a3f9c2e8b1d4.jpg",
			wantStatus: "copied",
		},
		{
			name:       "year boundary new year eve",
			dest:       "/photos",
			rec:        fileRecord{src: "video/clip.mov", date: "2014:12:31_23:09:08", dateTag: "CreateDate", ext: "mov"},
			hash:       "3123113e3cc4",
			wantPath:   "/photos/2014/12/2014-12-31-230908-3123113e3cc4.mov",
			wantStatus: "copied",
		},
		{
			name:       "empty date goes to unknown",
			dest:       "/output",
			rec:        fileRecord{src: "photos/nodate.bin", date: "", dateTag: "", ext: "bin"},
			hash:       "b7e1f2a3c4d5",
			wantPath:   "/output/unknown/b7e1f2a3c4d5.bin",
			wantStatus: "no-date",
		},
		{
			name:       "unparseable date goes to unknown",
			dest:       "/output",
			rec:        fileRecord{src: "photos/bad.jpg", date: "not-a-date", dateTag: "DateTimeOriginal", ext: "jpg"},
			hash:       "1234567890ab",
			wantPath:   "/output/unknown/1234567890ab.jpg",
			wantStatus: "no-date",
		},
		{
			name:       "uppercase ext already normalized by exiftool",
			dest:       "/output",
			rec:        fileRecord{src: "photos/15.JPG", date: "2003:11:05_08:52:31", dateTag: "IFD0:ModifyDate", ext: "jpg"},
			hash:       "077ea54d1509",
			wantPath:   "/output/2003/11/2003-11-05-085231-077ea54d1509.jpg",
			wantStatus: "copied",
		},
		// --keep-names: original filename preserved under dated bucket.
		{
			name:       "keep-names dated",
			dest:       "/output",
			rec:        fileRecord{src: "photos/IMG_001.jpg", date: "2020:05:23_14:23:01", dateTag: "DateTimeOriginal", ext: "jpg"},
			hash:       "",
			keepNames:  true,
			wantPath:   "/output/2020/05/IMG_001.jpg",
			wantStatus: "copied",
		},
		// --keep-names: no-date file keeps original name under unknown/.
		{
			name:       "keep-names no-date",
			dest:       "/output",
			rec:        fileRecord{src: "photos/nodate.bin", date: "", dateTag: "", ext: "bin"},
			hash:       "",
			keepNames:  true,
			wantPath:   "/output/unknown/nodate.bin",
			wantStatus: "no-date",
		},
		// --keep-names: original filename with mixed case is preserved as-is.
		{
			name:       "keep-names preserves mixed-case name",
			dest:       "/output",
			rec:        fileRecord{src: "photos/15.JPG", date: "2003:11:05_08:52:31", dateTag: "IFD0:ModifyDate", ext: "jpg"},
			hash:       "",
			keepNames:  true,
			wantPath:   "/output/2003/11/15.JPG",
			wantStatus: "copied",
		},
		// Sub-second variants: the parser strips ".<digits>" before time.Parse,
		// so the filename drops sub-seconds regardless of input form.
		{
			name:       "sub-second .123 in date drops to second-level filename",
			dest:       "/output",
			rec:        fileRecord{src: "photos/burst.heic", date: "2024:03:15_14:23:01.123", dateTag: "SubSecDateTimeOriginal", ext: "heic"},
			hash:       "abc123def456",
			wantPath:   "/output/2024/03/2024-03-15-142301-abc123def456.heic",
			wantStatus: "copied",
		},
		{
			name:       "sub-second .000 padding drops to second-level filename",
			dest:       "/output",
			rec:        fileRecord{src: "photos/padded.jpg", date: "2020:05:23_14:23:01.000", dateTag: "DateTimeOriginal", ext: "jpg"},
			hash:       "a3f9c2e8b1d4",
			wantPath:   "/output/2020/05/2020-05-23-142301-a3f9c2e8b1d4.jpg",
			wantStatus: "copied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, status := planDestination(tt.dest, tt.rec, tt.hash, tt.keepNames)
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if status != tt.wantStatus {
				t.Errorf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}

func TestIsJunk(t *testing.T) {
	tests := []struct {
		name string
		src  string
		ext  string
		want bool
	}{
		{"dotfile", "photos/.DS_Store", "", true},
		{"hidden file", "dir/.hidden", "", true},
		{"thumbs.db", "dir/Thumbs.db", "", true},
		{"thumbs.db lowercase", "dir/thumbs.db", "", true},
		{"aae sidecar", "dir/edit.aae", "aae", true},
		{"xmp sidecar", "dir/photo.xmp", "xmp", true},
		{"thm sidecar", "dir/thumb.thm", "thm", true},
		{"normal photo", "dir/IMG_001.jpg", "jpg", false},
		{"normal video", "dir/clip.mov", "mov", false},
		{"heic", "dir/photo.heic", "heic", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isJunk(tt.src, tt.ext); got != tt.want {
				t.Errorf("isJunk(%q, %q) = %v, want %v", tt.src, tt.ext, got, tt.want)
			}
		})
	}
}

func TestExiftoolFormat(t *testing.T) {
	got := exiftoolFormat()

	if !strings.HasPrefix(got, "${Directory}/${FileName}") {
		t.Errorf("format does not start with Directory/FileName: %q", got)
	}
	if !strings.HasSuffix(got, "\t${FileTypeExtension}\n") {
		t.Errorf("format does not end with FileTypeExtension: %q", got)
	}
	for _, tag := range dateFallbackChain {
		want := "${" + tag + "}"
		if !strings.Contains(got, want) {
			t.Errorf("format missing tag %q in %q", want, got)
		}
	}
}

func TestContentHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	// sha1("test") = a94a8fe5ccb19ba61c4c0873d391e987982fbbd3
	want := "a94a8fe5ccb1"
	os.WriteFile(path, []byte("test"), 0o644)

	got, err := contentHash(path)
	if err != nil {
		t.Fatalf("contentHash() err = %v", err)
	}
	if got != want {
		t.Errorf("contentHash() = %q, want %q", got, want)
	}
}

func TestContentHash_Deterministic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "same.txt")

	os.WriteFile(path, []byte("identical content"), 0o644)

	h1, err := contentHash(path)
	if err != nil {
		t.Fatalf("first contentHash() err = %v", err)
	}
	h2, err := contentHash(path)
	if err != nil {
		t.Fatalf("second contentHash() err = %v", err)
	}
	if h1 != h2 {
		t.Errorf("same content different hash: %q vs %q", h1, h2)
	}
}

func TestContentHash_DifferentContent(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.txt")
	p2 := filepath.Join(dir, "b.txt")

	os.WriteFile(p1, []byte("content A"), 0o644)
	os.WriteFile(p2, []byte("content B"), 0o644)

	h1, _ := contentHash(p1)
	h2, _ := contentHash(p2)
	if h1 == h2 {
		t.Error("different content produced same hash")
	}
}

func TestContentHash_MissingFile(t *testing.T) {
	_, err := contentHash("/nonexistent/path/file.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCopyNoClobber(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "sub", "dst.txt")
	os.WriteFile(src, []byte("hello"), 0o644)
	os.MkdirAll(filepath.Dir(dst), 0o755)

	if err := copyNoClobber(src, dst); err != nil {
		t.Fatalf("copyNoClobber() err = %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile() err = %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("dst content = %q, want %q", got, "hello")
	}
}

func TestCopyNoClobber_DoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	// same content, same size — dst is a complete copy from a prior run
	os.WriteFile(src, []byte("original"), 0o644)
	os.WriteFile(dst, []byte("original"), 0o644)

	if err := copyNoClobber(src, dst); err != nil {
		t.Fatalf("copyNoClobber() err = %v", err)
	}

	got, _ := os.ReadFile(dst)
	if string(got) != "original" {
		t.Errorf("dst overwritten: got %q, want %q", got, "original")
	}
}

func TestCopyNoClobber_OverwritesTruncated(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	// src is complete, dst is truncated (aborted copy left only 3 of 11 bytes)
	os.WriteFile(src, []byte("hello world"), 0o644)
	os.WriteFile(dst, []byte("hel"), 0o644)

	if err := copyNoClobber(src, dst); err != nil {
		t.Fatalf("copyNoClobber() err = %v", err)
	}

	got, _ := os.ReadFile(dst)
	if string(got) != "hello world" {
		t.Errorf("dst not re-copied: got %q, want %q", got, "hello world")
	}
}

func TestCopyNoClobber_MissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	if err := copyNoClobber("/nonexistent", dst); err == nil {
		t.Fatal("expected error for missing source")
	}
}

func TestSizesDiffer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	os.WriteFile(src, []byte("hello world"), 0o644)

	t.Run("dest missing", func(t *testing.T) {
		dest := filepath.Join(dir, "missing.bin")
		got, err := sizesDiffer(src, dest)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got {
			t.Error("dest missing should not report sizes differ")
		}
	})

	t.Run("same size", func(t *testing.T) {
		dest := filepath.Join(dir, "same.bin")
		os.WriteFile(dest, []byte("hello world"), 0o644)
		got, err := sizesDiffer(src, dest)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got {
			t.Error("same-size dest should not report sizes differ")
		}
	})

	t.Run("different size", func(t *testing.T) {
		dest := filepath.Join(dir, "truncated.bin")
		os.WriteFile(dest, []byte("hel"), 0o644)
		got, err := sizesDiffer(src, dest)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !got {
			t.Error("different-size dest should report sizes differ")
		}
	})

	t.Run("source missing", func(t *testing.T) {
		dest := filepath.Join(dir, "same.bin")
		_, err := sizesDiffer("/nonexistent", dest)
		if err == nil {
			t.Fatal("expected error for missing source")
		}
	})
}

func TestStatusPrefix(t *testing.T) {
	tests := []struct {
		status string
		apply  bool
		want   string
	}{
		{statusCopied, true, "copy  "},
		{statusNoDate, true, "nodate"},
		{statusSkippedDup, true, "dup   "},
		{statusRecopy, true, "recopy"},
		{statusDupRecopy, true, "duprec"},
		{statusNoDateRecopy, true, "ndrecp"},
		{statusError, true, "error "},
		// dry-run overrides every status except error
		{statusCopied, false, "dry   "},
		{statusNoDate, false, "dry   "},
		{statusSkippedDup, false, "dry   "},
	}
	for _, tt := range tests {
		got := statusPrefix(tt.status, tt.apply)
		if got != tt.want {
			t.Errorf("statusPrefix(%q, apply=%v) = %q, want %q", tt.status, tt.apply, got, tt.want)
		}
	}
}

func TestUndoStatus(t *testing.T) {
	st := stats{
		copied:       3,
		noDate:       2,
		skippedDup:   1,
		recopy:       1,
		dupRecopy:    1,
		noDateRecopy: 1,
		errors:       0,
	}
	// Decrement each counter down to zero.
	for range 3 {
		undoStatus(&st, statusCopied)
	}
	for range 2 {
		undoStatus(&st, statusNoDate)
	}
	undoStatus(&st, statusSkippedDup)
	undoStatus(&st, statusRecopy)
	undoStatus(&st, statusDupRecopy)
	undoStatus(&st, statusNoDateRecopy)
	if st != (stats{}) {
		t.Errorf("stats not zeroed: %+v", st)
	}
}

func TestStripSubsec(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"2024:03:15_14:23:01", "2024:03:15_14:23:01"},     // no fraction
		{"2024:03:15_14:23:01.123", "2024:03:15_14:23:01"}, // real sub-seconds
		{"2024:03:15_14:23:01.000", "2024:03:15_14:23:01"}, // padded
		{"2024:03:15_14:23:01.", "2024:03:15_14:23:01."},   // trailing dot, no digits — left intact
		{"", ""}, // empty
		{"2024:03:15_14:23:01.12a", "2024:03:15_14:23:01.12a"}, // non-digit after dot — left intact
	}
	for _, tt := range tests {
		got := stripSubsec(tt.in)
		if got != tt.want {
			t.Errorf("stripSubsec(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
