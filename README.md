# photo-organize

Organizes photos and videos by capture date with content-hash naming. It shells
out to [exiftool](https://exiftool.org/) for metadata extraction and uses Go
for all hashing, path building, and file operations.

## Install

```
go install github.com/marcelbeumer/photo-organize@latest
```

Requires [exiftool](https://exiftool.org/):

- Arch: `pacman -S perl-image-exiftool`
- Debian/TrueNAS: `apt install libimage-exiftool-perl`

## Usage

```
photo-organize --src DIR --dest DIR [--apply] [--move] [--log FILE]
```

Without `--apply` the tool runs in dry-run mode: it logs what it would do but
copies or moves nothing.

Flags:

- `--src` — source directory (required)
- `--dest` — destination directory (required)
- `--apply` — copy/move files (default: dry-run)
- `--move` — move instead of copy
- `--log` — log file path (default `organize.log.tsv`)

## Output layout

```
<dest>/<YYYY>/<MM>/<YYYY-MM-DD>-<HHMMSS>-<sha1[12]>.<ext>
<dest>/unknown/<sha1[12]>.<ext>   (when no date is recoverable)
```

The 12-character sha1 prefix makes filenames content-based: identical files
always produce the same name, enabling automatic deduplication across source
directories. Re-running is a safe no-op.

## Date fallback chain

The tool resolves the capture date by checking exiftool tags in order, using
the first non-empty value:

1. `DateTimeOriginal` — EXIF capture date (cameras, phones)
2. `CreateDate` — EXIF/MOV creation date
3. `IFD0:ModifyDate` — TIFF/Photoshop modify date (e.g. scanned prints)
4. `XMP:ModifyDate` — XMP modify date
5. `XMP:DateTimeOriginal` — XMP capture date (Lightroom edits)
6. `TrackCreateDate` — video track creation date
7. `QuickTime:CreateDate` — QuickTime container creation date (corrected to local time via `-api QuickTimeUTC`)
8. `FileModifyDate` — filesystem mtime (last resort)
9. `FileCreateDate` — filesystem ctime (last resort)

Files with no recoverable date are placed in `unknown/`.

The log file (TSV) records the source tag for each file so dates can be audited.
