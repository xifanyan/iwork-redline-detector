package detector

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRedlines(t *testing.T) {
	testdataDir := filepath.Join("..", "testdata")

	tests := []struct {
		name               string
		filename           string
		wantStatus         TrackChangesStatus
		wantEnabled        bool
		wantPaused         bool
		wantTrackedChanges bool
		wantConfidence     bool
		minInsertions      int
		maxInsertions      int
		wantDeletions      int
	}{
		{
			name:               "normal document without tracking",
			filename:           "normal.pages",
			wantStatus:         TCStatusDisabled,
			wantEnabled:        false,
			wantPaused:         false,
			wantTrackedChanges: false,
			wantConfidence:     true,
			minInsertions:      19,
			maxInsertions:      21,
			wantDeletions:      1,
		},
		{
			name:               "blank document with tracking enabled (no changes)",
			filename:           "blank.track.pages",
			wantStatus:         TCStatusEnabledNoChanges,
			wantEnabled:        true,
			wantPaused:         false,
			wantTrackedChanges: false,
			wantConfidence:     true,
			minInsertions:      19,
			maxInsertions:      21,
			wantDeletions:      1,
		},
		{
			name:               "tracking enabled but all changes accepted (no pending redlines)",
			filename:           "normal.track.accepted.pages",
			wantStatus:         TCStatusEnabledNoChanges,
			wantEnabled:        true,
			wantPaused:         false,
			wantTrackedChanges: false,
			wantConfidence:     true,
			minInsertions:      20,
			maxInsertions:      22,
			wantDeletions:      1,
		},
		{
			name:               "tracking enabled with unaccepted changes",
			filename:           "track.not-accepted.pages",
			wantStatus:         TCStatusEnabledWithChanges,
			wantEnabled:        true,
			wantPaused:         false,
			wantTrackedChanges: true,
			wantConfidence:     true,
			minInsertions:      21,
			maxInsertions:      23,
			wantDeletions:      1,
		},
		{
			name:               "tracking paused with deletions present",
			filename:           "deletion.track-paused.pages",
			wantStatus:         TCStatusPaused,
			wantEnabled:        true,
			wantPaused:         true,
			wantTrackedChanges: true,
			wantConfidence:     true,
			minInsertions:      20,
			maxInsertions:      22,
			wantDeletions:      2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pagesPath := filepath.Join(testdataDir, tt.filename)

			info, err := os.Stat(pagesPath)
			if err != nil {
				t.Skipf("skipping %s: test file not found: %v", tt.name, err)
			}
			if info.IsDir() {
				t.Skipf("skipping %s: path is a directory", tt.name)
			}

			result, err := DetectRedlines(pagesPath)
			if err != nil {
				t.Fatalf("DetectRedlines(%s) returned error: %v", tt.name, err)
			}

			if result.TrackChangesStatus != tt.wantStatus {
				t.Errorf("DetectRedlines(%s) status = %v, want %v", tt.name, result.TrackChangesStatus, tt.wantStatus)
			}

			if result.SettingEnabled != tt.wantEnabled {
				t.Errorf("DetectRedlines(%s) SettingEnabled = %v, want %v", tt.name, result.SettingEnabled, tt.wantEnabled)
			}

			if result.SettingPaused != tt.wantPaused {
				t.Errorf("DetectRedlines(%s) SettingPaused = %v, want %v", tt.name, result.SettingPaused, tt.wantPaused)
			}

			if result.TrackedChangesPresent != tt.wantTrackedChanges {
				t.Errorf("DetectRedlines(%s) TrackedChangesPresent = %v, want %v", tt.name, result.TrackedChangesPresent, tt.wantTrackedChanges)
			}

			if result.HighConfidence != tt.wantConfidence {
				t.Errorf("DetectRedlines(%s) HighConfidence = %v, want %v", tt.name, result.HighConfidence, tt.wantConfidence)
			}

			if result.InsertionCount < tt.minInsertions || result.InsertionCount > tt.maxInsertions {
				t.Errorf("DetectRedlines(%s) InsertionCount = %d, want between %d and %d",
					tt.name, result.InsertionCount, tt.minInsertions, tt.maxInsertions)
			}

			if result.DeletionCount != tt.wantDeletions {
				t.Errorf("DetectRedlines(%s) DeletionCount = %d, want %d", tt.name, result.DeletionCount, tt.wantDeletions)
			}
		})
	}
}

func TestDetectRedlines_FileNotFound(t *testing.T) {
	_, err := DetectRedlines("/nonexistent/pages.pages")
	if err == nil {
		t.Error("DetectRedlines expected error for nonexistent file, got nil")
	}
}

func TestDetectRedlines_InvalidPages(t *testing.T) {
	tmpFile := "/tmp/invalid.pages"
	err := os.WriteFile(tmpFile, []byte("not a zip file"), 0644)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile)

	_, err = DetectRedlines(tmpFile)
	if err == nil {
		t.Error("DetectRedlines expected error for invalid file, got nil")
	}
}

func TestCountPattern(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		pattern  []byte
		expected int
	}{
		{
			name:     "empty data",
			data:     []byte{},
			pattern:  []byte{0x08},
			expected: 0,
		},
		{
			name:     "no match",
			data:     []byte{0x00, 0x01, 0x02},
			pattern:  []byte{0x08},
			expected: 0,
		},
		{
			name:     "single match",
			data:     []byte{0x00, 0x08, 0x01},
			pattern:  []byte{0x08},
			expected: 1,
		},
		{
			name:     "multiple matches",
			data:     []byte{0x08, 0x00, 0x08, 0x01, 0x08},
			pattern:  []byte{0x08},
			expected: 3,
		},
		{
			name:     "insertion pattern",
			data:     []byte{0x08, 0x01, 0x12, 0x00},
			pattern:  []byte{0x08, 0x01, 0x12},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countPattern(tt.data, tt.pattern)
			if result != tt.expected {
				t.Errorf("countPattern() = %d, want %d", result, tt.expected)
			}
		})
	}
}
