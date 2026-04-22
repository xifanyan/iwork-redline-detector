---
title: iWork Redline Detector
created: 2026-03-20
tags:
  - iwork
  - document-processing
  - redlines
  - apple
status: completed
---

# iWork Redline Detector

## Table of Contents

- [Overview](#overview)
- [Research](#research)
- [How It Works](#how-it-works)
  - [Supported Formats](#supported-formats)
  - [Detection Approach](#detection-approach)
  - [Step-by-Step Process](#step-by-step-process)
  - [Why Baselines?](#why-baselines)
  - [In Summary](#in-summary)
- [Implementation](#implementation)
  - [Architecture](#architecture)
  - [Detection Logic](#detection-logic)
  - [Result Structure](#result-structure)
- [Usage](#usage)
  - [Output Formats](#output-formats)
  - [Detection Confidence](#detection-confidence)
- [Test Results](#test-results)
  - [Modern Format](#modern-format-testdatapages)
  - [Legacy iWork '09 Format](#legacy-iwork-09-format-testdatapages09)
- [Technical Notes](#technical-notes)
- [Limitations](#limitations)
- [Related](#related)

## Overview

Go-based tool to detect track changes and redlines in Apple iWork documents (.pages files). Uses direct protobuf field scanning plus change heuristics to identify:
- Track Changes feature enabled/disabled status
- Tracked insertions and deletions
- Track Changes paused state
- Comment-only redlines in modern Pages and legacy Pages '09 files
- Markup visibility settings

## Research

This project was built based on detailed research of the iWork file format. See [[iWork-TrackChanges-Research-Report]] for the complete technical analysis including:
- IWA file format structure (Snappy + Protocol Buffers)
- Track changes protobuf message types
- Modern and legacy comment detection findings
- ChangeArchive detection rules
- Legal considerations for redline workflows

## How It Works

### Supported Formats

The tool automatically detects and handles three iWork file formats:

#### Modern IWA Format (.pages, iWork 2014+)

Modern `.pages` files use Apple's binary IWA (iWork Archive) format. Files are ZIP bundles containing `Index/Document.iwa` and other IWA payloads directly in an `Index/` folder.

#### Pages 2013 Format (.pages, iWork 2013)

Pages 2013 files are a transitional format. They contain `Index.zip` at the bundle root, which itself contains IWA files (e.g., `Document.iwa`, `DocumentStylesheet.iwa`). This format was used during Apple's migration from XML to IWA.

#### Legacy iWork '09 XML Format (.pages)

iWork '09 files use plain XML instead of IWA bundles. They contain a top-level `index.xml` (or `index.xml.gz`) entry in the ZIP archive rather than `Index/Document.iwa`.

The tool detects the format automatically by inspecting ZIP entry names:
- `Index/Document.iwa` → Modern IWA format
- `Index.zip` → Pages 2013 format
- `index.xml` / `index.xml.gz` → Legacy XML format

#### Format Fallback

If a file appears to be Modern format (has `Index/Document.iwa`) but parsing fails (corrupt IWA, invalid snappy data, etc.), the tool automatically falls back to legacy XML parsing as a recovery mechanism. This makes the tool more robust when encountering edge cases or partially corrupted files.

### Detection Approach

The detector uses a **multi-signal detection strategy** to accurately identify track changes status:

#### 1. Settings Detection (High Confidence)

The detector decompresses the relevant `.iwa` payloads and reads the boolean settings fields directly:

- **`Document.iwa`**: field 40 indicates whether Track Changes is enabled
- **`ViewState*.iwa`** (contains `TP.UIStateArchive`): field 28 indicates whether tracking is currently paused
- **`TP.SettingsArchive` (when parsed)**: provides markup visibility settings

This lets the tool distinguish `Disabled`, `Paused`, and `Enabled (No Changes)` without relying only on insertion/deletion counts.

#### 2. Legacy XML Parsing (iWork '09)

For iWork '09 files, the detector parses `index.xml` directly:

- **`sl:change-tracking`**: reads `enabled` and `suspended` attributes for tracking state
- **`sf:text-changes`**: reads aggregate `insertion-count` and `deletion-count` attributes when present
- **`sf:changed` / `sf:change`**: counts individual change elements by `kind` attribute ("insertion" or "deletion")
- **`sf:annotation`**: counts annotation blocks as legacy comments when no tracked insertions/deletions are present

Priority: aggregate counts from `sf:text-changes` are used when available; otherwise individual change elements are counted. Malformed XML returns an error rather than silently producing incorrect results.

#### 3. Comment Detection

Comments are detected and reported independently from tracked insertions/deletions.

- **Modern Pages (2014+)**: parsed `TSD.CommentStorageArchive` (type 3056) and `TSWP.HighlightArchive` (type 2013) records from `Document.iwa` and other IWA files in the bundle. When `AnnotationAuthorStorage.iwa` contains typed comment records, those are counted as well.
- **Pages 2013**: parsed `TSD.CommentStorageArchive` records from IWA files inside `Index.zip`. Only records without field 4 (revision markers) are counted, which matches the visible comment count. Falls back to parsed `HighlightArchive` records when no `CommentStorageArchive` records exist, and to text heuristics only when IWA parsing fails entirely.
- **Legacy Pages '09**: count `sf:annotation` elements in `index.xml`
- **Combined reporting**: documents with both tracked changes and comments show both signals in debug output

This keeps the `COMMENTS` column informative in every case while still treating either signal as sufficient for `REDLINES=true`.

#### 4. Change Detection

The detector uses structured parsed records when IWA parsing succeeds, falling back to byte-pattern heuristics only when parsing fails:

**Structured detection (primary):**
When `iwa.ParseIWAFile()` succeeds, the detector counts typed `ChangeArchive` records (observed as type 2060 in real-world samples) by their `kind` field:
- **Kind 1**: Insertions
- **Kind 2**: Deletions

This eliminates false positives from unrelated protobuf structures that happen to match byte patterns.

**Heuristic detection (fallback):**
When IWA parsing fails, the detector scans decompressed bytes for byte-pattern markers:
- **Insertion markers**: `0x08 0x01 0x12`
- **Deletion markers**: `0x08 0x02 0x12`

Thresholds for heuristic mode:
- **Insertions > 21** → actual track changes detected
- **Deletions > 1** → actual track changes detected

#### 5. Combined Status

The detector combines both signals to determine the final status:

| Enabled | Paused | Changes Detected | Status |
|---|---|---|---|
| No | No | No | `Disabled` |
| Yes | No | No | `Enabled (No Changes)` |
| Yes | No | Yes | `Enabled (With Changes)` |
| Yes | Yes | Yes/No | `Paused` |
| Unknown | Unknown | Yes | `Enabled (With Changes)` - fallback |

### Step-by-Step Process

**1. File Structure**
A `.pages` file is a ZIP archive:

| Path | Description |
|-------|-------------|
| `DocumentName.pages/` | Root of .pages bundle |
| `Index/` | Contains all document data (modern format) |
| `Index/Document.iwa` | Main content + track changes settings |
| `Index/DocumentStylesheet.iwa` | Document styles |
| `Index/AnnotationAuthorStorage.iwa` | Author names and colors |
| `Index.zip` | Nested ZIP containing IWA files (Pages 2013 format) |

**2. Parse Document.iwa**
The `Document.iwa` file contains:
- **Snappy compression** - decompress to access raw data
- **Protocol Buffers** - structured message format with type IDs
- **IWA object stream** - repeated `varint length → ArchiveInfo → payload` objects
- **Message types**:
  - `TP.DocumentArchive` (type 1002) - document settings
  - `TP.SettingsArchive` (type 1003) - markup visibility
  - `TSWP.TextStorageArchive` (type 1001) - actual text content
  - `TSWP.ChangeArchive` (type ~2060 observed) - pending tracked changes
  - `TSD.CommentStorageArchive` (type 3056) - comment records
  - `TSWP.HighlightArchive` (type 2013) - highlight/comment anchor records

**3. Read Track Changes Setting**
Read the decompressed settings signals:
- **Document field 40**: Track Changes enabled
- **UIStateArchive field 28** (in ViewState*.iwa): Track Changes paused

**4. Count Changes**
When IWA parsing succeeds, count typed `ChangeArchive` records by `kind` field (1=insertion, 2=deletion). When parsing fails, fall back to byte-pattern heuristic scanning.

**5. Determine Status**
Combine settings and counts to produce the final track-changes status.

**6. Detect Comments**
Check for comments independently of tracked insertions/deletions:
- **Modern**: parsed `CommentStorageArchive` and `HighlightArchive` records from IWA files
- **Pages 2013**: parsed `CommentStorageArchive` records (without field 4) from IWA files inside `Index.zip`
- **Legacy**: `sf:annotation` elements in `index.xml`

Overall `REDLINES=true` when either tracked changes exist or comment-only redlines are found.

**7. Compression**
The `Document.iwa` file is compressed using Google's Snappy algorithm. We decompress it to read the raw data.

**8. Protobuf Structure**
Inside the decompressed data, Pages uses Google's Protocol Buffers to organize information. The IWA object stream contains repeated `ArchiveInfo → MessageInfo → payload` objects, each tagged with a type ID.

**9. Track Changes Markers**
When track changes is in use, Pages stores `ChangeArchive` records (observed type 2060 in real-world samples) in the IWA object stream:
- **kind = 1** → insertion (underlined text)
- **kind = 2** → deletion (strikethrough text)

In heuristic fallback mode, these appear as byte patterns `08 01 12` (insertion) and `08 02 12` (deletion) in the decompressed data.

**10. Counting**
When structured parsing succeeds, the detector counts typed records directly — no thresholds needed. When falling back to heuristic mode, it counts byte patterns and applies thresholds (insertions > 21, deletions > 1).

### Why Baselines?

When structured IWA parsing succeeds, no baselines or thresholds are needed — typed `ChangeArchive` records are counted directly. Baseline thresholds (insertions > 21, deletions > 1) are only used in heuristic fallback mode when IWA parsing fails. In heuristic mode, normal documents already have ~20 insertion-pattern matches from regular text styling, so the threshold avoids false positives.

### In Summary

| Step | Description |
|-------|-------------|
| 1 | Open .pages file (ZIP format) |
| 2 | Detect format: Modern (Index/Document.iwa), Pages 2013 (Index.zip), or Legacy (index.xml) |
| 3 | Extract and decompress Document.iwa |
| 4 | Parse IWA object stream for typed records |
| 5 | Count ChangeArchive records (structured) or scan byte patterns (heuristic fallback) |
| 6 | Count CommentStorageArchive / HighlightArchive records for comments |
| 7 | Return result: REDLINES or OK |

## Implementation

### Architecture

| Component | Description |
|-----------|-------------|
| `main.go` | CLI entry point, progress bar, batch processing, error handling |
| `iwa/parser.go` | IWA file parsing (Snappy decompression + protobuf decoding + object stream traversal) |
| `detector/types.go` | Type ID mappings, field constants, TrackChangesStatus enum, format detection |
| `detector/redline.go` | Redline detection logic + protobuf settings parsing + structured comment counting |
| `bin/` | Compiled binary executables |

**Project Structure:**

- `iwork-redline-detector/`
  - `main.go`
  - `iwa/`
    - `parser.go`
    - `parser_test.go`
  - `detector/`
    - `types.go`
    - `redline.go`
    - `redline_test.go`
  - `testdata/`
    - `pages/`
    - `pages09/`
    - `pages2013/`
  - `bin/`
    - `release/` (platform-specific binaries)

**CLI Features:**
- Progress bar with elapsed time display
- Concurrent batch processing with configurable thread count
- Error collection and reporting via separate errors.csv file
- Format auto-detection with automatic fallback to legacy XML parsing
- Summary statistics (processed count, error count, format counts)

### Detection Logic

The detector uses a **layered strategy**: structured IWA parsing first, byte-pattern heuristics as fallback.

**Primary Signal (Structured Parsing):**
```go
1. Parse Document.iwa via IWA object stream (varint length → ArchiveInfo → payloads)
2. Count typed ChangeArchive records (observed type 2060) by kind field
   - kind == 1 → insertion
   - kind == 2 → deletion
3. Read field 40 (change_tracking_enabled) from decompressed data
4. Read field 28 from ViewState*.iwa (paused state)
5. Count comment records:
   - HighlightArchive (type 2013) and CommentStorageArchive (type 3056)
   - Pages 2013: CommentStorageArchive without field 4 (excludes revision records)
6. Result: Accurate counts with no false positives from unrelated protobuf structures
```

**Fallback Signal (Heuristic):**
```go
1. Decompress Document.iwa with Snappy
2. Scan for byte patterns:
   - 0x08 0x01 0x12 = insertion marker
   - 0x08 0x02 0x12 = deletion marker
3. Count occurrences
4. Apply thresholds: insertions > 21 OR deletions > 1
5. Result: Best-effort detection when structured parsing fails
```

**Combination Logic:**
- If IWA parsing succeeds → use typed record counts (no false positives from styling)
- If IWA parsing fails → fall back to heuristic byte-pattern counting
- Set `HighConfidence` when the current mode comes from document/view-state settings
- Detect comments independently of tracked insertions/deletions
- Treat comment-only documents as redlines without changing the track-changes status label

**Track Changes Status Values:**

- **`Disabled`**: Track Changes feature is turned off. No tracked changes will be recorded.
- **`Paused`**: Track Changes was enabled for the document, but recording is currently paused.
- **`Enabled (No Changes)`**: Track Changes is turned on, but no insertions/deletions have been made yet.
- **`Enabled (With Changes)`**: Track Changes is on and actual tracked changes (insertions/deletions) are present in the document.

### Result Structure

```go
type RedlineDetection struct {
    Format                FormatType        // Detected file format: Modern or Legacy XML
    TrackChangesStatus    TrackChangesStatus // Disabled, Paused, EnabledNoChanges, EnabledWithChanges
    SettingEnabled        bool               // From Document.iwa field 40 (modern) or sl:change-tracking (legacy)
    SettingPaused         bool               // From UIStateArchive field 28 in ViewState*.iwa (modern) or sl:suspended (legacy)
    TrackedChangesPresent bool               // From heuristic scan (modern) or sf:changed elements (legacy)
    HighConfidence        bool               // True if settings fields were found

    InsertionCount int                    // From heuristic scan (modern) or XML parsing (legacy)
    DeletionCount  int                    // From heuristic scan (modern) or XML parsing (legacy)
    HiddenChanges  int                    // Hidden changes count

    Changes []Change                     // Individual change records (future enhancement)

    HasComments  bool                   // Comments detected separately
    CommentCount int                    // Counted independently from tracked insertions/deletions

    MarkupSettings MarkupSettings          // Visibility settings
    Authors []Author                    // Detected authors
}
```

## Usage

```bash
# Single file
./bin/iwork-redline-detector document.pages

# Directory (finds all .pages files)
./bin/iwork-redline-detector ./path/to/folder/

# With debug output (shows insertions/deletions count and comments)
./bin/iwork-redline-detector -debug ./path/to/folder/

# Output results as CSV to a file (suppresses console table output)
./bin/iwork-redline-detector -csv results.csv ./path/to/folder/

# Custom thread count (default: 2)
./bin/iwork-redline-detector -threads 4 ./path/to/folder/

# Batch processing from filelist (one .pages path per line)
./bin/iwork-redline-detector -filelist filelist.txt

# Specify custom errors CSV file (default: errors.csv)
./bin/iwork-redline-detector -errors-csv processing_errors.csv -filelist filelist.txt
```

### Output Formats

**Normal mode** (aligned table, 3 columns):
```
FILEPATH                        REDLINES  FORMAT  
normal.track.accepted.pages     false     Modern  
normal.pages                    false     Modern  
blank.track.pages               false     Modern  
comments.no-tracking.pages      true      Modern  
track.not-accepted.pages        true      Modern  
deletion.track-paused.pages     true      Modern  
tracking.insert.deletion.pages  true      Modern  
encrypted.pages                             Modern  
```

**Debug mode** (aligned table, all columns):
```
FILEPATH                        REDLINES  INSERTIONS  DELETIONS  COMMENTS      SOURCE    STATUS                  CONF  FORMAT     ENCRYPTED
normal.pages                    false     20          1                                 Disabled                High  Modern     false
comments.no-tracking.pages      true      20          1          Comments (1)  Comments  Paused                  High  Modern     false
track.not-accepted.pages        true      22          1                       Tracked Changes  Enabled (With Changes)  High  Modern     false
pages09/normal.pages            false     0           0                                 Disabled                High  Pages '09  false
pages09/comments.no-tracking.pages  true  0           0          Comments (1)  Comments  Paused                  High  Pages '09  false
encrypted.pages                                        -                                  -                        -     Modern     true
```

**Encrypted files** show empty REDLINES field since content cannot be analyzed. The actual format (Modern/Legacy) is preserved when detectable.

**CSV output** (when `-csv <filename>` is specified):
```csv
filepath,redlines,encrypted,insertions,deletions,comments,source,status,conf,format
normal.pages,false,false,20,1,,,Disabled,High,Modern
comments.no-tracking.pages,true,false,20,1,Comments (1),Comments,Paused,High,Modern
comments.track.pages,true,false,22,1,Comments (1),Tracked Changes + Comments,Enabled (With Changes),High,Modern
track.not-accepted.pages,true,false,22,1,,Tracked Changes,Enabled (With Changes),High,Modern
pages09/comments.no-tracking.pages,true,false,0,0,Comments (1),Comments,Paused,High,Pages '09
encrypted.pages,,true,0,0,,,,,Modern
```

Note: For encrypted files, `redlines` is empty (not `true` or `false`), `encrypted` is `true`, and all count/status fields are empty.

**Progress Bar & Summary Report:**
```
Processing 24436 file(s) with 2 thread(s)...

Processing...  34% |█████████████| (8552/24436) [1m30s]
Processed: 24423 | Errors: 13 | Encrypted: 5 | Modern: 1233 | Legacy: 1231
```

**Errors CSV** (when errors occur, written to `errors.csv` by default or custom path):
```csv
filepath,error message
"\\server\share\corrupt.pages","failed to decompress Document.iwa: snappy: corrupt input"
"\\server\share\truncated.pages","failed to extract Document.iwa: zip: not a valid zip file"
```

### Exit Codes

- `0`: processing completed with no errors (redlines may or may not have been found)
- `1`: usage or runtime error (invalid args, unreadable file, CSV write failure, or any file processing error)

### Detection Confidence

The detector provides different levels of confidence based on data availability:

| Confidence Level | Source | Interpretation |
|---|---|---|
| **High** | Settings fields found | Track Changes mode is read directly from document/view-state data |
| **Low** | Heuristic only | Status based on byte-pattern counting, may have false positives |

When settings fields cannot be found, the detector falls back to heuristic detection and marks confidence as low.

## Test Results

### Modern Format (testdata/pages/)

| File | Insertions | Deletions | Comments | Redlines | Status | Format |
|------|------------|-----------|----------|----------|---------|--------|
| normal.pages | 20 | 1 | 0 | false | Disabled | Modern |
| comments.no-tracking.pages | 20 | 1 | 1 | true | Paused | Modern |
| comments.track.pages | 22 | 1 | 1 | true | Enabled (With Changes) | Modern |
| blank.track.pages | 20 | 1 | 0 | false | Enabled (No Changes) | Modern |
| normal.track.accepted.pages | 21 | 1 | 0 | false | Enabled (No Changes) | Modern |
| track.not-accepted.pages | 22 | 1 | 0 | true | Enabled (With Changes) | Modern |
| deletion.track-paused.pages | 21 | 2 | 0 | true | Paused | Modern |
| tracking.insert.deletion.pages | 22 | 3 | 0 | true | Enabled (With Changes) | Modern |

### Legacy iWork '09 Format (testdata/pages09/)

| File | Insertions | Deletions | Comments | Redlines | Status | Format |
|------|------------|-----------|----------|----------|---------|--------|
| normal.pages | 0 | 0 | 0 | false | Disabled | Pages '09 |
| comments.no-tracking.pages | 0 | 0 | 1 | true | Paused | Pages '09 |
| comments.track.pages | 1 | 0 | 1 | true | Enabled (With Changes) | Pages '09 |
| blank.track.pages | 0 | 0 | 0 | false | Enabled (No Changes) | Pages '09 |
| normal.track.accepted.pages | 0 | 0 | 0 | false | Enabled (No Changes) | Pages '09 |
| track.not-accepted.pages | 1 | 0 | 0 | true | Enabled (With Changes) | Pages '09 |
| deletion.track-paused.pages | 0 | 1 | 0 | true | Paused | Pages '09 |
| tracking.insert.deletion.pages | 1 | 2 | 0 | true | Enabled (With Changes) | Pages '09 |

### Pages 2013 Format (testdata/pages2013/)

| File | Insertions | Deletions | Comments | Redlines | Status | Format |
|------|------------|-----------|----------|----------|---------|--------|
| normal.2013.pages | varies | varies | 0 | varies | varies | Pages 2013 |

## Technical Notes

- **IWA files** use Snappy compression with 4-byte length prefix
- **IWA object stream** is a repeated sequence of `varint length → ArchiveInfo → MessageInfo payloads`, not a single monolithic protobuf message
- **Protobuf encoding** uses variable-length integers for field tags and values
- **Type IDs**: Messages are identified by integer type IDs (e.g., 1002 = DocumentArchive, 3056 = CommentStorageArchive)
- **Field numbers**: Protocol buffer fields are identified by numbers (e.g., field 40 = change_tracking_enabled, field 4 presence in CommentStorageArchive = revision marker)
- **Snappy framing**: Custom variant without CRC-32C checksums or stream identifier chunks
- **Structured vs heuristic**: Structured parsing counts typed records directly; heuristic byte-pattern scanning is a fallback with thresholds to avoid false positives from character styling
- **Pages 2013 comment counting**: `CommentStorageArchive` records without field 4 correspond to visible comments; records with field 4 are revision/edit markers and are excluded

## Limitations

- **Keynote and Numbers**: Only support comments/annotations, **not** inline track changes
- **Heuristic threshold**: When structured IWA parsing fails, modern format change detection falls back to byte-pattern thresholds and may produce false positives on documents with unusual styling density.
- **Legacy XML**: Heuristic scanning is not applied to iWork '09 files; change counts come from XML parsing only.
- **Comments detection**: Modern and Pages 2013 comments are counted from structured parsed records (`TSD.CommentStorageArchive`, `TSWP.HighlightArchive`). Legacy Pages '09 comments are counted from `sf:annotation` XML elements.
- **Pages 2013 comments**: Counted from `CommentStorageArchive` records that lack field 4 (revision markers). In the rare case that IWA parsing fails entirely, a text-based heuristic fallback is used.
- **Change records**: Individual change author/timestamp parsing not yet fully implemented
- **Legal-grade detection**: For highest confidence, compare against a known-clean baseline document
- **Format fallback**: When Modern format parsing fails and fallback to legacy XML succeeds, the reported format remains "Modern" (reflecting the detected structure), even though legacy parsing was used
- **Encrypted files**: Password-protected iWork files cannot be analyzed. They are detected via `.iwpv2` file (modern), "snappy: corrupt" errors during decompression (modern), or "unsupported compression" errors (legacy). They are reported with `encrypted=true` and excluded from the `errors.csv` report. Content is not parsed.

## Related

- [[iWork-TrackChanges-Research-Report]]
