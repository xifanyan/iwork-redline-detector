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

Go-based tool to detect track changes and redlines in Apple iWork documents (.pages files). Uses protobuf-based detection with heuristic fallback to reliably identify:
- Track Changes feature enabled/disabled status
- Tracked insertions and deletions
- Track Changes paused state
- Markup visibility settings

## Research

This project was built based on detailed research of the iWork file format. See [[iWork-TrackChanges-Research-Report]] for the complete technical analysis including:
- IWA file format structure (Snappy + Protocol Buffers)
- Track changes protobuf message types
- ChangeArchive detection rules
- Legal considerations for redline workflows

## How It Works

### Detection Approach

The detector uses a **multi-signal detection strategy** to accurately identify track changes status:

#### 1. Protobuf-Based Detection (High Confidence)

When `Document.iwa` can be successfully parsed, the detector directly reads document settings:

- **`TP.DocumentArchive` (type 1002)**: Check field 40 (`change_tracking_enabled`) and field 45 (`change_tracking_paused`)
- **`TP.SettingsArchive` (type 1003)**: Read markup visibility settings

This provides **definitive** information about whether Track Changes is enabled, independent of content.

#### 2. Change Detection (Heuristic)

The detector also scans for actual tracked changes using byte-pattern detection:

- **Insertion markers**: `0x08 0x01 0x12` - indicates inserted text
- **Deletion markers**: `0x08 0x02 0x12` - indicates deleted text

Normal documents have ~20 insertion patterns from character styling, so we use thresholds:
- **Insertions > 21** → actual track changes detected
- **Deletions > 1** → actual track changes detected

#### 3. Combined Status

The detector combines both signals to determine the final status:

| Setting Enabled | Changes Detected | Status |
|---|---|---|
| No | No | `Disabled` |
| Yes | Yes | `Enabled (With Changes)` |
| Yes | No | `Enabled (No Changes)` |
| Unknown (protobuf failed) | Yes | `Enabled (With Changes)` - fallback |

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
Parse `TP.DocumentArchive` to find:
- **Field 40**: `change_tracking_enabled` (boolean)
- **Field 45**: `change_tracking_paused` (boolean)

**4. Count Changes**
Scan decompressed data for ChangeArchive patterns:
- **Kind 1**: Insertions (underlined text)
- **Kind 2**: Deletions (strikethrough text)

**5. Determine Status**
Combine settings and counts to produce final result.
DocumentName.pages/
└── Index/
    └── Document.iwa  ← This contains all the text and track changes
```

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
```
iwork-redline-detector/
├── main.go
├── iwa/
│   └── parser.go
├── detector/
│   ├── types.go
│   └── redline.go
└── bin/
    └── iwork-redline-detector
```

### Detection Logic

The detector combines **protobuf-based settings parsing** with **heuristic change counting**:

**Primary Signal (Protobuf):**
```go
1. Parse Document.iwa as IWA format
2. Find TP.DocumentArchive (type 1002)
3. Read field 40 (change_tracking_enabled)
4. Read field 45 (change_tracking_paused)
5. Result: Definitive Track Changes status
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
- If protobuf parses successfully → use as primary source of truth
- If protobuf fails → fall back to heuristic detection
- Set `HighConfidence` flag when protobuf data is available

**Track Changes Status Values:**

- **`Disabled`**: Track Changes feature is turned off. No tracked changes will be recorded.
- **`Enabled (No Changes)`**: Track Changes is turned on, but no insertions/deletions have been made yet.
- **`Enabled (With Changes)`**: Track Changes is on and actual tracked changes (insertions/deletions) are present in the document.

### Result Structure

```go
type RedlineDetection struct {
    TrackChangesStatus   TrackChangesStatus  // Disabled, EnabledNoChanges, EnabledWithChanges
    SettingEnabled       bool                // From DocumentArchive field 40
    SettingPaused        bool                // From DocumentArchive field 45
    HighConfidence       bool                // True if protobuf parsing succeeded

    InsertionCount int                   // From heuristic scan
    DeletionCount  int                   // From heuristic scan
    HiddenChanges  int                   // Hidden changes count

    Changes []Change                     // Individual change records (future enhancement)

    HasComments  bool                   // Comments detected separately
    CommentCount int

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

# With debug output (shows insertions/deletions count)
./bin/iwork-redline-detector -debug ./path/to/folder/

# Custom thread count (default: 2)
./bin/iwork-redline-detector -threads 4 ./path/to/folder/
```

### Output Formats

**Normal mode** (tab-separated):
```
pages.normal.pages	false
pages.track.pages	true
```

**Debug mode**:
```
FILEPATH                     | REDLINES | INSERTIONS | DELETIONS
pages.normal.pages          | false     | 20         | 1
pages.track.pages           | true      | 22         | 1
```

### Detection Confidence

The detector provides different levels of confidence based on data availability:

| Confidence Level | Source | Interpretation |
|---|---|---|
| **High** | Protobuf parsed successfully | Track Changes status is definitive (direct from document settings) |
| **Low** | Heuristic only | Status based on byte-pattern counting, may have false positives |

When protobuf parsing fails, the detector falls back to heuristic detection but sets confidence to low.

## Test Results

| File | Insertions | Deletions | Redlines | Status |
|------|------------|-----------|----------|---------|
| pages.normal.pages | 20 | 1 | false | Track Changes disabled |
| pages.track.pages | 22 | 1 | true | Enabled with changes |
| pages.blank.track.pages | 20 | 1 | false | Enabled, no changes yet |
| pages.notracking.deletion.pages | 21 | 2 | true | Deletions detected (no tracking) |

## Technical Notes

- **IWA files** use Snappy compression with 4-byte length prefix
- **Protobuf encoding** uses variable-length integers for field tags and values
- **Type IDs**: Messages are identified by integer type IDs (e.g., 1002 = DocumentArchive)
- **Field numbers**: Protocol buffer fields are identified by numbers (e.g., field 40 = change_tracking_enabled)
- **Snappy framing**: Custom variant without CRC-32C checksums or stream identifier chunks
- **Baseline false positives**: Normal documents have ~20 insertion patterns from character styling

## Limitations

- **Keynote and Numbers**: Only support comments/annotations, **not** inline track changes
- **Heuristic threshold**: May have false positives on documents with many character styles (when protobuf parsing fails)
- **Change records**: Individual change author/timestamp parsing not yet fully implemented (structure ready)
- **Legal-grade detection**: For highest confidence, compare against a known-clean baseline document
- **Protobuf without schema**: Full field-level parsing requires protobuf schema definitions

## Related

- [[iWork-TrackChanges-Research-Report]]
