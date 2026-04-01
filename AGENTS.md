# AGENTS.md - iWork Redline Detector

## Project Overview
This is a Go project that detects redlines (track changes) and comments in Apple iWork .pages documents. It supports modern IWA format, Pages 2013 Index.zip format, and legacy Pages '09 XML format.

## Build Commands

### Build
```bash
go build -ldflags="-s -w" -trimpath -o bin/release/iwork-redline-detector-windows-amd64.exe .
```

### Run
```bash
go run main.go [-debug] [-csv <filename>] [-errors-csv <filename>] [-threads N] [-filelist <path>] <path-to.pages-file-or-directory>
```

### Test All
```bash
go test ./...
```

### Run Single Test
```bash
go test -v -run TestDetectRedlines ./detector/
go test -v -run TestParseMessage ./iwa/
```

### Run Tests in Specific Package
```bash
go test -v ./detector/
go test -v ./iwa/
```

## Code Style

### Go Version
Go 1.23.3

### Formatting
- Use `gofmt` for formatting (standard Go style)
- Tab indentation, not spaces
- One package per file
- Group imports: stdlib first, then third-party, then internal

### Naming Conventions
- **Types**: PascalCase (e.g., `RedlineDetection`, `TrackChangesStatus`, `FormatType`)
- **Constants**: Mixed - some PascalCase for type constants (`TCStatusDisabled`), some camelCase for field constants (`FieldChangeTrackingEnabled`)
- **Variables/Functions**: camelCase (e.g., `DetectRedlines`, `isEncrypted`)
- **Acronyms**: Keep original case (e.g., `IWA`, `XML`, `JSON`)

### Error Handling
- Use `fmt.Errorf("message: %w", err)` for wrapping errors
- Return early on errors when possible
- Use `os.IsNotExist(err)` for file-not-found checks
- Check encryption errors using `isEncryptionError()` helper

### Types Pattern
```go
type TrackChangesStatus int
type FormatType int

const (
    TCStatusUnknown TrackChangesStatus = iota
    TCStatusDisabled
    TCStatusPaused
    TCStatusEnabledNoChanges
    TCStatusEnabledWithChanges
)
```

### String Representations
Implement `String() string` methods for user-facing types:
```go
func (s TrackChangesStatus) String() string
func (f FormatType) String() string
```

### Testing
- Table-driven tests preferred
- Use `t.Run(name, func(t *testing.T))` for subtests
- Use `t.Skipf()` for optional tests when test files are missing
- Use `t.TempDir()` for temporary test files
- Test naming: `Test<FunctionName>_<Scenario>`

### Package Structure
```
.
├── main.go           # CLI entry point
├── detector/         # Core redline detection logic
│   ├── redline.go    # Main detection functions
│   ├── types.go      # Type definitions and constants
│   └── redline_test.go
└── iwa/              # IWA file parsing (protobuf-like)
    ├── parser.go     # IWA parsing logic
    └── parser_test.go
```

### Key Dependencies
- `github.com/golang/snappy` - Snappy compression
- `github.com/rodaine/table` - ASCII table output
- `github.com/schollz/progressbar/v3` - Progress bar

### Imports Grouping
```go
import (
    "archive/zip"
    "bytes"
    "compress/gzip"
    "encoding/xml"
    "fmt"
    "io"
    "strings"
    "time"

    "github.com/golang/snappy"
    "github.com/xifanyan/iwork-redline-detector/iwa"
)
```

### Important Patterns

#### Nil-safe Methods
Implement nil-safe receivers for pointer types:
```go
func (r *RedlineDetection) HasRedlines() bool {
    if r == nil {
        return false
    }
    return (r.SettingEnabled && r.TrackedChangesPresent) || r.HasComments
}
```

#### Boolean Field Detection
Pattern for detecting protobuf-style boolean fields in raw bytes:
```go
func detectBooleanFieldValue(data []byte, fieldNum uint64) (bool, bool)
```

#### Compression Detection
IWA files use Snappy compression with a custom header:
```go
func DecompressSnappy(data []byte) ([]byte, error)
```

#### XML Decoding for Legacy Format
Use `xml.NewDecoder` with token-based parsing for legacy index.xml files.

### Constants Organization
Group related constants:
- Type constants for protobuf message types (e.g., `TypeSettingsArchive`)
- Field constants (e.g., `FieldChangeTrackingEnabled`)
- Change kind constants (e.g., `ChangeKindInsertion`)

### CLI Flag Definitions
Use `flag` package in `package main`:
```go
var (
    debugFlag     = flag.Bool("debug", false, "Show detailed information")
    csvFlag       = flag.String("csv", "", "Output results as CSV")
    threadsFlag   = flag.Int("threads", 2, "Number of concurrent threads")
)
```

## Format Detection

### Format Types
- **Modern**: Has `Index/Document.iwa` directly in the .pages bundle
- **Pages 2013**: Has `Index.zip` in the .pages bundle (contains Index/ folder with IWA files)
- **Legacy XML**: Has `index.xml` or `index.xml.gz` in the .pages bundle

### Important: Index.zip Precedence
Format detection checks for `Index.zip` BEFORE `Index/Document.iwa`. If both exist, `Index.zip` takes precedence since it indicates Pages 2013 format.

```go
// detector/types.go - DetectFormat()
if hasIndexZip {
    return FormatPages2013   // Check Index.zip FIRST
}
if hasIndexDocument {
    return FormatModernIWA  // Then check direct IWA
}
```

## Pages 2013 Format (Index.zip)

### Structure
- `.pages` bundle contains `Index.zip` at root
- `Index.zip` contains `Index/` folder with IWA files (same as modern format)
- IWA files inside `Index.zip` are DEFLATE compressed (method=8)

### Creating Pages 2013 Test Files
When creating test files by replacing Index.zip in a Pages 2013 bundle:
- **Use `zip -0`** to store IWA files without compression
- This preserves IWA bytes exactly, ensuring consistent parsing
- Without `-0`, different compression may alter bytes or require extra decompression

```bash
# Correct: No compression, bytes preserved exactly
zip -0 -r Index.zip Index/

# Wrong: May alter bytes through compression
zip -r Index.zip Index/
```

### Why zip -0 Matters
- IWA files use Snappy compression with specific byte patterns
- DEFLATE compression (default) may modify the underlying IWA bytes
- Even when decompressed, the bytes may differ from original
- Using `-0` (store only) ensures byte-for-byte identical IWA data

### Comment Detection Logic

#### 2014+ (Modern) - Uses Document.iwa directly
1. Extract `Index/Document.iwa` from bundle
2. Decompress snappy data
3. Call `detectCommentsInData(decompressed)` to find comment patterns
4. Uses `looksLikeCommentContent()` heuristic

#### 2013 (Index.zip) - Uses IWA files inside Index.zip
1. Extract `Index.zip` from bundle, then extract IWA from inside
2. Decompress each IWA file's snappy data
3. For each IWA, call `looksLikeCommentContent()` on decompressed data
4. Count files that contain comment-like content
5. If count > 0, document has comments

### Key Insight
When Index.zip is created with `zip -0`, the IWA bytes inside are identical to 2014+ format. The comment detection logic works the same way - it's just accessing the data through an extra zip layer.
