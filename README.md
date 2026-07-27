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
photo-organize --src DIR --dest DIR [--apply] [--move] [--keep-names] [--log FILE]
```

`--src` is scanned recursively: all files in subdirectories are included.

Without `--apply` the tool runs in dry-run mode: it logs what it would do but
copies or moves nothing.

Flags:

- `--src` — source directory (required)
- `--dest` — destination directory (required)
- `--apply` — copy/move files (default: dry-run)
- `--move` — move instead of copy
- `--quiet` — suppress per-file progress output to stderr
- `--keep-names` — preserve original filenames instead of `<date>-<hash>.<ext>`
- `--log` — log file path (default `organize.log.tsv`)

## Output layout

```
<dest>/<YYYY>/<MM>/<YYYY-MM-DD>-<HHMMSS>-<sha1[12]>.<ext>
<dest>/unknown/<sha1[12]>.<ext>   (when no date is recoverable)
```

With `--keep-names`, the original filename is preserved instead of the
date+hash name:

```
<dest>/<YYYY>/<MM>/<originalname.ext>
<dest>/unknown/<originalname.ext>   (when no date is recoverable)
```

Note: under `--keep-names`, two different files sharing an original name in
the same month bucket collide. The second overwrites the first and is logged
as `dup-recopy` for human review. This is the tradeoff of preserving names.

The 12-character sha1 prefix makes filenames content-based: identical files
always produce the same name, enabling automatic deduplication across source
directories. Re-running is a safe no-op.

## Date fallback chain

The tool resolves the capture date by checking exiftool tags in order, using
the first non-empty value:

1. `SubSecDateTimeOriginal` — sub-second EXIF capture (iPhone burst)
2. `DateTimeOriginal` — EXIF capture date (cameras, phones)
3. `CreateDate` — EXIF/MOV creation date
4. `IFD0:ModifyDate` — TIFF/Photoshop modify date (e.g. scanned prints)
5. `XMP:DateTimeOriginal` — XMP capture date (Lightroom edits)
6. `XMP:CreateDate` — XMP creation date (Apple Photos import, WhatsApp fallback)
7. `XMP:ModifyDate` — XMP modify date
8. `TrackCreateDate` — video track creation date
9. `QuickTime:CreateDate` — QuickTime container creation date (corrected to local time via `-api QuickTimeUTC`)
10. `FileModifyDate` — filesystem mtime (last resort)

Files with no recoverable date are placed in `unknown/`.

`XMP:CreateDate` is the save date for EXIF-stripped media like WhatsApp, not
the true capture date. Check `dateSourceTag` in the log to identify these.

The log file (TSV) records the source tag for each file so dates can be audited.

## Timezones

Dates resolve to the **local time of the host running the tool**:

- **Images**: `DateTimeOriginal`/`SubSecDateTimeOriginal`/`CreateDate` are
  stored naive (no offset) in EXIF. iPhone HEICs also carry `OffsetTime*`
  tags, but exiftool does not shift the date value, so it is used as-is —
  the local time the camera captured at. Pre-smartphone cameras and
  EXIF-stripped files carry no timezone at all. Either way the naive value
  is the best available signal and is used verbatim.
- **Videos**: QuickTime/MP4 `CreateDate` is stored as UTC seconds since
  epoch. exiftool's `-api QuickTimeUTC` converts it to host-local before
  the tool reads it, so a `.mov`/`.mp4` resolves to the host's local time,
  not the capture-location local time.

Consequence: re-running the tool on a host in a different timezone than
the original run will produce **different dates for video files** (and for
`FileModifyDate`-resolved files, since mtime changes on copy). Run the tool
on a host whose timezone matches the original import/capture timezone, or
accept that video dates reflect where the tool ran.
