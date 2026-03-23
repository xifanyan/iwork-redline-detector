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

The tool automatically detects and handles two iWork file formats:

#### Modern IWA Format (.pages)

Modern `.pages` files use Apple's binary IWA (iWork Archive) format. Files are ZIP bundles containing `Index/Document.iwa` and other IWA payloads.

#### Legacy iWork '09 XML Format (.pages)

iWork '09 files use plain XML instead of IWA bundles. They contain a top-level `index.xml` (or `index.xml.gz`) entry in the ZIP archive rather than `Index/Document.iwa`.

The tool detects the format automatically by inspecting ZIP entry names:
- `Index/Document.iwa` → Modern IWA format
- `index.xml` / `index.xml.gz` → Legacy XML format

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

- **Modern Pages**: detect comment-like payload text from decompressed `Document.iwa`
- **Legacy Pages '09**: count `sf:annotation` elements in `index.xml`
- **Combined reporting**: documents with both tracked changes and comments show both signals in debug output

This keeps the `COMMENTS` column informative in every case while still treating either signal as sufficient for `REDLINES=true`.

#### 4. Change Detection (Heuristic)

The detector also scans for actual tracked changes using byte-pattern detection:

- **Insertion markers**: `0x08 0x01 0x12` - indicates inserted text
- **Deletion markers**: `0x08 0x02 0x12` - indicates deleted text

Normal documents have ~20 insertion patterns from character styling, so we use thresholds:
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
| `Index/` | Contains all document data |
| `Index/Document.iwa` | Main content + track changes settings |
| `Index/DocumentStylesheet.iwa` | Document styles |
| `Index/AnnotationAuthorStorage.iwa` | Author names and colors |

**2. Parse Document.iwa**
The `Document.iwa` file contains:
- **Snappy compression** - decompress to access raw data
- **Protocol Buffers** - structured message format with type IDs
- **Message types**:
  - `TP.DocumentArchive` (type 1002) - document settings
  - `TP.SettingsArchive` (type 1003) - markup visibility
  - `TSWP.TextStorageArchive` (type 1001) - actual text content

**3. Read Track Changes Setting**
Read the decompressed settings signals:
- **Document field 40**: Track Changes enabled
- **UIStateArchive field 28** (in ViewState*.iwa): Track Changes paused

**4. Count Changes**
Scan decompressed data for ChangeArchive patterns:
- **Kind 1**: Insertions (underlined text)
- **Kind 2**: Deletions (strikethrough text)

**5. Determine Status**
Combine settings and counts to produce the final track-changes status.

**6. Detect Comments**
Check for comments independently of tracked insertions/deletions:
- Modern: comment-like payloads in `Document.iwa`
- Legacy: `sf:annotation` elements in `index.xml`

Overall `REDLINES=true` when either tracked changes exist or comment-only redlines are found.

**2. Compression**
The `Document.iwa` file is compressed using Google's Snappy algorithm. We decompress it to read the raw data.

**3. Protobuf Structure**
Inside the decompressed data, Pages uses Google's Protocol Buffers to organize information. Think of it like a structured database with:
- **Fields** (like columns) identified by numbers
- **Tags** that tell us field number + value type

**4. Track Changes Markers**
When you insert text with track changes ON, Pages adds a `ChangeArchive` record with this pattern:
```
08 01 12 ...
```
Where:
- `08` = field 1 (kind = what type of change)
- `01` = value 1 (means "insertion")
- `12` = field 2 (session = who made the change)

For deletions, it's `08 02 12 ...` where `02` means "deletion".

**5. Counting**
Our detector simply counts how many times these byte patterns appear:
- If insertion markers > 21 (baseline threshold), flag as having redlines
- If deletion markers > 1 (baseline threshold), flag as having redlines

### Why Baselines?

Normal documents already have ~20 of these patterns for regular text styling (not actual track changes). So we use a threshold approach rather than just "any found = redlines".

### In Summary

| Step | Description |
|-------|-------------|
| 1 | Open .pages file (ZIP format) |
| 2 | Extract Index/Document.iwa |
| 3 | Decompress with Snappy |
| 4 | Scan for byte patterns (insertions/deletions) |
| 5 | Compare to threshold (insertions > 21 OR deletions > 1) |
| 6 | Return result: REDLINES or OK |

## Implementation

### Architecture

| Component | Description |
|-----------|-------------|
| `main.go` | CLI entry point and command-line argument parsing |
| `iwa/parser.go` | IWA file parsing (Snappy decompression + protobuf decoding) |
| `detector/types.go` | Type ID mappings, field constants, TrackChangesStatus enum |
| `detector/redline.go` | Redline detection logic + protobuf settings parsing |
| `bin/iwork-redline-detector` | Compiled binary executable |

