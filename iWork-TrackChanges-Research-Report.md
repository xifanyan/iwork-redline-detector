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

iWork '09 used **raw XML** inside ZIP bundles — `index.xml` or `index.xml.gz`. The complete schema was never made public by Apple, but the legacy samples in this repository are structured enough to inspect Apple-specific XML namespaces directly.

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
  // Note: change_tracking_paused is not reliably stored here in all Pages versions
}
```

**Detection:**
```
if change_tracking_enabled == true  → Track changes feature is ENABLED
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

Comments don't require track changes to be enabled. They use different protobuf messages, and the current implementation reports comment status independently from tracked insertions/deletions:

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

#### Practical Finding From Fixtures

The modern Pages fixtures in `testdata/pages/` show that comment-bearing documents can be detected from printable payload text inside decompressed `Document.iwa`, but this signal should be treated as a narrow heuristic:

- `comments.no-tracking.pages` contains a comment-only payload string and no tracked insertions/deletions
- `comments.track.pages` contains both tracked changes and a comment payload string
- `normal.pages` does not contain the same comment payload pattern

That means the safest current rule is:

```text
detect tracked insertions/deletions
detect comment signals separately
report both when present
```

This preserves comment visibility in the output while still requiring a narrow matcher so generic metadata strings containing the word `comment` do not become false positives.

### 4.6.1 Legacy Pages '09 Comments

Legacy Pages '09 comments are not stored in IWA archives. The repository samples show that `index.xml` contains a generic `<sf:comment>` node even in clean documents, so that tag is **not** a reliable comment signal on its own.

What *does* distinguish comment-bearing legacy samples is the presence of `sf:annotation` blocks:

- `testdata/pages09/comments.no-tracking.pages` → contains `<sf:annotation ...>`
- `testdata/pages09/comments.track.pages` → contains `<sf:annotation ...>`
- `testdata/pages09/normal.pages` → no `<sf:annotation ...>` block

Practical legacy rule:

```text
count <sf:annotation> elements as comments
report tracked changes separately from comments
```

This matches the intended business rule: comments count as redlines on their own, and tracked-change documents can still report comment status when both signals exist.

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
| **High** | Direct field scan | Reading settings fields from decompressed `Document.iwa` / `ViewState*.iwa` or legacy XML attributes |
| **Low** | Heuristic | Byte-pattern counting or narrow comment payload matching |

#### Limitations of Heuristic Detection

- **False Positives**: Documents with many character styles may exceed thresholds
- **No Author Info**: Byte patterns don't reveal who made changes
- **No Timestamps**: Cannot determine when changes were made
- **Mode Detection Depends on Known Fields**: If field locations change in future Pages versions, high-confidence status may regress to heuristic mode
- **Comment Heuristics Should Stay Narrow**: generic metadata strings containing `comment` are too broad; comment matching should remain stricter than a plain substring search

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
              │     ├── Decompress ViewState*.iwa (contains UIStateArchive)
              │     │     └── Read UIStateArchive field 28
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
               ├── Inspect comment signals separately
               │     ├── Modern: printable comment payloads in Document.iwa
               │     └── Legacy: <sf:annotation> elements in index.xml
              │
              └── Find AnnotationAuthorStorage.iwa
                    └── TSK.AnnotationAuthorStorageArchive
                          └── List of all authors with names + colors
