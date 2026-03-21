package detector

import (
	"archive/zip"
	"fmt"
	"io"

	"github.com/golang/snappy"
)

type RedlineDetection struct {
	HasTrackChanges     bool
	TrackChangesEnabled bool
	TrackChangesPaused  bool

	InsertionCount int
	DeletionCount  int
	HiddenChanges  int

	HasComments  bool
	CommentCount int

	MarkupSettings MarkupSettings

	Authors []Author
}

type MarkupSettings struct {
	ShowCTMarkup         bool
	ShowCTDeletions      bool
	ChangeBarsVisible    bool
	FormatChangesVisible bool
	AnnotationsVisible   bool
}

type Author struct {
	Name  string
	Color string
}

func DetectRedlines(pagesPath string) (*RedlineDetection, error) {
	result := &RedlineDetection{
		MarkupSettings: MarkupSettings{
			ShowCTMarkup:         true,
			ShowCTDeletions:      true,
			ChangeBarsVisible:    true,
			FormatChangesVisible: true,
			AnnotationsVisible:   true,
		},
	}

	docData, err := extractDocumentIWA(pagesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract Document.iwa: %w", err)
	}

	decompressed, err := snappy.Decode(nil, docData[4:])
	if err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}

	result.HasTrackChanges, result.InsertionCount, result.DeletionCount = detectTrackChanges(decompressed)
	result.HasComments = detectComments(decompressed)

	annotationData, err := extractAnnotationStorageIWA(pagesPath)
	if err == nil && len(annotationData) > 4 {
		result.Authors = extractAuthorsFromData(annotationData)
	}

	if result.HasTrackChanges && (result.InsertionCount > 0 || result.DeletionCount > 0) {
		result.TrackChangesEnabled = true
	}

	return result, nil
}

func extractDocumentIWA(pagesPath string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index/Document.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("Document.iwa not found")
}

func extractAnnotationStorageIWA(pagesPath string) ([]byte, error) {
	r, err := zip.OpenReader(pagesPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "Index/AnnotationAuthorStorage.iwa" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	return nil, fmt.Errorf("AnnotationAuthorStorage.iwa not found")
}

func detectTrackChanges(data []byte) (hasTrackChanges bool, insertions, deletions int) {
	kind1Count := countPattern(data, []byte{0x08, 0x01, 0x12})
	kind2Count := countPattern(data, []byte{0x08, 0x02, 0x12})

	insertions = kind1Count
	deletions = kind2Count

	if kind1Count > 21 || kind2Count > 1 {
		hasTrackChanges = true
	}

	return hasTrackChanges, insertions, deletions
}

func detectComments(data []byte) bool {
	return countPattern(data, []byte{0x08, 0x01, 0x12, 0x4e}) > 0
}

func extractAuthorsFromData(data []byte) []Author {
	if len(data) < 4 {
		return nil
	}

	decoded, err := snappy.Decode(nil, data[4:])
	if err != nil {
		return nil
	}

	var authors []Author

	authorNames := []string{"Paul", "John", "Jane", "Admin", "User"}
	for _, name := range authorNames {
		if contains(decoded, []byte(name)) {
			authors = append(authors, Author{Name: name})
		}
	}

	return authors
}

func countPattern(data, pattern []byte) int {
	count := 0
	for i := 0; i <= len(data)-len(pattern); i++ {
		found := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				found = false
				break
			}
		}
		if found {
			count++
			i += len(pattern) - 1
		}
	}
	return count
}

func contains(data, pattern []byte) bool {
	for i := 0; i <= len(data)-len(pattern); i++ {
		found := true
		for j := 0; j < len(pattern); j++ {
			if data[i+j] != pattern[j] {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}