**Project Structure:**

- `iwork-redline-detector/`
  - `main.go`
  - `iwa/`
    - `parser.go`
  - `detector/`
    - `types.go`
    - `redline.go`
  - `bin/`
    - `iwork-redline-detector`

### Detection Logic

The detector combines **direct settings field reads** with **heuristic change counting**:

**Primary Signal (Settings Fields):**
```go
1. Decompress Document.iwa
2. Read field 40 (change_tracking_enabled)
3. Decompress ViewState*.iwa (contains UIStateArchive)
4. Read field 28 from UIStateArchive (paused state)
5. Result: Definitive current Track Changes mode
```

**Secondary Signal (Heuristic):**
```go
1. Decompress Document.iwa with Snappy
2. Scan for byte patterns:
   - 0x08 0x01 0x12 = insertion marker
   - 0x08 0x02 0x12 = deletion marker
3. Count occurrences
4. Apply thresholds: insertions > 21 OR deletions > 1
5. Result: Actual tracked changes present
```

**Combination Logic:**
- If settings fields are found → use them as the primary source of truth
- If settings fields are unavailable → fall back to heuristic change detection
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

# Output results as CSV to a file
./bin/iwork-redline-detector -csv results.csv ./path/to/folder/

# Custom thread count (default: 2)
./bin/iwork-redline-detector -threads 4 ./path/to/folder/
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
```

**Debug mode** (aligned table, all columns):
```
FILEPATH                        REDLINES  INSERTIONS  DELETIONS  COMMENTS      SOURCE    STATUS                  CONF  FORMAT     
normal.pages                    false     20          1                                 Disabled                High  Modern     
comments.no-tracking.pages      true      20          1          Comments (1)  Comments  Paused                  High  Modern     
track.not-accepted.pages        true      22          1                       Tracked Changes  Enabled (With Changes)  High  Modern     
pages09/normal.pages            false     0           0                                 Disabled                High  Pages '09  
pages09/comments.no-tracking.pages  true  0           0          Comments (1)  Comments  Paused                  High  Pages '09  
```

**CSV output** (when `-csv <filename>` is specified):
```csv
filepath,redlines,insertions,deletions,comments,source,status,conf,format
normal.pages,false,20,1,,,Disabled,High,Modern
comments.no-tracking.pages,true,20,1,Comments (1),Comments,Paused,High,Modern
comments.track.pages,true,22,1,Comments (1),Tracked Changes + Comments,Enabled (With Changes),High,Modern
track.not-accepted.pages,true,22,1,,Tracked Changes,Enabled (With Changes),High,Modern
pages09/comments.no-tracking.pages,true,0,0,Comments (1),Comments,Paused,High,Pages '09
```

### Exit Codes

- `0`: processing completed successfully, regardless of whether redlines/comments were found
- `1`: usage or runtime error (invalid args, unreadable file, CSV write failure, etc.)

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

## Technical Notes

- **IWA files** use Snappy compression with 4-byte length prefix
- **Protobuf encoding** uses variable-length integers for field tags and values
- **Type IDs**: Messages are identified by integer type IDs (e.g., 1002 = DocumentArchive)
- **Field numbers**: Protocol buffer fields are identified by numbers (e.g., field 40 = change_tracking_enabled)
- **Snappy framing**: Custom variant without CRC-32C checksums or stream identifier chunks
- **Baseline false positives**: Normal documents have ~20 insertion patterns from character styling

## Limitations

- **Keynote and Numbers**: Only support comments/annotations, **not** inline track changes
- **ArchiveInfo parsing**: Full typed-message traversal in `iwa/parser.go` is still incomplete for some IWA structures, so detailed message extraction remains limited.
- **Heuristic threshold**: Modern format change detection uses insertion/deletion byte-pattern thresholds and may produce false positives on documents with unusual styling density.
- **Legacy XML**: Heuristic scanning is not applied to iWork '09 files; change counts come from XML parsing only.
- **Comments detection**: Comment redlines are supported for both modern Pages and legacy Pages '09, and comment status is reported even when tracked insertions/deletions also exist.
- **Modern comments**: Current modern comment detection is based on observed payload text patterns in `Document.iwa`, not full typed comment-archive traversal yet.
- **Change records**: Individual change author/timestamp parsing not yet fully implemented
- **Legal-grade detection**: For highest confidence, compare against a known-clean baseline document

## Related

- [[iWork-TrackChanges-Research-Report]]
