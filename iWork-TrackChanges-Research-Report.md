# iWork File Format — Track Changes / Redline Detection
**Research Date:** 2026-03-20
**Author:** Claude (Macro Trading Assistant)
**Purpose:** Detect track changes & redlines in Apple iWork documents for legal document review workflows

> **Note:** This document is a technical research reference. For usage and implementation details, see [README.md](./README.md).

---

## 1. Format Overview

### Modern iWork (2013+, .pages / .numbers / .keynote)

iWork 2013+ uses a **bundle-based ZIP format**:

```
DocumentName.pages/
├── Data/                        # Media files (images, videos)
│   ├── thumb_*.jpg
│   └── preview*.jpg
├── Index.zip                    # All document data
│   ├── Document.iwa             # Main content (text, styles, etc.)
│   ├── DocumentStylesheet.iwa   # Styles
│   ├── Metadata.iwa            # Document metadata
│   ├── AnnotationAuthorStorage.iwa  # Author info for comments/markup
│   └── Tables/                 # (Numbers only)
│       └── *.iwa
└── Metadata/
    ├── BuildVersionHistory.plist
    ├── DocumentIdentifier
    └── Properties.plist
```

### Inside Index.zip

Each `.iwa` (iWork Archive) file uses a two-layer format:
1. **Snappy compression** (Google's framing format, but without CRC-32C checksums or stream identifier chunk)
2. **Google Protocol Buffers** (protobuf) — serialized message objects

```
IWA file = [Snappy chunk headers + compressed data] → [Protobuf stream]
                    ↓ decompress
Protobuf stream = [ArchiveInfo (metadata)] → [MessageInfo × N] → [Payload × N]
```

### Older iWork '09 (Pre-2013, XML-based)

iWork '09 used **raw XML** inside ZIP bundles — `index.xml.gz`. The complete schema was never made public by Apple. Detection would require Apple-specific XML namespace inspection.

---

## 2. IWA File Format (Technical Detail)

### ArchiveInfo Structure (Protobuf)

Each IWA contains an `ArchiveInfo` message at the start, followed by `MessageInfo` entries describing each payload:

```protobuf
message ArchiveInfo {
  optional uint64 identifier = 1;
  repeated MessageInfo message_infos = 2;  // describes the payloads below
}

message MessageInfo {
  optional uint64 length = 1;
  optional uint64 type = 2;        // ← Type ID maps to protobuf message name
  optional bytes archive_data = 3;  // ← The actual payload
}
```

### Type ID Mapping

The type ID is an integer that maps to a specific protobuf message type. Mappings vary per app (Pages, Numbers, Keynote). Recovered from the `TSPRegistry` class in the iWork executables.

**Sources for type mappings:**
- `Common.json` — shared types across all iWork apps
- `Pages.json` — Pages-specific types
- `Numbers.json` — Numbers-specific types
- `Keynote.json` — Keynote-specific types

---

## 3. Track Changes — What Each App Supports

### Pages — ✅ Full Track Changes

Pages is the only iWork app with **true track changes / redlines**:

| Feature | Supported |
|---|---|
| Track insertions (red underline) | ✅ |
| Track deletions (strikethrough) | ✅ |
| Change bars in margin | ✅ |
| Per-author colors | ✅ |
| Comments (separate from TC) | ✅ |
| Enable/disable track changes | ✅ |
| Pause/resume tracking | ✅ |
| Hidden changes (still tracked) | ✅ |

### Keynote — ⚠️ Comments Only

Keynote supports **annotations and comments**, but NOT inline track changes (no insertions/deletions tracked per character):

| Feature | Supported |
|---|---|
| Comment bubbles | ✅ |
| Annotation authors + colors | ✅ |
| Inline insertions/deletions | ❌ |
| Track changes | ❌ |

#### Keynote Comment Type IDs

| Type ID | Message Name | Purpose |
|---------|-------------|---------|
| 2006 | `KNShapeInfoArchive` | Shape information (may contain comments) |
| 2013 | `TSWP.HighlightArchive` | Highlighted text comments |
| 2014 | `TSWP.CommentInfoArchive` | Comment bubble metadata |

### Numbers — ⚠️ Comments Only

Numbers supports **cell comments**, but NOT track changes:

| Feature | Supported |
|---|---|
| Cell comments | ✅ |
| Annotation authors | ✅ |
| Inline insertions/deletions | ❌ |
| Track changes | ❌ |

#### Numbers Comment Type IDs

| Type ID | Message Name | Purpose |
|---------|-------------|---------|
| 2001 | `TSTTableInfoArchive` | Table information |
| 5000 | `TNCellCommentArchive` | Cell comment data |
| 2013 | `TSWP.HighlightArchive` | Highlighted text comments |
| 2014 | `TSWP.CommentInfoArchive` | Comment bubble metadata |

---

## 4. Track Changes Detection — Complete Reference

### 4.1 Protobuf Type ID Reference

**Source:** `Common.json` + `Pages.json` from [obriensp/iWorkFileFormat](https://github.com/obriensp/iWorkFileFormat)

#### Core Document Types

| Type ID | Message Name | App | Purpose |
|---------|-------------|-----|---------|
| **1001** | `TSWP.TextStorageArchive` | Pages | Text content with style attributes |
| **1002** | `TP.DocumentArchive` | Pages | Document settings and metadata |
| **1003** | `TP.SettingsArchive` | Pages | Markup visibility settings |
| **1004** | `TSWP.ChangeArchive` | Pages | Individual change record |
| **1005** | `TSWP.ChangeSessionArchive` | Pages | Change session (author + timestamp) |

#### Command and Comment Types

| Type ID | Message Name | App | Purpose |
|---------|-------------|-----|---------|
| **10148** | `TP.ChangeCTVisibilityCommandArchive` | Pages | Toggle change tracking visibility |
| **10149** | `TP.TrackChangesCommandArchive` | Pages | Command to enable track changes |
| **10157** | `TP.PauseChangeTrackingCommandArchive` | Pages | Pause/resume tracking |
| **2013** | `TSWP.HighlightArchive` | Pages | Highlighted text (linked to comments) |
| **2014** | `TSWP.CommentInfoArchive` | Pages | Comment bubble metadata |
| **3056** | `TSD.CommentStorageArchive` | Pages | Comment text + author + date |
| **3060** | `TSD.CommentStorageApplyCommandArchive` | Pages | Apply comment storage |
| **3057** | `TSD.DrawableInfoCommentCommandArchive` | Pages | Attach comment to drawable |
| **211** | `TSK.AnnotationAuthorArchive` | All | Author name + color for markup |
| **212** | `TSK.AnnotationAuthorStorageArchive` | All | List of all authors who made changes |

### 4.2 Document-Level Settings (TP.DocumentArchive)

In `Document.iwa` — protobuf message `TP.DocumentArchive`:

```protobuf
message DocumentArchive {
  // ... other fields ...
  optional bool change_tracking_enabled = 40;  // ← Track changes is ON
  optional bool change_tracking_paused  = 45;  // ← Tracking is paused
}
```

**Detection:**
```
if change_tracking_enabled == true  → Track changes feature is ENABLED
if change_tracking_paused  == true  → Tracking is PAUSED
```

### 4.3 Markup Visibility Settings (TP.SettingsArchive)

In `Document.iwa` — protobuf message `TP.SettingsArchive`:

```protobuf
message SettingsArchive {
  // ... other fields ...
  optional bool show_ct_markup          = 12;  // Show insertions (default: true)
  optional bool show_ct_deletions       = 13;  // Show deletions (default: true)
  optional int32 ct_bubbles_visibility  = 14;  // Change bubble visibility
  optional bool change_bars_visible     = 15;  // Change bars in margin (default: true)
  optional bool format_changes_visible  = 16;  // Show format changes (default: true)
  optional bool annotations_visible     = 17;  // Show comments (default: true)
  // ...
}
```

### 4.4 Tracked Changes — Text Storage (TSWP.TextStorageArchive)

The **actual redline records** live in the text content of `Document.iwa` inside `TSWP.TextStorageArchive`. This message stores all text in the document with style attributes:

```protobuf
message TextStorageArchive {
  optional .TSWP.ObjectAttributeTable table_insertion = 21;  // ← INSERTED text
  optional .TSWP.ObjectAttributeTable table_deletion  = 22;  // ← DELETED text
  optional .TSWP.ObjectAttributeTable table_highlight = 23;  // ← Highlights
  // ...
}

message StringAttributeTable {
  // Maps character index ranges to attribute values
  message Entry {
    required uint32 character_index = 1;    // Start char index
    required uint32 length = 2;             // Length of range
    optional .TSP.Reference value = 3;       // → ChangeArchive reference
  }
  repeated Entry entries = 1;
}
```

### 4.5 The ChangeArchive — Core Redline Record

```protobuf
message ChangeArchive {
  enum ChangeKind {
    kChangeKindInsertion = 1;  // ← Added text (shown as underlined)
    kChangeKindDeletion  = 2;  // ← Removed text (shown as strikethrough)
  }
  optional .TSWP.ChangeArchive.ChangeKind kind = 1;  // Change type
  optional .TSP.Reference session              = 2;  // → ChangeSessionArchive
  optional .TSP.Date date                     = 3;  // When change was made
  optional bool hidden                        = 4;  // Hidden changes (still tracked)
}

message ChangeSessionArchive {
  optional uint32 session_uid = 1;        // Unique session ID
  optional .TSP.Reference author;              // → Author name/color
  optional .TSP.Date date = 3;        // Session start date
}
```

**Redline Detection Rule:**
```
Presence of TSWP.ChangeArchive objects in TextStorageArchive's
table_insertion or table_deletion → Tracked changes/redlines exist

kind == 1 (kChangeKindInsertion) → Text was inserted
kind == 2 (kChangeKindDeletion)  → Text was deleted
session → TSWP.ChangeSessionArchive → Author + timestamp
hidden == true → Hidden change (visible in UI only when toggled)
```

### 4.6 Comments — Separate from Track Changes

Comments don't require track changes to be enabled. They use different protobuf messages:

```protobuf
// In Document.iwa — TSWP.HighlightArchive (type 2013)
message HighlightArchive {
  optional .TSP.Reference commentStorage = 1;  // → CommentStorageArchive
}

// In Document.iwa — TSWP.CommentInfoArchive (type 2014)
message CommentInfoArchive {
  required .TSWP.ShapeInfoArchive super = 1;
  optional .TSP.Reference comment_storage = 2;  // → CommentStorageArchive
}

// In Document.iwa — TSD.CommentStorageArchive (type 3056)
message CommentStorageArchive {
  optional string text             = 1;   // Comment text content
  optional .TSP.Date creation_date = 2;   // When created
  optional .TSP.Reference author;               // → AnnotationAuthorStorage
}
```

### 4.7 Author Information

```protobuf
// In AnnotationAuthorStorage.iwa — TSK.AnnotationAuthorArchive (type 212)
message AnnotationAuthorArchive {
  optional string name  = 1;       // Author display name
  optional .TSP.Color color = 2;   // Color used for this author's changes
}

// In AnnotationAuthorStorage.iwa — TSK.AnnotationAuthorStorageArchive (type 213)
message AnnotationAuthorStorageArchive {
  repeated .TSP.Reference annotation_author = 1;  // List of all authors
}
```

### 4.8 Heuristic Detection (Fallback)

When detailed protobuf message extraction fails or is unavailable, the detector uses **byte-pattern scanning** as a fallback method for tracked-change presence.

#### Why Heuristic Detection?

1. **Protobuf Parsing May Fail**: Complex IWA format or corruption can prevent successful protobuf decoding
2. **Graceful Degradation**: Without heuristic, malformed files would produce no results instead of best-effort detection
3. **Complementary Approach**: Byte patterns can detect tracked changes even when protobuf structure can't be fully parsed

#### Byte Patterns Used

The scanner looks for specific byte sequences that indicate ChangeArchive records:

| Change Type | Byte Pattern | Meaning |
|---|---|---|
| Insertion | `0x08 0x01 0x12` | Field 1 (kind) = 1, Field 2 (session) follows |
| Deletion | `0x08 0x02 0x12` | Field 1 (kind) = 2, Field 2 (session) follows |

#### Threshold-Based Detection

Normal documents already contain these byte patterns for **regular text styling** (not actual track changes):

- Baseline insertions: ~20 patterns from character styling attributes
- Baseline deletions: ~1 pattern from formatting

To avoid false positives, the detector uses thresholds:

```
has_redlines = (insertion_count > 21) OR (deletion_count > 1)
```

#### Confidence Levels

The detector reports confidence based on detection method:

| Confidence | Method | Source |
|---|---|---|
| **High** | Direct field scan | Reading settings fields from decompressed `Document.iwa` / `ViewState*.iwa` |
| **Low** | Heuristic | Byte-pattern counting, may have false positives |

#### Limitations of Heuristic Detection

- **False Positives**: Documents with many character styles may exceed thresholds
- **No Author Info**: Byte patterns don't reveal who made changes
- **No Timestamps**: Cannot determine when changes were made
- **Mode Detection Depends on Known Fields**: If field locations change in future Pages versions, high-confidence status may regress to heuristic mode

---

## 5. Detection Decision Tree

**Note:** The current implementation uses direct settings-field extraction as the primary signal and heuristic counting as the fallback for change presence.

```
Open .pages file
  └── Extract Index.zip
        └── Decode Document.iwa (Snappy → Protobuf)
              │
              ├── Decompress Document.iwa
              │     ├── Read field 40 directly
              │     │     └── change_tracking_enabled = true? → ENABLED
              │     ├── Decompress ViewState*.iwa
              │     │     └── Read field 28
              │     │           └── paused = true? → PAUSED
              │     └── If fields unavailable → Use Heuristic Detection (Low Confidence)
              │           └── Scan for byte patterns
              │                 ├── 0x08 0x01 0x12 → Insertions (count)
              │                 └── 0x08 0x02 0x12 → Deletions (count)
              │
              ├── Find TSWP.TextStorageArchive
              │     ├── table_insertion non-empty?
              │     │     └── YES → Insertion redlines exist
              │     ├── table_deletion non-empty?
              │     │     └── YES → Deletion redlines exist
              │     └── Entries reference TSWP.ChangeArchive
              │           ├── kind=1 → Insertion (underlined)
              │           ├── kind=2 → Deletion (strikethrough)
              │           └── session → Author + date via ChangeSessionArchive
              │
              ├── Find TSWP.HighlightArchive / TSWP.CommentInfoArchive
              │     └── YES → Comments exist (separate from track changes)
              │
              └── Find AnnotationAuthorStorage.iwa
                    └── TSK.AnnotationAuthorStorageArchive
                          └── List of all authors with names + colors
```

#### Hybrid Detection Logic

| Enabled | Paused | Change Detected | Final Status | Confidence |
|---|---|---|---|---|
| No | No | No | Disabled | High |
| Yes | No | No | Enabled (No Changes) | High |
| Yes | No | Yes | Enabled (With Changes) | High |
| Yes | Yes | No/Yes | Paused | High |
| Unknown | Unknown | Yes | Enabled (With Changes) | Low |
| Unknown | Unknown | No | Disabled | Low |

---

## 6. Quick Reference Table

| What to Detect | Where to Look | How |
|---|---|---|
| **Track changes enabled?** | `Document.iwa` → `TP.DocumentArchive` | Field `change_tracking_enabled = true` |
| **Tracking paused?** | `ViewState*.iwa` | Field `28 = true` |
| **Insertions exist?** | `Document.iwa` → `TSWP.TextStorageArchive.table_insertion` | Non-empty attribute table |
| **Deletions exist?** | `Document.iwa` → `TSWP.TextStorageArchive.table_deletion` | Non-empty attribute table |
| **Insertion count?** | `Document.iwa` → count `kChangeKindInsertion` entries | Scan ChangeArchive records |
| **Deletion count?** | `Document.iwa` → count `kChangeKindDeletion` entries | Scan ChangeArchive records |
| **Change author?** | `ChangeArchive.session` → `ChangeSessionArchive` | Author reference + date |
| **Hidden changes?** | `Document.iwa` → `ChangeArchive.hidden = true` entries | Hidden but present |
| **Comments exist?** | `Document.iwa` → `TSWP.HighlightArchive` / `CommentInfoArchive` | Present |
| **Comment authors?** | `AnnotationAuthorStorage.iwa` → `AnnotationAuthorStorageArchive` | Name + color per author |
| **Markup visible?** | `Document.iwa` → `TP.SettingsArchive` | `show_ct_markup`, `change_bars_visible`, etc. |
| **Heuristic detection?** | Decompressed bytes | Scan for `0x08 0x01 0x12` (insertion) and `0x08 0x02 0x12` (deletion) |

---

## 7. Parsing Pipeline

> **Note:** The actual implementation uses Go (see `detector/redline.go` and `iwa/parser.go`). The Python examples in this section are for illustrative purposes only.

### Step 1: Extract Index.zip

```python
import zipfile

with zipfile.ZipFile("Document.pages", 'r') as zf:
    with zf.open("Index.zip") as f:
        index_data = f.read()
```

### Step 2: Decompress IWA (Snappy)

iWork's Snappy variant: 4-byte header per chunk (1 byte type + 3 bytes little-endian length), no CRC, no stream identifier.

```python
def decompress_iwa(data):
    chunks = []
    i = 0
    while i < len(data):
        chunk_type = data[i]
        chunk_len = int.from_bytes(data[i+1:i+4], 'little')
        payload = data[i+4:i+4+chunk_len]
        if chunk_type == 0x01:  # compressed chunk
            chunks.append(snappy.decompress(payload))
        elif chunk_type == 0xff:  # uncompressed chunk
            chunks.append(payload)
        i += 4 + chunk_len
    return b''.join(chunks)
```

### Step 3: Decode Protobuf

Parse the `ArchiveInfo` → `MessageInfo` → `Payload` chain, then use type ID mappings.

### Step 4: Check Type IDs

```python
COMMON_TYPES = {
    211: "TSK.AnnotationAuthorArchive",
    212: "TSK.AnnotationAuthorStorageArchive",
    2013: "TSWP.HighlightArchive",
    2014: "TSWP.CommentInfoArchive",
    3056: "TSD.CommentStorageArchive",
}

PAGES_TYPES = {
    10148: "TP.ChangeCTVisibilityCommandArchive",
    10149: "TP.TrackChangesCommandArchive",
    10157: "TP.PauseChangeTrackingCommandArchive",
}
```

---

## 8. Legal Redline Considerations

### Pages Track Changes — Limitations

Pages' track changes is designed for **single-author revision tracking**, not multi-party legal redline workflows:

| Legal Requirement | Pages | Word (Industry Standard) |
|---|---|---|
| Multi-party markup (Party A + Party B edits tracked separately) | ❌ Limited | ✅ Yes |
| .docx export preserves track changes | ⚠️ Risky | ✅ Native |
| Version control + approval workflow | ❌ No | ✅ Via SharePoint/iManage |
| eDiscovery-compatible audit trail | ❌ No | ✅ Yes |
| DMS integration (NetDocuments, iManage) | ❌ .pages not native | ✅ .docx native |

### Recommendation for Legal Use

1. **Pages** — fine for initial internal draft review
2. **Export to .docx** — for external exchange (but verify formatting)
3. **Word** — for full legal redline workflows (multi-party exchange)
4. **Legal DMS** — for compliance and audit trail requirements

---

## 9. Reference Resources

| Resource | URL | Notes |
|---|---|---|
| **obriensp/iWorkFileFormat** | https://github.com/obriensp/iWorkFileFormat | Authoritative protobuf specs + type mappings |
| **orcastor/iwork-converter** | https://github.com/orcastor/iwork-converter | Go converter, proto files + type JSON |
| **stingrayreader** | https://stingrayreader.sourceforge.net/ | Python IWA parser (protobuf + snappy) |
| **6over3/WorkKit** | https://github.com/6over3/WorkKit | Swift iWork parser library |
| **pyiwa** | https://github.com/matchaxnb/pyiwa | Python IWA reader |
| **Apple iWork XML Guide** | https://leopard-adc.pepas.com/documentation/AppleApplications/Conceptual/iWork2-0_XML/Chapter01/Introduction.html | Pre-2013 XML format docs |

### Key Protobuf Definition Files

```
obriensp/iWorkFileFormat/iWorkFileInspector/
├── Messages/Proto/
│   ├── TSPArchiveMessages.proto   # Common types (TSP)
│   ├── TSWPArchives.proto         # Text/word processing types (Pages)
│   ├── TPArchives.proto           # Pages document types
│   ├── TSDArchives.proto         # Drawable/shape types
│   ├── TSTArchives.proto         # Table types (Numbers)
│   ├── TNArchives.proto          # Numbers document types
│   ├── TSKArchives.proto         # Keynote/document structure types
│   └── KNArchives.proto          # Keynote presentation types
└── Persistence/MessageTypes/
    ├── Common.json    # Type ID → message name mappings (all apps)
    ├── Pages.json     # Pages-specific type IDs
    ├── Numbers.json   # Numbers-specific type IDs
    └── Keynote.json   # Keynote-specific type IDs
```

---

## 10. Implementation Checklist

- [x] Extract `Index.zip` from .pages bundle
- [x] Decode Snappy compression for each .iwa file
- [x] Parse Protobuf `ArchiveInfo` → `MessageInfo` chain
- [x] Load type ID mappings from Common.json + Pages.json
- [x] Check `Document.iwa` field `change_tracking_enabled`
- [x] Check `ViewState*.iwa` paused-state field
- [x] Scan `TSWP.TextStorageArchive.table_insertion` for insertions
- [x] Scan `TSWP.TextStorageArchive.table_deletion` for deletions
- [ ] Parse `TSWP.ChangeArchive` records for author/date (struct ready, parser not fully implemented)
- [x] Check `TSWP.HighlightArchive` for comments
- [x] Parse `AnnotationAuthorStorage.iwa` for author list
- [x] Report markup visibility settings from `TP.SettingsArchive`
- [x] Handle hidden changes (`hidden=true`)
