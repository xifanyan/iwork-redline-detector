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

Go-based tool to detect track changes and redlines in Apple iWork documents (.pages files).

## Research

This project was built based on detailed research of the iWork file format. See [[iWork-TrackChanges-Research-Report]] for the complete technical analysis including:
- IWA file format structure (Snappy + Protocol Buffers)
- Track changes protobuf message types
- ChangeArchive detection rules
- Legal considerations for redline workflows

## How It Works

### The Short Version

Pages stores document content in a file called `Document.iwa`. When you enable track changes and edit the document, Pages adds special markers around changed text. Our detector finds these markers.

### Step-by-Step

**1. File Structure**
A `.pages` file is actually a ZIP archive containing folders. The main document content is in:
```
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

```
.pages file → ZIP → Index/Document.iwa → Snappy decompress →
Search for byte patterns → Count them → Compare to threshold →
REDLINES or OK
```

## Implementation

### Architecture

```
├── main.go              # CLI entry point
├── iwa/
│   └── parser.go        # IWA file parsing (Snappy + raw protobuf)
├── detector/
│   ├── types.go         # Type ID mappings (reference)
│   └── redline.go       # Redline detection logic
└── bin/
    └── iwork-redline-detector  # Compiled binary
```

### Detection Approach

The implementation uses raw byte-pattern scanning on decompressed IWA data:

1. **Extract** `Index/Document.iwa` from .pages bundle (ZIP format)
2. **Decompress** using Snappy framing format (skip 4-byte header, decode remaining)
3. **Scan** for ChangeArchive protobuf patterns:
   - `0x08 0x01 0x12` = kind=1 (insertion) ChangeArchive marker
   - `0x08 0x02 0x12` = kind=2 (deletion) ChangeArchive marker
4. **Threshold-based detection**: If insertion patterns > 21 or deletion patterns > 1, flag as having redlines

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

## Test Results

| File | Insertions | Deletions | Redlines |
|------|------------|-----------|----------|
| pages.normal.pages | 20 | 1 | false |
| pages.track.pages | 22 | 1 | true |
| pages.changes.pages | 21 | 1 | false (tracking OFF) |

## Technical Notes

- IWA files use Snappy compression with 4-byte length prefix
- Protobuf encoding uses group syntax (wire types 6, 7) in addition to standard types
- ChangeArchive patterns appear multiple times in normal documents due to character styling
- A baseline comparison approach (track vs normal) provides more accurate detection

## Limitations

- **Keynote and Numbers do not have track changes/redlines feature** - only Pages supports this
- Threshold-based detection may have false positives on documents with many character styles
- For legal-grade detection, compare against a known-clean baseline document
- Complex protobuf structure prevents reliable field-level parsing without schema definitions

## Related

- [[iWork-TrackChanges-Research-Report]]