```

#### Hybrid Detection Logic

| Enabled | Paused | Change Detected | Comment Detected | Final Status | Confidence |
|---|---|---|---|---|---|
| No | No | No | No | Disabled | High |
| Yes | No | No | No | Enabled (No Changes) | High |
| Yes | No | Yes | No | Enabled (With Changes) | High |
| Yes | No | Yes | Yes | Enabled (With Changes) + comments | High/Medium |
| Yes | Yes | No/Yes | No/Yes | Paused | High |
| Unknown | Unknown | Yes | No/Yes | Enabled (With Changes) | Low |
| Unknown | Unknown | No | Yes | Disabled + comment-only redline | Low/Medium |
| Unknown | Unknown | No | No | Disabled | Low |

---

## 6. Quick Reference Table

| What to Detect | Where to Look | How |
|---|---|---|
| **Track changes enabled?** | `Document.iwa` → `TP.DocumentArchive` | Field `change_tracking_enabled = true` |
| **Tracking paused?** | `ViewState*.iwa` → `TP.UIStateArchive` | Field `28 = true` (change_tracking_paused) |
| **Insertions exist?** | `Document.iwa` → `TSWP.TextStorageArchive.table_insertion` | Non-empty attribute table |
| **Deletions exist?** | `Document.iwa` → `TSWP.TextStorageArchive.table_deletion` | Non-empty attribute table |
| **Insertion count?** | `Document.iwa` → count `kChangeKindInsertion` entries | Scan ChangeArchive records |
| **Deletion count?** | `Document.iwa` → count `kChangeKindDeletion` entries | Scan ChangeArchive records |
| **Change author?** | `ChangeArchive.session` → `ChangeSessionArchive` | Author reference + date |
| **Hidden changes?** | `Document.iwa` → `ChangeArchive.hidden = true` entries | Hidden but present |
| **Comments exist? (modern)** | `Document.iwa` narrow payload scan | Report independently from tracked changes |
| **Comments exist? (legacy)** | `index.xml` → `sf:annotation` | Report independently from tracked changes |
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

## 10. Encryption Detection

### Overview

iWork Pages documents support password protection/encryption. The encryption mechanism differs between modern and legacy formats:

| Format | Encryption Method | Detection Approach |
|--------|-------------------|-------------------|
| **Modern** (2013+) | Individual files in `Index.zip` encrypted with AES128-CBC | Check for `.iwpv2` file |
| **Legacy** (iWork '09) | Entire ZIP archive encrypted as single unit | Check for missing `index.xml` |

**Important:** There are **no protobuf message fields** that indicate encryption status. Encryption is handled entirely at the file/bundle level.

---

### Modern Format Encryption

#### How It Works

When a modern iWork document is password-protected:

1. **Bundle structure remains intact** — The `.pages` bundle still contains `Index.zip`, `Metadata/`, `Data/` folders
2. **Files inside `Index.zip` are individually encrypted** using AES128 with PKCS7 padding
3. **Unencrypted files:** `Metadata/` folder and `Data/` folder (images, videos) remain accessible
4. **Encryption indicator files** are placed in the bundle root

#### Key Files

| File | Purpose | Size |
|------|---------|------|
| `.iwpv2` | Password verifier data | 98 bytes |
| `.iwph` | Password hint (if set) | Variable |

#### Password Verifier Structure (`.iwpv2`)

```c
typedef struct {
    uint16_t version;        // Must be 2
    uint16_t format;         // Must be 1
    uint32_t iterations;     // PBKDF2 iteration count
    uint8_t salt[16];        // Salt for key derivation
    uint8_t iv[16];          // AES IV
    uint8_t data[64];        // Encrypted verification block
} IWPasswordVerifierData;
```

#### Encrypted IWA Structure

Each encrypted `.iwa` file has this format:
```
[16 bytes IV][encrypted_bytes][20 bytes garbage]
```
After AES decryption, first 16 bytes are discarded (used as extra IV material), remaining bytes are Snappy-compressed IWA data.

---

### Detection Algorithm

#### Modern Format Detection

```
1. Open .pages file as ZIP
   │
2. Check for .iwpv2 file in bundle root
   │
   ├── NOT FOUND → File is NOT encrypted (or uses different protection)
   │
   └── FOUND → Open and verify structure
                ├── Size == 98 bytes?
                ├── Bytes[0:2] == version 2?  (uint16 LE)
                ├── Bytes[2:4] == format 1?   (uint16 LE)
                │
                ├── YES to all → File is ENCRYPTED
                │
                └── NO to any → File protection format unknown
                                 (may still be encrypted with different method)
```

#### Legacy Format Detection

```
1. Format detected as Legacy (has index.xml or index.xml.gz in ZIP)
   │
2. Try normal legacy XML parsing
   │
3. If parsing fails:
   │
4. Check: Does ZIP contain index.xml or index.xml.gz?
   │
   ├── YES → File is CORRUPT (not encrypted)
   │
   └── NO  → File is ENCRYPTED (legacy whole-bundle encryption)
```

#### Complete Decision Flow

```
Open .pages file
  │
  ├─ Format detected as Modern
  │   │
  │   ├─ .iwpv2 exists? (98 bytes, v=2, f=1)
  │   │   ├─ YES → ENCRYPTED (skip parsing)
  │   │   │
  │   │   └─ NO → Continue normal Modern parsing
  │   │           │
  │   │           ├─ Parse succeeds → Return result (IsEncrypted=false)
  │   │           │
  │   │           └─ Parse fails → Check .iwpv2 again (could be corrupted IWA + encryption)
  │   │                     │
  │   │                     ├─ .iwpv2 now found → ENCRYPTED
  │   │                     │
  │   │                     └─ .iwpv2 not found → ERROR (corrupt/unknown)
  │   │
  │   └─ Snappy decode fails?
  │       ├─ YES → Could be encrypted IWA (check .iwpv2)
  │       └─ NO → Continue normal parsing
  │
  └─ Format detected as Legacy
      │
      ├─ Parsing succeeds → Return result (IsEncrypted=false)
      │
      └─ Parsing fails
          │
          ├─ index.xml or index.xml.gz exists? → ERROR (corrupt file)
          │
          └─ Neither exists → ENCRYPTED (whole-bundle encryption)
