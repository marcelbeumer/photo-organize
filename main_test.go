package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseExifLine(t *testing.T) {
	tests := []struct {
		name string
		line string
		want fileRecord
		ok   bool
	}{
		{
			name: "DateTimeOriginal wins",
			line: "photos/IMG_001.jpg\t2020:05:23_14:23:01\t-\t-\t-\t-\t-\t-\t-\t-\tjpg",
			want: fileRecord{src: "photos/IMG_001.jpg", date: "2020:05:23_14:23:01", dateTag: "DateTimeOriginal", ext: "jpg"},
			ok:   true,
		},
		{
			name: "fallback to IFD0:ModifyDate (third column)",
			line: "photos/scan.tif\t-\t-\t2003:11:05_08:52:31\t-\t-\t-\t-\t-\t-\ttif",
			want: fileRecord{src: "photos/scan.tif", date: "2003:11:05_08:52:31", dateTag: "IFD0:ModifyDate", ext: "tif"},
			ok:   true,
		},
		{
			name: "fallback to FileModifyDate (eighth column)",
			line: "photos/nodate.jpg\t-\t-\t-\t-\t-\t-\t-\t2020:05:23_04:01:00\t-\tjpg",
			want: fileRecord{src: "photos/nodate.jpg", date: "2020:05:23_04:01:00", dateTag: "FileModifyDate", ext: "jpg"},
			ok:   true,
		},
		{
			name: "all dates missing",
			line: "photos/unknown.bin\t-\t-\t-\t-\t-\t-\t-\t-\t-\tbin",
			want: fileRecord{src: "photos/unknown.bin", date: "", dateTag: "", ext: "bin"},
			ok:   true,
		},
		{
			name: "QuickTime:CreateDate for video",
			line: "video/clip.mov\t-\t2014:12:31_23:09:08\t-\t-\t-\t2014:12:31_23:09:08\t2014:12:31_23:09:08\t-\t-\tmov",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, status := planDestination(tt.dest, tt.rec, tt.hash)
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

	os.WriteFile(src, []byte("new"), 0o644)
	os.WriteFile(dst, []byte("original"), 0o644)

	if err := copyNoClobber(src, dst); err != nil {
		t.Fatalf("copyNoClobber() err = %v", err)
	}

	got, _ := os.ReadFile(dst)
	if string(got) != "original" {
		t.Errorf("dst overwritten: got %q, want %q", got, "original")
	}
}

func TestCopyNoClobber_MissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst.txt")

	if err := copyNoClobber("/nonexistent", dst); err == nil {
		t.Fatal("expected error for missing source")
	}
}
