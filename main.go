package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/rodaine/table"
	"github.com/xifanyan/iwork-redline-detector/detector"
)

var (
	debugFlag   = flag.Bool("debug", false, "Show detailed information (insertions, deletions, etc.)")
	threadsFlag = flag.Int("threads", 2, "Number of concurrent threads")
)

func main() {
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Println("Usage: iwork-redline-detector [-debug] [-threads N] <path-to.pages-file-or-directory>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	path := flag.Arg(0)

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var pagesFiles []string

	if info.IsDir() {
		pagesFiles, err = findPagesFiles(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding .pages files: %v\n", err)
			os.Exit(1)
		}
	} else {
		pagesFiles = []string{path}
	}

	threads := *threadsFlag
	if threads < 1 {
		threads = 1
	}

	fmt.Printf("Processing %d file(s) with %d thread(s)...\n\n", len(pagesFiles), threads)
	if *debugFlag {
		fmt.Printf("DEBUG: Found %d files: %v\n", len(pagesFiles), pagesFiles)
	}

	type result struct {
		file      string
		relPath   string
		detection *detector.RedlineDetection
		err       error
	}

	results := make(chan result, len(pagesFiles))

	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup

	for _, file := range pagesFiles {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			relPath, _ := filepath.Rel(path, f)
			if relPath == "." {
				relPath = filepath.Base(f)
			}

			r, err := detector.DetectRedlines(f)
			results <- result{file: f, relPath: relPath, detection: r, err: err}
		}(file)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	type row struct {
		filePath       string
		hasRedlines    bool
		insertionCount int
		deletionCount  int
		status         string
		confidence     string
		format         string
	}

	var rows []row
	redlinesFound := 0

	for res := range results {
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", res.file, res.err)
			continue
		}

		d := res.detection
		hasRedlines := d.SettingEnabled && d.TrackedChangesPresent
		if hasRedlines {
			redlinesFound++
		}

		rows = append(rows, row{
			filePath:       res.relPath,
			hasRedlines:    hasRedlines,
			insertionCount: d.InsertionCount,
			deletionCount:  d.DeletionCount,
			status:         d.TrackChangesStatus.String(),
			confidence:     map[bool]string{true: "High", false: "Low"}[d.HighConfidence],
			format:         d.Format.String(),
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].filePath < rows[j].filePath
	})

	if *debugFlag {
		tbl := table.New("FILEPATH", "REDLINES", "INSERTIONS", "DELETIONS", "STATUS", "CONF", "FORMAT")
		for _, r := range rows {
			tbl.AddRow(r.filePath, r.hasRedlines, r.insertionCount, r.deletionCount, r.status, r.confidence, r.format)
		}
		tbl.Print()
	} else {
		tbl := table.New("FILEPATH", "REDLINES", "FORMAT")
		for _, r := range rows {
			tbl.AddRow(r.filePath, r.hasRedlines, r.format)
		}
		tbl.Print()
	}

	if redlinesFound > 0 {
		os.Exit(1)
	}
}

func findPagesFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".pages") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