```

---

### Visual Comparison

#### Unencrypted Modern .pages Structure

```
DocumentName.pages/
├── Data/                    # Images, videos (NOT encrypted)
├── Index.zip                # Contains valid IWA files
│   ├── Document.iwa         # Starts with Snappy header (0x01 or 0xff)
│   └── ...
├── Metadata/                 # Document metadata (NOT encrypted)
└── preview.jpg
```

#### Encrypted Modern .pages Structure

```
DocumentName.pages/
├── Data/                    # Images, videos (NOT encrypted)
├── Index.zip                # Contains encrypted .iwa files
│   ├── Document.iwa         # Starts with 16-byte AES IV (NOT Snappy!)
│   └── ...
├── Metadata/                 # Document metadata (NOT encrypted)
├── .iwpv2                   # ← ENCRYPTION INDICATOR (98 bytes)
└── .iwph                    # ← PASSWORD HINT (if set)
```

---

### Implementation Notes

#### Primary Detection: `.iwpv2` File

```go
func DetectEncryption(pagesPath string) (bool, error) {
    r, err := zip.OpenReader(pagesPath)
    if err != nil {
        return false, err
    }
    defer r.Close()

    for _, f := range r.File {
        if f.Name == ".iwpv2" {
            rc, err := f.Open()
            if err != nil {
                return false, err
            }
            defer rc.Close()
            data := make([]byte, 98)
            n, err := rc.Read(data)
            if n == 98 && data[0] == 2 && data[2] == 1 {
                return true, nil  // Encrypted
            }
        }
    }
    return false, nil  // Not encrypted
}
```

#### Encrypted IWA Header Check

Valid IWA files start with Snappy chunk types:
- `0x01` — compressed chunk
- `0xff` — uncompressed chunk

If `Document.iwa` first byte is neither `0x01` nor `0xff`, and `.iwpv2` exists, the file is likely encrypted.

#### Legacy Fallback Logic

```go
// After parsing fails for legacy format:
if format == FormatLegacyXML && parseError != nil {
    hasIndexXML := false
    for _, entry := range zipEntries {
        if entry.Name == "index.xml" || entry.Name == "index.xml.gz" {
            hasIndexXML = true
            break
        }
    }
    if !hasIndexXML {
        return ENCRYPTED  // Legacy whole-bundle encryption
    }
    return ERROR  // Actually corrupt
}
```

---

### Summary Table

| Detection Method | Modern | Legacy | Notes |
|-----------------|--------|--------|-------|
| `.iwpv2` file (98 bytes, v=2, f=1) | **YES** | N/A | Primary modern indicator |
| `.iwph` file | Yes (if hint set) | N/A | Secondary, optional |
| IWA doesn't start with Snappy header | Yes | N/A | Confirmatory only |
| `index.xml` missing on legacy parse failure | N/A | **YES** | Primary legacy indicator |
| Protobuf message fields | **None** | **None** | Encryption is file-level |

---

## 11. Implementation Checklist

- [x] Extract `Index.zip` from .pages bundle
- [x] Decode Snappy compression for each .iwa file
- [x] Parse Protobuf `ArchiveInfo` → `MessageInfo` chain
- [x] Load type ID mappings from Common.json + Pages.json
- [x] Check `Document.iwa` field `40` for `change_tracking_enabled`
- [x] Check `ViewState*.iwa` (UIStateArchive) field `28` for paused state
- [x] Scan decompressed bytes for insertion/deletion change markers (heuristic)
- [ ] Parse `TSWP.ChangeArchive` records for author/date (struct ready, parser not fully implemented)
- [x] Detect comment-only modern Pages redlines from observed `Document.iwa` payload patterns
- [x] Detect comment-only legacy Pages '09 redlines from `sf:annotation` elements in `index.xml`
- [x] Report comment status even when tracked insertions/deletions also exist
- [x] Parse `AnnotationAuthorStorage.iwa` for author list
- [x] Report markup visibility settings from `TP.SettingsArchive`
- [ ] Handle hidden changes (`hidden=true`)
